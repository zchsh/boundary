// +build linux darwin windows

package schema

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/boundary/internal/docker"
	"github.com/hashicorp/vault/sdk/helper/base62"
	"github.com/ory/dockertest/v3"
)

const TestingDbEnvKey = "BOUNDARY_DB_TEST"

func init() {
	docker.GetInitializedDb = getInitializedDbSupported
	docker.StartDbInDocker = startDbInDockerSupported
}

var (
	dockerSetup sync.Once
)

var (
	mainContainer, mainUrl string
	mainDb                 *sql.DB
)

func noopclean() error { return nil }

func disconnectFromDatabase(dbName string, db *sql.DB) func() error {
	return func() error {
		if _, err := db.Exec(fmt.Sprintf("SELECT pg_terminate_backend(pg_stat_activity.pid) FROM pg_stat_activity  WHERE pg_stat_activity.datname = '%s' AND pid <> pg_backend_pid()", dbName)); err != nil {
			return fmt.Errorf("Unable to terminate connection to db %q: %w", dbName, err)
		}

		// if _, err := db.Exec(fmt.Sprintf("drop database %s", dbName)); err != nil {
		// 	return fmt.Errorf("Couldn't remove table: %w", err)
		// }
		return nil
	}
}

func createNewInitializedDatabaseFromMain(u string, d *sql.DB) (outUrl, dbName string, err error) {
	n, err := base62.Random(5)
	if err != nil {
		return "", "", fmt.Errorf("Couldn't generate new db suffix: %w", err)
	}
	dbName = strings.ToLower(fmt.Sprintf("boundary_%s_%d", n, time.Now().Unix()))
	if _, err := d.Exec(fmt.Sprintf("CREATE DATABASE %s template boundary", dbName)); err != nil {
		return "", "", fmt.Errorf("Unable to create sub db: %w", err)
	}

	nu, err := url.Parse(u)
	if err != nil {
		return "", "", fmt.Errorf("Unable to parse url: %w", err)
	}
	nu.Path = dbName
	return nu.String(), dbName, nil
}

func createNewFreshDatabaseFromMain(u string, d *sql.DB) (outUrl, dbName string, err error) {
	n, err := base62.Random(5)
	if err != nil {
		return "", "", fmt.Errorf("Couldn't generate new db suffix: %w", err)
	}
	dbName = strings.ToLower(fmt.Sprintf("boundary_%s_%d", n, time.Now().Unix()))
	if _, err := d.Exec(fmt.Sprintf("CREATE DATABASE %s", dbName)); err != nil {
		return "", "", fmt.Errorf("Unable to create sub db: %w", err)
	}

	nu, err := url.Parse(u)
	if err != nil {
		return "", "", fmt.Errorf("Unable to parse url: %w", err)
	}
	nu.Path = dbName
	return nu.String(), dbName, nil
}

