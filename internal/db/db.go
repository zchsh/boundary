package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/hashicorp/boundary/internal/db/migrations"
	"github.com/hashicorp/boundary/internal/docker"
	"github.com/hashicorp/boundary/internal/errors"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-multierror"
	"github.com/jinzhu/gorm"
	"github.com/lib/pq"
)

var (
	StartDbInDocker = docker.StartDbInDocker
)

type DbType int

const (
	UnknownDB DbType = 0
	Postgres  DbType = 1
)

func (db DbType) String() string {
	return [...]string{
		"unknown",
		"postgres",
	}[db]
}

// Open a database connection which is long-lived.
// You need to call Close() on the returned gorm.DB
func Open(dbType DbType, connectionUrl string) (*gorm.DB, error) {
	db, err := gorm.Open(dbType.String(), connectionUrl)
	if err != nil {
		return nil, fmt.Errorf("unable to open database: %w", err)
	}
	return db, nil
}

func VerifyUpToDate(ctx context.Context, db *sql.DB) error {
	r, err := db.QueryContext(ctx, "select version, dirty from boundary_schema_migrations LIMIT 1")
	switch {
	case err == nil:
		r.Next()
		var dirty bool
		var version uint
		if err := r.Scan(&version, &dirty); err != nil {
			return fmt.Errorf("Error querying database for status: %w", err)
		}
		if dirty {
			return errors.New(errors.InvalidSchema, errors.WithMsg("A previous migration or initialization of the database has failed. Please restore the database to a good state."))
		}
		source, err := migrations.NewMigrationSource("postgres")
		if err != nil {
			return fmt.Errorf("Error querying migration state for status: %w", err)
		}
		nVer, err := source.Next(version)
		if err == nil {
			return errors.New(errors.OutdatedSchema, errors.WithMsg(fmt.Sprintf("Detected version %d which could be updated to %d.", version, nVer)))
		}
	case errors.IsMissingTableError(err):
		return errors.New(errors.DbNotInitialized)
	default:
		return fmt.Errorf("Error querying database for status: %w", err)
	}
	return nil
}

func GetSharedLock(ctx context.Context, db *sql.DB) bool {
	// Ensure no other process is accessing the database.
	r := db.QueryRowContext(ctx, "SELECT pg_try_advisory_lock_shared(123)")
	if r.Err() != nil {
		return false
	}
	var gotL bool
	r.Scan(&gotL)
	return gotL
}

func GetExclusiveLock(ctx context.Context, db *sql.DB) bool {
	// Ensure no other process is accessing the database.
	r := db.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(123)")
	if r.Err() != nil {
		return false
	}
	var gotL bool
	r.Scan(&gotL)
	return gotL
}

// DevMigration is true if the database schema that would be applied by InitStore would be from
// files in the /dev directory which indicates it would not be safe to run in a non dev
// environment.
func DevMigration() bool {
	return migrations.DevMigration
}

// InitStore will execute the migrations needed to initialize the store. It
// returns true if migrations actually ran; false if we were already current.
func InitStore(dialect string, cleanup func() error, url string) (bool, error) {
	var mErr *multierror.Error
	// run migrations
	source, err := migrations.NewMigrationSource(dialect)
	if err != nil {
		mErr = multierror.Append(mErr, fmt.Errorf("error creating migration driver: %w", err))
		if cleanup != nil {
			if err := cleanup(); err != nil {
				mErr = multierror.Append(mErr, fmt.Errorf("error cleaning up from creating driver: %w", err))
			}
		}
		return false, mErr.ErrorOrNil()
	}
	m, err := migrate.NewWithSourceInstance("httpfs", source, url)
	if err != nil {
		mErr = multierror.Append(mErr, fmt.Errorf("error creating migrations: %w", err))
		if cleanup != nil {
			if err := cleanup(); err != nil {
				mErr = multierror.Append(mErr, fmt.Errorf("error cleaning up from creating migrations: %w", err))
			}
		}
		return false, mErr.ErrorOrNil()
	}
	if err := m.Up(); err != nil {
		if err == migrate.ErrNoChange {
			return false, nil
		}
		mErr = multierror.Append(mErr, fmt.Errorf("error running migrations: %w", err))
		if cleanup != nil {
			if err := cleanup(); err != nil {
				mErr = multierror.Append(mErr, fmt.Errorf("error cleaning up from running migrations: %w", err))
			}
		}
		return false, mErr.ErrorOrNil()
	}
	return true, mErr.ErrorOrNil()
}

func GetGormLogFormatter(log hclog.Logger) func(values ...interface{}) (messages []interface{}) {
	return func(values ...interface{}) (messages []interface{}) {
		if len(values) > 2 && values[0].(string) == "log" {
			switch values[2].(type) {
			case *pq.Error:
				log.Trace("error from database adapter", "location", values[1], "error", values[2])
			}
			return nil
		}
		return nil
	}
}

type gormLogger struct {
	logger hclog.Logger
}

func (g gormLogger) Print(values ...interface{}) {
	formatted := gorm.LogFormatter(values...)
	if formatted == nil {
		return
	}
	// Our formatter should elide anything we don't want so this should never
	// happen, panic if so so we catch/fix
	panic("unhandled error case")
}

func GetGormLogger(log hclog.Logger) gormLogger {
	return gormLogger{logger: log}
}
