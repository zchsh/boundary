package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"time"

	"github.com/hashicorp/boundary/internal/db/schema"
	"github.com/ory/dockertest/v3"
)

func main() {
	pool, err := dockertest.NewPool("")
	if err != nil {
		log.Fatal("Couldn't create pool %v", err)
	}
	resource, err := pool.Run("postgres", "12", []string{"POSTGRES_PASSWORD=password"})
	if err != nil {
		log.Fatal("Couldn't docker run %v", err)
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
		log.Fatalf("Unable to ping db: %w", err)
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
	fmt.Printf("%s=%s", schema.TestingDbEnvKey, u)
	// if err := os.Setenv(schema.TestingDbEnvKey, u); err != nil {
	// 	log.Fatalf("Couldn't set the env variable: %v", err)
	// }
}
