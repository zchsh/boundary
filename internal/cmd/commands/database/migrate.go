package database

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	"github.com/hashicorp/boundary/internal/cmd/base"
	"github.com/hashicorp/boundary/internal/cmd/config"
	"github.com/hashicorp/boundary/internal/db"
	"github.com/hashicorp/boundary/internal/db/migrations"
	"github.com/hashicorp/boundary/internal/errors"
	"github.com/hashicorp/boundary/sdk/wrapper"
	wrapping "github.com/hashicorp/go-kms-wrapping"
	"github.com/hashicorp/vault/sdk/helper/mlock"
	"github.com/mitchellh/cli"
	"github.com/posener/complete"
)

var _ cli.Command = (*MigrateCommand)(nil)
var _ cli.CommandAutocomplete = (*MigrateCommand)(nil)

type MigrateCommand struct {
	*base.Command
	srv *base.Server

	SighupCh   chan struct{}
	ReloadedCh chan struct{}
	SigUSR2Ch  chan struct{}

	Config *config.Config

	configWrapper wrapping.Wrapper

	flagConfig                       string
	flagConfigKms                    string
	flagLogLevel                     string
	flagLogFormat                    string
	flagMigrationUrl                 string
	flagAllowDevMigration            bool
}

func (c *MigrateCommand) Synopsis() string {
	return "Update Boundary's database"
}

func (c *MigrateCommand) Help() string {
	return base.WrapForHelpText([]string{
		"Usage: boundary database migrate [options]",
		"",
		"  Upgrade Boundary's database:",
		"",
		"    $ boundary database migrate -config=/etc/boundary/controller.hcl",
	}) + c.Flags().Help()
}

func (c *MigrateCommand) Flags() *base.FlagSets {
	set := c.FlagSet(base.FlagSetHTTP)

	f := set.NewFlagSet("Command Options")

	f.StringVar(&base.StringVar{
		Name:   "config",
		Target: &c.flagConfig,
		Completion: complete.PredictOr(
			complete.PredictFiles("*.hcl"),
			complete.PredictFiles("*.json"),
		),
		Usage: "Path to the configuration file.",
	})

	f.StringVar(&base.StringVar{
		Name:   "config-kms",
		Target: &c.flagConfigKms,
		Completion: complete.PredictOr(
			complete.PredictFiles("*.hcl"),
			complete.PredictFiles("*.json"),
		),
		Usage: `Path to a configuration file containing a "kms" block marked for "config" purpose, to perform decryption of the main configuration file. If not set, will look for such a block in the main configuration file, which has some drawbacks; see the help output for "boundary config encrypt -h" for details.`,
	})

	f.StringVar(&base.StringVar{
		Name:       "log-level",
		Target:     &c.flagLogLevel,
		EnvVar:     "BOUNDARY_LOG_LEVEL",
		Completion: complete.PredictSet("trace", "debug", "info", "warn", "err"),
		Usage: "Log verbosity level. Supported values (in order of more detail to less) are " +
			"\"trace\", \"debug\", \"info\", \"warn\", and \"err\".",
	})

	f.StringVar(&base.StringVar{
		Name:       "log-format",
		Target:     &c.flagLogFormat,
		Completion: complete.PredictSet("standard", "json"),
		Usage:      `Log format. Supported values are "standard" and "json".`,
	})

	f = set.NewFlagSet("Migrate Options")

	f.BoolVar(&base.BoolVar{
		Name:   "allow-development-migration",
		Target: &c.flagAllowDevMigration,
		Usage:  "If set the migrate will continue even if the schema includes unsafe database update steps that have not been finalized.",
	})

	f.StringVar(&base.StringVar{
		Name:   "migration-url",
		Target: &c.flagMigrationUrl,
		Usage:  `If set, overrides a migration URL set in config, and specifies the URL used to connect to the database for migration. This can allow different permissions for the user running migration vs. normal operation. This can refer to a file on disk (file://) from which a URL will be read; an env var (env://) from which the URL will be read; or a direct database URL.`,
	})

	return set
}

func (c *MigrateCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictNothing
}

func (c *MigrateCommand) AutocompleteFlags() complete.Flags {
	return c.Flags().Completions()
}

