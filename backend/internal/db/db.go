package db

import (
	"context"
	"embed"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	pgxv5 "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
)

//go:embed migrations
var migrationsFS embed.FS

func New(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	// PgBouncer transaction mode requires simple protocol (no prepared statements).
	cfg.ConnConfig.DefaultQueryExecMode = pgxv5.QueryExecModeSimpleProtocol

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.New: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	if err := runMigrations(databaseURL); err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}
	return &Store{pool: pool}, nil
}

func runMigrations(databaseURL string) error {
	d, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	migURL := strings.Replace(databaseURL, "postgres://", "pgx5://", 1)
	migURL = strings.Replace(migURL, "postgresql://", "pgx5://", 1)
	if strings.Contains(migURL, "?") {
		migURL += "&default_query_exec_mode=simple_protocol"
	} else {
		migURL += "?default_query_exec_mode=simple_protocol"
	}
	m, err := migrate.NewWithSourceInstance("iofs", d, migURL)
	if err != nil {
		return err
	}
	// m.Close() releases the database connection this migrate instance opened
	// independently of the pgxpool above -- without it, every call to New()
	// (i.e. every test that stands up a *Store) leaks one Postgres connection
	// for the life of the process. Across a full test run that adds up fast
	// enough to exhaust max_connections and fail unrelated tests deep into
	// the run with "too many clients already".
	defer m.Close()
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return err
	}
	return nil
}
