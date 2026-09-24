// Package pgtest runs a throwaway PostgreSQL 18 container for one test binary
// and gives each test its own database on it.
//
// A package using it calls Main from TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }
//
// It needs a running Docker daemon.
package pgtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// image is the server tests run against: the same major version archie
// requires.
const image = "postgres:18"

var (
	adminURL string
	nextDB   atomic.Int64
)

// Main starts the container, runs the tests and removes the container,
// returning the exit code. A container that cannot start is a test failure,
// not a skip.
func Main(m *testing.M) int {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase("postgres"),
		tcpostgres.WithUsername("archie"),
		tcpostgres.WithPassword("archie"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() { _ = testcontainers.TerminateContainer(container) }()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgtest: start %s (is Docker running?): %v\n", image, err)
		return 1
	}
	adminURL, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgtest: connection string: %v\n", err)
		return 1
	}
	return m.Run()
}

// URL creates an empty database for t, drops it when t ends, and returns a
// connection URL for it.
func URL(t testing.TB) string {
	t.Helper()
	if adminURL == "" {
		t.Fatal("pgtest: no container running; call pgtest.Main from TestMain")
	}
	name := fmt.Sprintf("t%d", nextDB.Add(1))
	exec(t, "CREATE DATABASE "+name)
	t.Cleanup(func() { exec(t, "DROP DATABASE "+name+" WITH (FORCE)") })

	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("pgtest: parse connection string: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

func exec(t testing.TB, sql string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("pgtest: connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("pgtest: %s: %v", sql, err)
	}
}