func (c *MigrateCommand) Run(args []string) (retCode int) {
	if result := c.ParseFlagsAndConfig(args); result > 0 {
		return result
	}

	if c.configWrapper != nil {
		defer func() {
			if err := c.configWrapper.Finalize(c.Context); err != nil {
				c.UI.Warn(fmt.Errorf("Error finalizing config kms: %w", err).Error())
			}
		}()
	}

	if migrations.DevMigration != c.flagAllowDevMigration {
		if migrations.DevMigration {
			c.UI.Error("This version of the binary has unsafe dev database schema updates.  To proceed anyways please use the 'allow-development-migration' flag.")
		} else {
			c.UI.Error("The 'allow-development-migration' flag was set but this binary has no dev database schema updates.")
		}
		return 1
	}

	c.srv = base.NewServer(&base.Command{UI: c.UI})

	if err := c.srv.SetupLogging(c.flagLogLevel, c.flagLogFormat, c.Config.LogLevel, c.Config.LogFormat); err != nil {
		c.UI.Error(err.Error())
		return 1
	}

	if err := c.srv.SetupKMSes(c.UI, c.Config); err != nil {
		c.UI.Error(err.Error())
		return 1
	}

	if c.srv.RootKms == nil {
		c.UI.Error("Root KMS not found after parsing KMS blocks")
		return 1
	}

	// If mlockall(2) isn't supported, show a warning. We disable this in dev
	// because it is quite scary to see when first using Boundary. We also disable
	// this if the user has explicitly disabled mlock in configuration.
	if !c.Config.DisableMlock && !mlock.Supported() {
		c.UI.Warn(base.WrapAtLength(
			"WARNING! mlock is not supported on this system! An mlockall(2)-like " +
				"syscall to prevent memory from being swapped to disk is not " +
				"supported on this system. For better security, only run Boundary on " +
				"systems where this call is supported. If you are running Boundary" +
				"in a Docker container, provide the IPC_LOCK cap to the container."))
	}

	if c.Config.Controller.Database == nil {
		c.UI.Error(`"controller.database" config block not found`)
		return 1
	}

	urlToParse := c.Config.Controller.Database.Url
	if urlToParse == "" {
		c.UI.Error(`"url" not specified in "database" config block"`)
		return 1
	}

	var migrationUrlToParse string
	if c.Config.Controller.Database.MigrationUrl != "" {
		migrationUrlToParse = c.Config.Controller.Database.MigrationUrl
	}
	if c.flagMigrationUrl != "" {
		migrationUrlToParse = c.flagMigrationUrl
	}
	// Fallback to using database URL for everything
	if migrationUrlToParse == "" {
		migrationUrlToParse = urlToParse
	}

	dbaseUrl, err := config.ParseAddress(urlToParse)
	if err != nil && err != config.ErrNotAUrl {
		c.UI.Error(fmt.Errorf("Error parsing database url: %w", err).Error())
		return 1
	}

	migrationUrl, err := config.ParseAddress(migrationUrlToParse)
	if err != nil && err != config.ErrNotAUrl {
		c.UI.Error(fmt.Errorf("Error parsing migration url: %w", err).Error())
		return 1
	}

	// Core migrations using the migration URL
	{
		c.srv.DatabaseUrl = strings.TrimSpace(migrationUrl)
		ldb, err := sql.Open("postgres", c.srv.DatabaseUrl)
		if err != nil {
			c.UI.Error(fmt.Errorf("Error opening database to check init status: %w", err).Error())
			return 1
		}

		err = db.VerifyUpToDate(c.Context, ldb)
		switch {
		case err == nil:
			c.UI.Error("Database is already up to date.")
			return 0
		case errors.IsOutdatedSchemaError(err):
			// The database is outdated, we can continue.
		case errors.IsNotInitializedError(err):
			// Doesn't exist so we continue on
			if base.Format(c.UI) == "table" {
				c.UI.Info("Database not initialized. run boundary database init instead.")
				return 1
			}
		default:
			c.UI.Error(err.Error())
			return 1
		}

		if !db.GetExclusiveLock(c.Context, ldb) {
			c.UI.Error("Cannot run migration. Another service is currently accessing the database.")
			return 1
		}

		initDatabaseUrl, err := url.ParseRequestURI(c.srv.DatabaseUrl)
		if err != nil {
			c.UI.Error(fmt.Errorf("Error parsing database url %q status: %w", c.srv.DatabaseUrl, err).Error())
			return 1
		}
		queryValues := initDatabaseUrl.Query()
		queryValues.Add("x-migrations-table", "boundary_schema_migrations")
		initDatabaseUrl.RawQuery = queryValues.Encode()
		ran, err := db.InitStore("postgres", nil, initDatabaseUrl.String())
		if err != nil {
			c.UI.Error(fmt.Errorf("Error running database migrations: %w", err).Error())
			return 1
		}
		if !ran {
			if base.Format(c.UI) == "table" {
				c.UI.Info("Database already up to date. No changes applied.")
				return 0
			}
		}
		if base.Format(c.UI) == "table" {
			c.UI.Info("Migrations successfully run.")
		}
	}

	// Everything after is done with normal database URL and is affecting actual data
	c.srv.DatabaseUrl = strings.TrimSpace(dbaseUrl)
	if err := c.srv.ConnectToDatabase("postgres"); err != nil {
		c.UI.Error(fmt.Errorf("Error connecting to database after migrations: %w", err).Error())
		return 1
	}

	var jsonMap map[string]interface{}
	if base.Format(c.UI) == "json" {
		jsonMap = make(map[string]interface{})
		defer func() {
			b, err := base.JsonFormatter{}.Format(jsonMap)
			if err != nil {
				c.UI.Error(fmt.Errorf("Error formatting as JSON: %w", err).Error())
				retCode = 1
				return
			}
			c.UI.Output(string(b))
		}()
	}

	return 0
}

func (c *MigrateCommand) ParseFlagsAndConfig(args []string) int {
	var err error

	f := c.Flags()

	if err = f.Parse(args); err != nil {
		c.UI.Error(err.Error())
		return 1
	}


	// Validation
	switch {
	case len(c.flagConfig) == 0:
		c.UI.Error("Must specify a config file using -config")
		return 1
	}

	wrapperPath := c.flagConfig
	if c.flagConfigKms != "" {
		wrapperPath = c.flagConfigKms
	}
	wrapper, err := wrapper.GetWrapperFromPath(wrapperPath, "config")
	if err != nil {
		c.UI.Error(err.Error())
		return 1
	}
	if wrapper != nil {
		c.configWrapper = wrapper
		if err := wrapper.Init(c.Context); err != nil {
			c.UI.Error(fmt.Errorf("Could not initialize kms: %w", err).Error())
			return 1
		}
	}

	c.Config, err = config.LoadFile(c.flagConfig, wrapper)
	if err != nil {
		c.UI.Error("Eor parsing config `" + c.flagConfig + "' : " + err.Error())
		return 1
	}

	return 0
}
