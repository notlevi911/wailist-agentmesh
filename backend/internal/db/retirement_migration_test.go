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

func TestRetirementMigrationPreservesRowsAndRollback(t *testing.T) {
	base := accountTestStore(t)
	schema := "retirement_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	databaseURL := u.String()
	conn, err := pgx.Connect(t.Context(), databaseURL)
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
	if err := m.Migrate(42); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO waitlist (id,email,created_at) VALUES
		 ('retained-first','First@example.test','2024-02-29T12:34:56.123456Z'),
		 ('retained-second','second@example.test','2025-11-02T01:30:00.654321-04:00')`,
		`INSERT INTO users (id,email,password_hash) VALUES ('settings-owner','settings@example.test','hash')`,
		`INSERT INTO user_settings (user_id,low_balance_usd_micros,max_call_spend_usd_micros,updated_at)
		 VALUES ('settings-owner',1234567,250000,'2026-01-01T00:00:01.234567Z')`,
	} {
		if _, err := conn.Exec(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func(table, order string) string {
		t.Helper()
		var value string
		statement := "SELECT jsonb_agg(to_jsonb(record) ORDER BY " + pgx.Identifier{order}.Sanitize() + ")::text FROM " + pgx.Identifier{table}.Sanitize() + " AS record"
		if err := conn.QueryRow(t.Context(), statement).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	wantRows := snapshot("waitlist", "id")
	wantSettings := snapshot("user_settings", "user_id")
	var originalOID int64
	if err := conn.QueryRow(t.Context(), `SELECT 'waitlist'::regclass::oid::bigint`).Scan(&originalOID); err != nil {
		t.Fatal(err)
	}
	assertState := func(version uint, table, absent string) {
		t.Helper()
		got, dirty, err := m.Version()
		if err != nil || got != version || dirty {
			t.Fatalf("version=%d dirty=%v err=%v, want %d clean", got, dirty, err, version)
		}
		var oid int64
		var absentMissing bool
		if err := conn.QueryRow(t.Context(), `SELECT to_regclass($1)::oid::bigint, to_regclass($2) IS NULL`, table, absent).Scan(&oid, &absentMissing); err != nil || oid != originalOID || !absentMissing {
			t.Fatalf("table=%s oid=%d absent=%v err=%v", table, oid, absentMissing, err)
		}
		if got := snapshot(table, "id"); got != wantRows {
			t.Fatalf("row IDs, emails or timestamps changed: got %s, want %s", got, wantRows)
		}
		if got := snapshot("user_settings", "user_id"); got != wantSettings {
			t.Fatalf("Settings rows changed: got %s, want %s", got, wantSettings)
		}
		if _, err := conn.Exec(t.Context(), "INSERT INTO "+pgx.Identifier{table}.Sanitize()+" (email) VALUES ('First@example.test')"); err == nil {
			t.Fatal("unique email constraint was lost")
		}
	}
	for range 2 {
		if err := m.Migrate(43); err != nil {
			t.Fatal(err)
		}
		assertState(43, "waitlist_archived", "waitlist")
		if err := m.Steps(-1); err != nil {
			t.Fatal(err)
		}
		assertState(42, "waitlist", "waitlist_archived")
	}
	if err := runMigrations(databaseURL); err != nil {
		t.Fatal(err)
	}
	assertState(43, "waitlist_archived", "waitlist")
	if err := runMigrations(databaseURL); err != nil {
		t.Fatal(err)
	}
	assertState(43, "waitlist_archived", "waitlist")
}
