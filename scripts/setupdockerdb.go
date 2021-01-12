package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"time"

	"github.com/hashicorp/boundary/internal/db/schema"
	"github.com/ory/dockertest/v3"
)

var dockerInfoFile string

func init() {
	flag.StringVar(&dockerInfoFile, "docker_info_file", "", "specifies the file to append the container info to.")
}

func main() {
	flag.Parse()
	if dockerInfoFile == "" {
		log.Fatalf("No docker info file defined.  Please use --docker_info_file to specify which file to use to output the docker info.")
	}

	f, err := os.OpenFile(dockerInfoFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0200)
	if err != nil {
		log.Fatalf("Failed to open the file %q for writing: %v", dockerInfoFile, err)
	}

	pool, err := dockertest.NewPool("")
	if err != nil {
		log.Fatalf("Couldn't create pool %v", err)
	}
	resource, err := pool.Run("postgres", "12", []string{"POSTGRES_PASSWORD=password"})
	if err != nil {
		log.Fatalf("Couldn't docker run %v", err)
	}

	if _, err := f.WriteString(resource.Container.Name); err != nil {
		log.Fatalf("Failed writing to %q: %v", dockerInfoFile, err)
	}

	dialect := "postgres"
	ctx := context.Background()
	u := fmt.Sprintf("postgres://postgres:password@%s?sslmode=disable", resource.GetHostPort("5432/tcp"))
	//var err error
	db, err := sql.Open(dialect, u)
	if err != nil {
		log.Fatalf("failed connecting to db %v", err)
	}
	time.Sleep(10 * time.Second)

	ctx, _ = context.WithTimeout(ctx, 2*time.Minute)
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("Unable to ping db: %v", err)
		return
	}
	if _, err := db.Exec("CREATE DATABASE boundary WITH is_template true"); err != nil {
		log.Fatalf("Unable to create the boundary db: %v", err)
		return
	}

	// Remove the 'boundary' template table from the URL so we aren't connecting to it.
	nu, err := url.Parse(u)
	if err != nil {
		log.Fatalf("Unable to parse url: %v", err)
		return
	}
	nu.Path = "boundary"

	pdb, err := sql.Open("postgres", nu.String())
	if err != nil {
		log.Fatalf("failed to connect to boundary db: %v", err)
		return
	}
	pdb.SetMaxIdleConns(0)
	time.Sleep(10 * time.Second)

	ctx, _ = context.WithTimeout(ctx, 30*time.Second)
	if err := pdb.PingContext(ctx); err != nil {
		log.Fatalf("Unable to ping db: %v", err)
		return
	}

	nsm, err := schema.NewManager(ctx, dialect, pdb)
	if err != nil {
		log.Fatalf("Failed getting schema manager: %v\n", err)
	}
	if err := nsm.RollForward(ctx); err != nil {
		log.Fatalf("Failed rolling forward: %v\n", err)
	}
	if err := pdb.Close(); err != nil {
		log.Fatalf("Unable to close the boundary db connection: %v", err)
	}

	if _, err := db.Exec("SELECT pg_terminate_backend(pg_stat_activity.pid) FROM pg_stat_activity  WHERE pg_stat_activity.datname = 'boundary' AND pid <> pg_backend_pid()"); err != nil {
		log.Fatalf("Unable to terminate backends: %v", err)
	}
	fmt.Print(u)
	// if err := os.Setenv(schema.TestingDbEnvKey, u); err != nil {
	// 	log.Fatalf("Couldn't set the env variable: %v", err)
	// }
}