func setupMainDb(ctx context.Context, dialect string) (u string, db *sql.DB, outErr error) {
	dockerSetup.Do(func() {
		log.Printf("Got env variables: %v", os.Environ())
		mainUrl = os.Getenv(TestingDbEnvKey)
		if mainUrl != "" {
			var err error
			mainDb, err = sql.Open(dialect, mainUrl)
			if err != nil {
				fmt.Printf("Failed to connedt to db: %v\n", err)
				outErr = fmt.Errorf("failed connecting to main db %w", err)
				return
			}
			time.Sleep(10 * time.Second)

			ctx, _ := context.WithTimeout(ctx, 2*time.Minute)
			if err := mainDb.PingContext(ctx); err != nil {
				outErr = fmt.Errorf("Unable to ping db: %w", err)
				return
			}
			return
		}
		log.Fatal("Should have had the environment variable set!")

		pool, err := dockertest.NewPool("")
		if err != nil {
			outErr = fmt.Errorf("Couldn't create pool %w", err)
			return
		}
		resource, err := pool.Run("postgres", "12", []string{"POSTGRES_PASSWORD=password"})
		if err != nil {
			outErr = fmt.Errorf("Couldn't docker run:%w", err)
			return
		}
		mainUrl = fmt.Sprintf("postgres://postgres:password@%s?sslmode=disable", resource.GetHostPort("5432/tcp"))
		mainDb, err = sql.Open(dialect, mainUrl)
		if err != nil {
			fmt.Printf("Failed to connedt to db: %v\n", err)
			outErr = fmt.Errorf("failed connecting to main db %w", err)
			return
		}
		time.Sleep(10 * time.Second)

		ctx, _ := context.WithTimeout(ctx, 2*time.Minute)
		if err := mainDb.PingContext(ctx); err != nil {
			outErr = fmt.Errorf("Unable to ping db: %w", err)
			return
		}

		if _, err := mainDb.Exec("CREATE DATABASE boundary WITH is_template true"); err != nil {
			fmt.Printf("Failed updating to template: %v\n", err)
			outErr = fmt.Errorf("Unable to create the boundary db: %w", err)
			return
		}

		// Remove the 'boundary' template table from the URL so we aren't connecting to it.
		nu, err := url.Parse(mainUrl)
		if err != nil {
			outErr = fmt.Errorf("Unable to parse url: %w", err)
			return
		}
		nu.Path = "boundary"

		pdb, err := sql.Open("postgres", nu.String())
		if err != nil {
			fmt.Printf("Failed to connect to boundary db: %v\n", err)
			outErr = fmt.Errorf("failed to connect to boundary db: %w", err)
			return
		}
		pdb.SetMaxIdleConns(0)
		time.Sleep(10 * time.Second)

		ctx, _ = context.WithTimeout(ctx, 30*time.Second)
		if err := pdb.PingContext(ctx); err != nil {
			outErr = fmt.Errorf("Unable to ping db: %w", err)
			return
		}

		nsm, err := NewManager(ctx, dialect, pdb)
		if err != nil {
			fmt.Printf("Failed getting schema manager: %v\n", err)
			outErr = err
			return
		}
		if err := nsm.RollForward(ctx); err != nil {
			fmt.Printf("Failed rolling forward: %v\n", err)
			outErr = err
			return
		}
		if err := pdb.Close(); err != nil {
			outErr = fmt.Errorf("Unable to close the boundary db connection: %w", err)
			return
		}

		if _, err := mainDb.Exec("SELECT pg_terminate_backend(pg_stat_activity.pid) FROM pg_stat_activity  WHERE pg_stat_activity.datname = 'boundary' AND pid <> pg_backend_pid()"); err != nil {
			outErr = fmt.Errorf("Unable to terminate backends: %w", err)
			return
		}
	})
	return mainUrl, mainDb, outErr
}

func getInitializedDbSupported(dialect string) (cleanup func() error, retURL, container string, err error) {
	ctx := context.TODO()
	mURL, mDB, err := setupMainDb(ctx, dialect)
	if err != nil {
		return noopclean, "", "", err
	}
	u, dbName, err := createNewInitializedDatabaseFromMain(mURL, mDB)
	if err != nil {
		return noopclean, "", "", fmt.Errorf("Unable to create new db at %q: %w", mainUrl, err)
	}
	return disconnectFromDatabase(dbName, mainDb), u, "", nil
}

func startDbInDockerSupported(dialect string) (cleanup func() error, retURL, container string, err error) {
	ctx := context.TODO()
	mURL, mDB, err := setupMainDb(ctx, dialect)
	if err != nil {
		return noopclean, "", "", err
	}
	u, dbName, err := createNewFreshDatabaseFromMain(mURL, mDB)
	if err != nil {
		return noopclean, "", "", fmt.Errorf("Unable to create new db at %q: %w", mainUrl, err)
	}
	return noopclean, u, "", nil
	return disconnectFromDatabase(dbName, mainDb), u, "", nil
}
