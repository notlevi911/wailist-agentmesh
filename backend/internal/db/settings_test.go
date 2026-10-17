package db

import (
	"context"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/agentmesh/backend/internal/models"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestSettingsDefaultsAtomicFieldsAndDeletion(t *testing.T) {
	s := accountTestStore(t)
	owner, other := accountFixture(t, s), accountFixture(t, s)
	ctx := t.Context()
	got, err := s.GetUserSettings(ctx, owner)
	if err != nil || got.LowBalanceUSDMicros != models.DefaultLowBalanceUSDMicros || got.MaxCallSpendUSDMicros != nil {
		t.Fatalf("defaults: %+v %v", got, err)
	}
	if _, err := s.GetUserSettings(ctx, uuid.NewString()); err == nil {
		t.Fatal("missing user got settings")
	}
	threshold, ceiling := int64(1_234_567), int64(765_432)
	var wg sync.WaitGroup
	for _, patch := range []models.UserSettingsPatch{
		{LowBalanceUSDMicros: &threshold},
		{SetMaxCallSpend: true, MaxCallSpendUSDMicros: &ceiling},
	} {
		wg.Add(1)
		go func(p models.UserSettingsPatch) {
			defer wg.Done()
			if _, err := s.UpsertUserSettings(ctx, owner, p); err != nil {
				t.Error(err)
			}
		}(patch)
	}
	wg.Wait()
	got, err = s.GetUserSettings(ctx, owner)
	if err != nil || got.LowBalanceUSDMicros != threshold || got.MaxCallSpendUSDMicros == nil || *got.MaxCallSpendUSDMicros != ceiling {
		t.Fatalf("concurrent patch lost a field: %+v %v", got, err)
	}
	got, err = s.UpsertUserSettings(ctx, owner, models.UserSettingsPatch{SetMaxCallSpend: true})
	if err != nil || got.MaxCallSpendUSDMicros != nil || got.LowBalanceUSDMicros != threshold {
		t.Fatalf("clear ceiling: %+v %v", got, err)
	}
	if _, err := s.UpsertUserSettings(ctx, other, models.UserSettingsPatch{LowBalanceUSDMicros: &threshold}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, owner, "hash"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM user_settings WHERE user_id=$1", owner).Scan(&count); err != nil || count != 0 {
		t.Fatalf("settings survived deletion: %d %v", count, err)
	}
	if got, err = s.GetUserSettings(ctx, other); err != nil || got.LowBalanceUSDMicros != threshold {
		t.Fatalf("other owner changed: %+v %v", got, err)
	}
	if _, err := s.UpsertUserSettings(ctx, owner, models.UserSettingsPatch{}); err == nil {
		t.Fatal("late settings write recreated deleted account data")
	}
}

func TestPasswordCompareAndSwap(t *testing.T) {
	s := accountTestStore(t)
	owner := accountFixture(t, s)
	ctx := t.Context()
	changed, err := s.UpdatePassword(ctx, owner, "wrong", "bad")
	if err != nil || changed {
		t.Fatalf("wrong prior hash accepted: %v %v", changed, err)
	}
	if changed, err = s.UpdatePassword(ctx, owner, "hash", "new"); err != nil || !changed {
		t.Fatalf("change: %v %v", changed, err)
	}
	if changed, err = s.UpdatePassword(ctx, owner, "hash", "stale"); err != nil || changed {
		t.Fatalf("stale reauthentication won: %v %v", changed, err)
	}
	user, err := s.GetUserByID(ctx, owner)
	if err != nil || user.PasswordHash != "new" {
		t.Fatalf("winning password lost: %+v %v", user, err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE users SET password_hash='' WHERE id=$1", owner); err != nil {
		t.Fatal(err)
	}
	if changed, err = s.UpdatePassword(ctx, owner, "", "injected"); err != nil || changed {
		t.Fatalf("OAuth password created: %v %v", changed, err)
	}
}

func TestSettingsMigrationUpgradeAndRollback(t *testing.T) {
	base := accountTestStore(t)
	schema := "settings_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	if err = m.Migrate(41); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(t.Context(), `INSERT INTO users (id,email,password_hash) VALUES ('settings-owner','settings@example.test','hash')`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = m.Migrate(42); err != nil {
			t.Fatal(err)
		}
		if _, err = conn.Exec(t.Context(), `INSERT INTO user_settings (user_id) VALUES ('settings-owner')`); err != nil {
			t.Fatal(err)
		}
		var threshold int64
		var ceiling *int64
		if err = conn.QueryRow(t.Context(), `SELECT low_balance_usd_micros,max_call_spend_usd_micros FROM user_settings`).Scan(&threshold, &ceiling); err != nil || threshold != models.DefaultLowBalanceUSDMicros || ceiling != nil {
			t.Fatalf("defaults %d %v %v", threshold, ceiling, err)
		}
		for _, sql := range []string{
			`UPDATE user_settings SET low_balance_usd_micros=-1`,
			`UPDATE user_settings SET low_balance_usd_micros=9007199254740992`,
			`UPDATE user_settings SET max_call_spend_usd_micros=49999`,
			`UPDATE user_settings SET max_call_spend_usd_micros=1000000001`,
			`INSERT INTO user_settings (user_id) VALUES ('missing')`,
		} {
			if _, err = conn.Exec(t.Context(), sql); err == nil {
				t.Fatalf("constraint allowed %s", sql)
			}
		}
		if err = m.Steps(-1); err != nil {
			t.Fatal(err)
		}
		version, dirty, err := m.Version()
		if err != nil || version != 41 || dirty {
			t.Fatalf("rollback %d %v %v", version, dirty, err)
		}
		var count int
		if err = conn.QueryRow(t.Context(), `SELECT count(*) FROM users WHERE id='settings-owner'`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("rollback removed owner %d %v", count, err)
		}
	}
}
