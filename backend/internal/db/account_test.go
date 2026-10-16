package db

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func accountTestStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	s, err := New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

var accountFixtureStatements = []string{
	`INSERT INTO users (id, email, password_hash) VALUES ($1, $1 || '@example.test', 'hash')`,
	`INSERT INTO workflows (id, user_id, name, is_system) VALUES ($1, $1, 'Owned workflow', true)`,
	`INSERT INTO runs (id, workflow_id) VALUES ($1, $1)`,
	`INSERT INTO run_logs (run_id, step_index, node_id, node_type, status) VALUES ($1, 0, 'node', 'trigger', 'success')`,
	`INSERT INTO agent_wallets (workflow_id, agent_node_id, address, encrypted_mnemonic) VALUES ($1, 'node', 'address', 'encrypted')`,
	`INSERT INTO tool_credentials (user_id, provider, encrypted_api_key) VALUES ($1, 'tool', 'encrypted')`,
	`INSERT INTO oauth_credentials (user_id, provider, access_token_enc, expires_at) VALUES ($1, 'tool', 'encrypted', now())`,
	`INSERT INTO device_tokens (user_id, token) VALUES ($1, $1)`,
	`INSERT INTO credit_ledger (user_id, provider, provider_order_id, credit_usd_micros, amount_usd_cents) VALUES ($1, 'nowpayments', $1, 10, 1)`,
	`INSERT INTO debit_ledger (user_id, workflow_id, run_id, node_id, kind, amount_usd_micros) VALUES ($1, $1, $1, 'node', 'x402_platform_fee', 1)`,
	`INSERT INTO coupon_redemptions (user_id, code, credit_usd_micros) VALUES ($1, 'TEST', 10)`,
	`INSERT INTO tendril_credit_ledger (user_id, kind, amount_usd_micros) VALUES ($1, 'topup', 10)`,
	`INSERT INTO tendril_leases (id, user_id, workflow_id, run_id, node_id, lease_id, lease_token_enc, tendril_node_id, rate_usd_micros_per_hour, hours_purchased, reserved_usd_micros, funded_until, status)
	 VALUES ($1, $1, $1, $1, 'node', $1, 'encrypted', 'machine', 1, 1, 1, now(), 'released')`,
	`INSERT INTO workflow_variables (workflow_id, key, value) VALUES ($1, 'key', '"value"')`,
	`INSERT INTO workflow_build_messages (workflow_id, role, text) VALUES ($1, 'user', 'message')`,
	`INSERT INTO workflow_chat_sessions (workflow_id, mode, session_id) VALUES ($1, 'run', $1)`,
	`INSERT INTO dead_letter_runs (run_id, node_id, error, attempt_count) VALUES ($1, 'node', 'error', 1)`,
	`INSERT INTO x402_run_fundings (id, run_id, inbound_tx_id, amount_asset_micros) VALUES ($1::uuid, $1, $1, 1)`,
	`INSERT INTO x402_relay_settlements (run_funding_id, target_url, amount_asset_micros) VALUES ($1::uuid, 'https://example.test', 1)`,
	`INSERT INTO waitlist_archived (email) VALUES (upper($1 || '@example.test'))`,
}

var accountFixtureCounts = []string{
	`SELECT count(*) FROM users WHERE id = $1`,
	`SELECT count(*) FROM workflows WHERE user_id = $1`,
	`SELECT count(*) FROM runs WHERE workflow_id = $1`,
	`SELECT count(*) FROM run_logs WHERE run_id = $1`,
	`SELECT count(*) FROM agent_wallets WHERE workflow_id = $1`,
	`SELECT count(*) FROM tool_credentials WHERE user_id = $1`,
	`SELECT count(*) FROM oauth_credentials WHERE user_id = $1`,
	`SELECT count(*) FROM device_tokens WHERE user_id = $1`,
	`SELECT count(*) FROM credit_ledger WHERE user_id = $1`,
	`SELECT count(*) FROM debit_ledger WHERE user_id = $1`,
	`SELECT count(*) FROM coupon_redemptions WHERE user_id = $1`,
	`SELECT count(*) FROM tendril_credit_ledger WHERE user_id = $1`,
	`SELECT count(*) FROM tendril_leases WHERE user_id = $1`,
	`SELECT count(*) FROM workflow_variables WHERE workflow_id = $1`,
	`SELECT count(*) FROM workflow_build_messages WHERE workflow_id = $1`,
	`SELECT count(*) FROM workflow_chat_sessions WHERE workflow_id = $1`,
	`SELECT count(*) FROM dead_letter_runs WHERE run_id = $1`,
	`SELECT count(*) FROM x402_run_fundings WHERE run_id = $1`,
	`SELECT count(*) FROM x402_relay_settlements WHERE run_funding_id = $1::uuid`,
	`SELECT count(*) FROM waitlist_archived WHERE lower(email) = $1 || '@example.test'`,
}

