package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/models"
)

func TestRunAndLogs(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	wf, _ := store.CreateWorkflow(ctx, "RunTest", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wf.ID) })

	run, err := store.CreateRun(ctx, wf.ID, "manual", []byte(`{"message":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != models.RunStatusRunning {
		t.Fatal("expected running")
	}

	logEntry, err := store.InsertRunLog(ctx, models.RunLog{
		RunID: run.ID, StepIndex: 0,
		NodeID: "n1", NodeType: models.NodeTypeTrigger,
		Status: models.LogStatusRunning,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := store.UpdateRunLog(ctx, logEntry.ID, models.LogStatusSuccess, []byte(`"done"`), 42, ""); err != nil {
		t.Fatal(err)
	}

	logs, err := store.GetRunLogs(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Status != models.LogStatusSuccess {
		t.Fatal("log not updated correctly")
	}
	if logs[0].DurationMs != 42 {
		t.Fatalf("want 42ms got %d", logs[0].DurationMs)
	}

	if err := store.FinishRun(ctx, run.ID, models.RunStatusSuccess); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetRun(ctx, run.ID)
	if got.Status != models.RunStatusSuccess {
		t.Fatal("run not finished")
	}
}

func TestCreateRunWithCooldownAllowsFirstRun(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	wf, _ := store.CreateWorkflow(ctx, "CooldownTest", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wf.ID) })

	run, err := store.CreateRunWithCooldown(ctx, wf.ID, "manual", []byte("null"), 5*time.Second)
	if err != nil {
		t.Fatalf("want first run allowed, got %v", err)
	}
	if run.ID == "" {
		t.Fatal("want a real run id")
	}
}

func TestCreateRunWithCooldownBlocksImmediateRetrigger(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	wf, _ := store.CreateWorkflow(ctx, "CooldownTest", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wf.ID) })

	if _, err := store.CreateRunWithCooldown(ctx, wf.ID, "manual", []byte("null"), 5*time.Second); err != nil {
		t.Fatalf("want first run allowed, got %v", err)
	}

	_, err := store.CreateRunWithCooldown(ctx, wf.ID, "manual", []byte("null"), 5*time.Second)
	var cooldownErr *db.ErrRunOnCooldown
	if !errors.As(err, &cooldownErr) {
		t.Fatalf("want *db.ErrRunOnCooldown, got %v", err)
	}
	if cooldownErr.RetryAfter <= 0 || cooldownErr.RetryAfter > 5*time.Second {
		t.Fatalf("want a positive retryAfter <= 5s, got %v", cooldownErr.RetryAfter)
	}
}

func TestCreateRunWithCooldownAllowsAfterElapsed(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	wf, _ := store.CreateWorkflow(ctx, "CooldownTest", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wf.ID) })

	const cooldown = 200 * time.Millisecond
	if _, err := store.CreateRunWithCooldown(ctx, wf.ID, "manual", []byte("null"), cooldown); err != nil {
		t.Fatalf("want first run allowed, got %v", err)
	}
	time.Sleep(cooldown + 50*time.Millisecond)
	if _, err := store.CreateRunWithCooldown(ctx, wf.ID, "manual", []byte("null"), cooldown); err != nil {
		t.Fatalf("want run allowed once the cooldown has elapsed, got %v", err)
	}
}

func TestCreateRunWithCooldownIsPerWorkflow(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	wfA, _ := store.CreateWorkflow(ctx, "CooldownTestA", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wfA.ID) })
	wfB, _ := store.CreateWorkflow(ctx, "CooldownTestB", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wfB.ID) })

	if _, err := store.CreateRunWithCooldown(ctx, wfA.ID, "manual", []byte("null"), 5*time.Second); err != nil {
		t.Fatalf("want wfA's first run allowed, got %v", err)
	}
	// wfB's own first run must not be blocked by wfA's cooldown.
	if _, err := store.CreateRunWithCooldown(ctx, wfB.ID, "manual", []byte("null"), 5*time.Second); err != nil {
		t.Fatalf("want wfB's first run allowed regardless of wfA's cooldown, got %v", err)
	}
}

// TestCreateRunWithCooldownDoesNotCollideWithOAuthCredentialLock guards
// against the two advisory locks in this package sharing a key space.
// CreateRunWithCooldown uses the two-key pg_advisory_xact_lock form
// specifically so it can never contend with LockOAuthCredentialForRefresh's
// single-key form, even when both are given the literal same identifier
// string. If that ever regressed back to a single, string-prefixed
// hashtext key, a workflow ID and an OAuth credential ID that happen to
// share a value (or just hash the same) would serialize against each
// other's unrelated locks.
func TestCreateRunWithCooldownDoesNotCollideWithOAuthCredentialLock(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	wf, _ := store.CreateWorkflow(ctx, "CooldownVsOAuthLockTest", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wf.ID) })

	// Hold the OAuth credential lock under the exact same identifier as the
	// workflow whose cooldown lock we're about to take.
	release, err := store.LockOAuthCredentialForRefresh(ctx, wf.ID)
	if err != nil {
		t.Fatalf("lock oauth credential: %v", err)
	}
	defer release(ctx)

	done := make(chan error, 1)
	go func() {
		_, err := store.CreateRunWithCooldown(ctx, wf.ID, "manual", []byte("null"), 5*time.Second)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("want the run-cooldown lock unaffected by an unrelated OAuth credential lock on the same ID, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("want CreateRunWithCooldown's try-lock to proceed without waiting on an unrelated OAuth credential lock")
	}
}

// TestCreateRunWithCooldownConcurrentBurstAllowsExactlyOne guards the
// atomicity this exists for: a burst of concurrent triggers for the same
// workflow (a bot hammering the endpoint) must only ever let one through,
// via pg_advisory_xact_lock serializing the check-then-insert per
// workflow, not a whole racing batch that each read "no recent run"
// before any of them committed their own.
func TestCreateRunWithCooldownConcurrentBurstAllowsExactlyOne(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	wf, _ := store.CreateWorkflow(ctx, "CooldownBurstTest", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wf.ID) })

	const burst = 10
	results := make(chan error, burst)
	for i := 0; i < burst; i++ {
		go func() {
			_, err := store.CreateRunWithCooldown(ctx, wf.ID, "manual", []byte("null"), 5*time.Second)
			results <- err
		}()
	}

	allowed := 0
	for i := 0; i < burst; i++ {
		err := <-results
		if err == nil {
			allowed++
			continue
		}
		var cooldownErr *db.ErrRunOnCooldown
		if !errors.As(err, &cooldownErr) {
			t.Fatalf("want either success or *db.ErrRunOnCooldown, got %v", err)
		}
	}
	if allowed != 1 {
		t.Fatalf("want exactly 1 of %d concurrent triggers allowed, got %d", burst, allowed)
	}
}

func TestAgentWallet(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	wf, _ := store.CreateWorkflow(ctx, "WalletTest", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wf.ID) })

	err := store.InsertAgentWallet(ctx, models.AgentWallet{
		WorkflowID:        wf.ID,
		AgentNodeID:       "agent1",
		Address:           "ALGO123456",
		EncryptedMnemonic: "enc-mnemonic",
		Network:           "testnet",
	})
	if err != nil {
		t.Fatal(err)
	}

	w, err := store.GetAgentWallet(ctx, wf.ID, "agent1")
	if err != nil {
		t.Fatal(err)
	}
	if w.Address != "ALGO123456" {
		t.Fatalf("want ALGO123456 got %s", w.Address)
	}
	if w.EncryptedMnemonic != "enc-mnemonic" {
		t.Fatal("mnemonic not persisted")
	}

	wallets, err := store.ListAgentWallets(ctx, wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(wallets) != 1 {
		t.Fatalf("want 1 wallet got %d", len(wallets))
	}
}
