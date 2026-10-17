package db

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestAccountMigrationUpgradeAndRollback(t *testing.T) {
	base := accountTestStore(t)
	schema := "account_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := base.pool.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := base.pool.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("search_path", schema)
	query.Set("default_query_exec_mode", "simple_protocol")
	u.RawQuery = query.Encode()
	conn, err := pgx.Connect(t.Context(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	u.Scheme = "pgx5"
	m, err := migrate.NewWithSourceInstance("iofs", source, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Migrate(40); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`INSERT INTO workflows (id, user_id, name) VALUES ('legacy', 'legacy', 'Legacy')`,
		`INSERT INTO tool_credentials (user_id, provider, encrypted_api_key) VALUES ('legacy', 'tool', 'encrypted')`,
		`INSERT INTO x402_run_fundings (run_id, inbound_tx_id, amount_asset_micros) VALUES ('legacy', 'legacy', 1)`,
	} {
		if _, err := conn.Exec(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := m.Migrate(41); err != nil {
			t.Fatalf("upgrade to 41: %v", err)
		}
		version, dirty, err := m.Version()
		if err != nil || version != 41 || dirty {
			t.Fatalf("version=%d dirty=%v err=%v", version, dirty, err)
		}
		for _, table := range []string{"workflows", "tool_credentials", "x402_run_fundings"} {
			var count int
			if err := conn.QueryRow(t.Context(), "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&count); err != nil || count != 1 {
				t.Fatalf("legacy %s lost: %d %v", table, count, err)
			}
		}
		for _, sql := range []string{
			`INSERT INTO workflows (id, user_id, name) VALUES ('new', 'missing', 'New')`,
			`INSERT INTO tool_credentials (user_id, provider, encrypted_api_key) VALUES ('missing', 'tool', 'encrypted')`,
			`INSERT INTO x402_run_fundings (run_id, inbound_tx_id, amount_asset_micros) VALUES ('missing', 'new', 1)`,
		} {
			if _, err := conn.Exec(t.Context(), sql); err == nil {
				t.Fatalf("new orphan allowed: %s", sql)
			}
		}
		if err := m.Steps(-1); err != nil {
			t.Fatalf("rollback to 40: %v", err)
		}
		for _, index := range []string{"idx_x402_run_fundings_run_id", "idx_x402_relay_settlements_run_funding_id"} {
			var exists bool
			if err := conn.QueryRow(t.Context(), `SELECT to_regclass($1) IS NOT NULL`, index).Scan(&exists); err != nil || !exists {
				t.Fatalf("base index %s removed: %v", index, err)
			}
		}
	}
}