func accountFixture(t *testing.T, s *Store) string {
	t.Helper()
	id := uuid.NewString()
	for _, sql := range accountFixtureStatements {
		if _, err := s.pool.Exec(t.Context(), sql, id); err != nil {
			t.Fatalf("seed %s: %v", sql, err)
		}
	}
	t.Cleanup(func() {
		if _, err := s.pool.Exec(context.Background(), `DELETE FROM waitlist_archived WHERE lower(email) = $1 || '@example.test'`, id); err != nil {
			t.Error(err)
		}
		if _, err := s.pool.Exec(context.Background(), `DELETE FROM tendril_leases WHERE user_id = $1`, id); err != nil {
			t.Error(err)
		}
		if _, err := s.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id); err != nil {
			t.Error(err)
		}
	})
	return id
}

func assertAccountRows(t *testing.T, s *Store, id string, expected int) {
	t.Helper()
	for _, sql := range accountFixtureCounts {
		var count int
		if err := s.pool.QueryRow(t.Context(), sql, id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != expected {
			t.Errorf("%s: got %d, want %d", sql, count, expected)
		}
	}
}

func TestDeleteAccountCleansOwnedRecords(t *testing.T) {
	s := accountTestStore(t)
	owner, other := accountFixture(t, s), accountFixture(t, s)
	assertAccountRows(t, s, owner, 1)
	assertAccountRows(t, s, other, 1)
	publicID := uuid.NewString()
	if _, err := s.pool.Exec(t.Context(), `INSERT INTO x402_relay_settlements (id, inbound_tx_id, target_url, amount_asset_micros) VALUES ($1::uuid, $1, 'https://example.test', 1)`, publicID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(t.Context(), owner, "hash"); err != nil {
		t.Fatal(err)
	}
	assertAccountRows(t, s, owner, 0)
	assertAccountRows(t, s, other, 1)
	var publicExists bool
	if err := s.pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM x402_relay_settlements WHERE id = $1::uuid)`, publicID).Scan(&publicExists); err != nil || !publicExists {
		t.Fatalf("unowned record: %v %v", publicExists, err)
	}
	if exists, err := s.UserExists(t.Context(), owner); err != nil || exists {
		t.Fatalf("deleted account exists: %v %v", exists, err)
	}
	for _, sql := range []string{accountFixtureStatements[1], accountFixtureStatements[5], accountFixtureStatements[17]} {
		if _, err := s.pool.Exec(t.Context(), sql, owner); err == nil {
			t.Fatalf("late write recreated deleted records: %s", sql)
		}
	}
}

func TestDeleteAccountRollsBackAllCleanup(t *testing.T) {
	s := accountTestStore(t)
	owner := accountFixture(t, s)
	other := accountFixture(t, s)
	// A mismatched historical lease prevents the workflow cascade after cleanup starts.
	if _, err := s.pool.Exec(t.Context(), `UPDATE tendril_leases SET workflow_id = $1, run_id = $1 WHERE user_id = $2`, owner, other); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(t.Context(), owner, "hash"); err == nil {
		t.Fatal("expected restrictive foreign key error")
	}
	assertAccountRows(t, s, owner, 1)
	assertAccountRows(t, s, other, 1)
}

func TestDeleteAccountGuards(t *testing.T) {
	s := accountTestStore(t)
	for _, state := range []string{"active", "released", "failed"} {
		t.Run(state, func(t *testing.T) {
			owner := accountFixture(t, s)
			if _, err := s.pool.Exec(t.Context(), `UPDATE tendril_leases SET status = $2, funded_until = now() - interval '1 day' WHERE user_id = $1`, owner, state); err != nil {
				t.Fatal(err)
			}
			err := s.DeleteAccount(t.Context(), owner, "hash")
			if state == "active" {
				if !errors.Is(err, ErrActiveMachineLeases) {
					t.Fatalf("active lease: %v", err)
				}
				assertAccountRows(t, s, owner, 1)
				if _, err := s.pool.Exec(t.Context(), `DELETE FROM users WHERE id = $1`, owner); err == nil {
					t.Fatal("direct deletion destroyed an active lease")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				assertAccountRows(t, s, owner, 0)
			}
		})
	}
	owner := accountFixture(t, s)
	if err := s.DeleteAccount(t.Context(), owner, "stale-hash"); !errors.Is(err, ErrAccountChanged) {
		t.Fatalf("changed credentials: %v", err)
	}
	assertAccountRows(t, s, owner, 1)
	if err := s.DeleteAccount(t.Context(), uuid.NewString(), ""); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing account: %v", err)
	}
}

func TestDeleteAccountWaitsForConcurrentLease(t *testing.T) {
	s := accountTestStore(t)
	owner := accountFixture(t, s)
	if _, err := s.pool.Exec(t.Context(), `DELETE FROM tendril_leases WHERE user_id = $1`, owner); err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(t.Context(), accountFixtureStatements[12], owner); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `UPDATE tendril_leases SET status = 'active' WHERE user_id = $1`, owner); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if err := s.DeleteAccount(ctx, owner, "hash"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deletion did not wait for lease writer: %v", err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(t.Context(), owner, "hash"); !errors.Is(err, ErrActiveMachineLeases) {
		t.Fatalf("new active lease missed: %v", err)
	}
	assertAccountRows(t, s, owner, 1)
}
