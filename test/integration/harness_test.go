package integration_test

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var (
	testDSN        string
	testPool       *pgxpool.Pool
	echoBinaryPath string
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("orchard_test"),
		postgres.WithUsername("orchard"),
		postgres.WithPassword("orchard"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		log.Fatalf("starting postgres container: %v", err)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = testcontainers.TerminateContainer(container)
		log.Fatalf("getting dsn: %v", err)
	}

	if err := runMigrations(dsn); err != nil {
		_ = testcontainers.TerminateContainer(container)
		log.Fatalf("running migrations: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		_ = testcontainers.TerminateContainer(container)
		log.Fatalf("creating pool: %v", err)
	}

	if err := buildEchoProvider(); err != nil {
		_ = testcontainers.TerminateContainer(container)
		log.Fatalf("building echo provider: %v", err)
	}

	testDSN = dsn
	testPool = pool

	code := m.Run()

	pool.Close()
	if err := testcontainers.TerminateContainer(container); err != nil {
		log.Printf("terminating container: %v", err)
	}
	os.Exit(code)
}

func runMigrations(dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("opening db: %w", err)
	}
	defer db.Close()

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("setting dialect: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

// buildEchoProvider compiles the test-only echo external provider
// into a known location inside testdata/echo so integration scenarios
// can reference it via `exec:./testdata/echo/echo-provider`. The
// relative path is what the scenario sees when go test sets cwd to
// the package directory during the run.
func buildEchoProvider() error {
	echoBinaryPath = filepath.Join("testdata", "echo", "echo-provider")
	cmd := exec.Command("go", "build", "-o", echoBinaryPath, "./testdata/echo")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build echo: %w", err)
	}
	return nil
}

// resetDB truncates all domain tables so each test starts clean.
// Migrations themselves stay in place.
func resetDB(t *testing.T) {
	t.Helper()
	_, err := testPool.Exec(context.Background(),
		"TRUNCATE TABLE products, merchants RESTART IDENTITY CASCADE")
	if err != nil {
		t.Fatalf("truncating tables: %v", err)
	}
}
