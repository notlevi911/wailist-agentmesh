package db_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/agentmesh/backend/internal/db"
)

func TestWorkflowVariablesRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	wf, _ := store.CreateWorkflow(ctx, "Vars", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wf.ID) })

	if err := store.SetWorkflowVariable(ctx, wf.ID, "lastRowId", []byte(`"row-42"`)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkflowVariable(ctx, wf.ID, "cursor", []byte(`{"page":3}`)); err != nil {
		t.Fatal(err)
	}

	vars, err := store.GetWorkflowVariables(ctx, wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if vars["lastRowId"] != "row-42" {
		t.Fatalf("lastRowId: got %#v", vars["lastRowId"])
	}
	cursor, ok := vars["cursor"].(map[string]any)
	if !ok || cursor["page"] != float64(3) {
		t.Fatalf("cursor: got %#v", vars["cursor"])
	}

	// Last write wins.
	if err := store.SetWorkflowVariable(ctx, wf.ID, "lastRowId", []byte(`"row-99"`)); err != nil {
		t.Fatal(err)
	}
	vars, _ = store.GetWorkflowVariables(ctx, wf.ID)
	if vars["lastRowId"] != "row-99" {
		t.Fatalf("upsert did not overwrite: %#v", vars["lastRowId"])
	}

	if err := store.DeleteWorkflowVariable(ctx, wf.ID, "cursor"); err != nil {
		t.Fatal(err)
	}
	vars, _ = store.GetWorkflowVariables(ctx, wf.ID)
	if _, still := vars["cursor"]; still {
		t.Fatal("cursor should be gone")
	}
}

// The reason the increment helper exists: two overlapping runs both
// bumping a counter must not lose an update the way read-modify-write
// would.
func TestIncrementWorkflowVariableIsAtomic(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	wf, _ := store.CreateWorkflow(ctx, "Counter", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wf.ID) })

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.IncrementWorkflowVariable(ctx, wf.ID, "hits", 1); err != nil {
				t.Errorf("increment: %v", err)
			}
		}()
	}
	wg.Wait()

	vars, err := store.GetWorkflowVariables(ctx, wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if vars["hits"] != float64(n) {
		t.Fatalf("lost updates: want %d got %#v", n, vars["hits"])
	}
}

// A non-numeric existing value is replaced by the delta rather than
// failing the run — the counter use case wants to keep counting.
func TestIncrementWorkflowVariableOverwritesNonNumeric(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	wf, _ := store.CreateWorkflow(ctx, "CounterReset", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wf.ID) })

	if err := store.SetWorkflowVariable(ctx, wf.ID, "hits", []byte(`"not a number"`)); err != nil {
		t.Fatal(err)
	}
	got, err := store.IncrementWorkflowVariable(ctx, wf.ID, "hits", 5)
	if err != nil {
		t.Fatal(err)
	}
	if got != 5 {
		t.Fatalf("want the delta to replace a non-numeric value, got %v", got)
	}
}

func TestVariableQuotas(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	wf, _ := store.CreateWorkflow(ctx, "Quota", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wf.ID) })

	big := []byte(`"` + strings.Repeat("x", 20000) + `"`)
	if err := store.SetWorkflowVariable(ctx, wf.ID, "big", big); !errors.Is(err, db.ErrVariableTooLarge) {
		t.Fatalf("want ErrVariableTooLarge got %v", err)
	}

	for i := 0; i < db.MaxWorkflowVariables; i++ {
		if err := store.SetWorkflowVariable(ctx, wf.ID, fmt.Sprintf("k%d", i), []byte(`1`)); err != nil {
			t.Fatalf("key %d: %v", i, err)
		}
	}
	err := store.SetWorkflowVariable(ctx, wf.ID, "one-too-many", []byte(`1`))
	if !errors.Is(err, db.ErrVariableQuotaExceeded) {
		t.Fatalf("want ErrVariableQuotaExceeded got %v", err)
	}
	// Updating an EXISTING key must still work at the cap.
	if err := store.SetWorkflowVariable(ctx, wf.ID, "k0", []byte(`2`)); err != nil {
		t.Fatalf("updating an existing key at the cap must be allowed: %v", err)
	}
}

// Variables belong to their workflow and die with it.
func TestWorkflowVariablesAreScopedAndCascade(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	wfA, _ := store.CreateWorkflow(ctx, "ScopeA", "dev")
	wfB, _ := store.CreateWorkflow(ctx, "ScopeB", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wfB.ID) })

	if err := store.SetWorkflowVariable(ctx, wfA.ID, "shared", []byte(`"a"`)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkflowVariable(ctx, wfB.ID, "shared", []byte(`"b"`)); err != nil {
		t.Fatal(err)
	}

	varsB, _ := store.GetWorkflowVariables(ctx, wfB.ID)
	if varsB["shared"] != "b" {
		t.Fatalf("workflows must not see each other's variables: %#v", varsB)
	}

	if err := store.DeleteWorkflow(ctx, wfA.ID); err != nil {
		t.Fatal(err)
	}
	varsA, err := store.GetWorkflowVariables(ctx, wfA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(varsA) != 0 {
		t.Fatalf("deleting a workflow must cascade to its variables, got %#v", varsA)
	}
}

// An empty workflow reads as an empty (non-nil) map so callers can index
// it without a nil check.
func TestGetWorkflowVariablesEmptyIsNonNil(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	wf, _ := store.CreateWorkflow(ctx, "EmptyVars", "dev")
	t.Cleanup(func() { store.DeleteWorkflow(ctx, wf.ID) })

	vars, err := store.GetWorkflowVariables(ctx, wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if vars == nil {
		t.Fatal("want an empty map, got nil")
	}
	if len(vars) != 0 {
		t.Fatalf("want no variables, got %#v", vars)
	}
}
