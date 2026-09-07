package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// The geofence trigger's whole correctness story lives in RecordGeofenceFix:
// a background-location client pushes a fix every 30-60s and replays whatever
// it queued while offline, so "a fix arrived" and "something happened" are very
// different statements. These tests pin the difference.

// geofencedWorkflow creates a deployed, geofenced workflow and returns its id.
func geofencedWorkflow(t *testing.T, name string) (string, func()) {
	t.Helper()
	store := testStore(t)
	ctx := context.Background()

	email := fmt.Sprintf("geofence-test-%s-%d@example.com", name, time.Now().UnixNano())
	user, err := store.CreateUser(ctx, email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	wf, err := store.CreateWorkflow(ctx, "Geofence "+name, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkflowDeployed(ctx, wf.ID, "https://example.com/run", time.Now()); err != nil {
		t.Fatal(err)
	}
	// 100 m around Big Ben.
	if err := store.SetWorkflowGeofence(ctx, wf.ID, 51.5007, -0.1246, 100); err != nil {
		t.Fatal(err)
	}
	return wf.ID, func() { store.DeleteWorkflow(context.Background(), wf.ID) }
}

// The first fix must NOT fire. An unknown position is not an outside one, so
// treating it as such would report an entry the user never made -- the phone
// was simply switched on inside the zone.
func TestFirstFixEstablishesBaselineWithoutFiring(t *testing.T) {
	store := testStore(t)
	id, cleanup := geofencedWorkflow(t, "baseline")
	t.Cleanup(cleanup)

	got, err := store.RecordGeofenceFix(context.Background(), id, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Fired {
		t.Fatalf("first fix must not fire a crossing, got %+v", got)
	}
}

// The common case by a wide margin: parked inside the zone, pinging every
// minute. None of those is an event.
func TestRepeatedFixesAtTheSameStateDoNotFire(t *testing.T) {
	store := testStore(t)
	id, cleanup := geofencedWorkflow(t, "steady")
	t.Cleanup(cleanup)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour)

	if _, err := store.RecordGeofenceFix(ctx, id, true, base); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		got, err := store.RecordGeofenceFix(ctx, id, true, base.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if got.Fired {
			t.Fatalf("fix %d at an unchanged state fired a crossing: %+v", i, got)
		}
	}
}

// Both directions are events, and the direction has to be reported correctly:
// "I left home" and "I arrived home" are different workflows to a user.
func TestCrossingFiresInBothDirections(t *testing.T) {
	store := testStore(t)
	id, cleanup := geofencedWorkflow(t, "crossing")
	t.Cleanup(cleanup)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour)

	// Baseline: outside.
	if _, err := store.RecordGeofenceFix(ctx, id, false, base); err != nil {
		t.Fatal(err)
	}

	enter, err := store.RecordGeofenceFix(ctx, id, true, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !enter.Fired || !enter.Entered {
		t.Fatalf("outside -> inside must fire an entry, got %+v", enter)
	}

	leave, err := store.RecordGeofenceFix(ctx, id, false, base.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !leave.Fired || leave.Entered {
		t.Fatalf("inside -> outside must fire a departure, got %+v", leave)
	}
}

// The offline-queue case, and the reason geofence_last_fix_at exists. A client
// that reconnects and flushes a backlog must not re-fire crossings that were
// already handled, however many times it replays them.
func TestReplayedBurstDoesNotRefire(t *testing.T) {
	store := testStore(t)
	id, cleanup := geofencedWorkflow(t, "replay")
	t.Cleanup(cleanup)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour)

	// Live sequence: baseline outside, then one real entry.
	if _, err := store.RecordGeofenceFix(ctx, id, false, base); err != nil {
		t.Fatal(err)
	}
	first, err := store.RecordGeofenceFix(ctx, id, true, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !first.Fired {
		t.Fatal("the live entry should have fired")
	}

	// Now replay the whole thing, out of order, as a reconnecting client would.
	for i, f := range []struct {
		inside bool
		at     time.Time
	}{
		{true, base.Add(time.Minute)}, // the same entry again
		{false, base},                 // the older baseline
		{true, base.Add(30 * time.Second)},
		{false, base.Add(10 * time.Second)},
	} {
		got, err := store.RecordGeofenceFix(ctx, id, f.inside, f.at)
		if err != nil {
			t.Fatal(err)
		}
		if got.Fired {
			t.Fatalf("replayed fix %d re-fired a crossing: %+v", i, got)
		}
		if !got.Stale {
			t.Fatalf("replayed fix %d should be reported stale, got %+v", i, got)
		}
	}

	// And the state must not have been dragged backwards by the replay: a
	// genuine new departure after the burst still fires.
	after, err := store.RecordGeofenceFix(ctx, id, false, base.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !after.Fired || after.Entered {
		t.Fatalf("a real departure after a replayed burst must still fire, got %+v", after)
	}
}

// Moving the fence must reset the recorded state: an inside/outside answer
// that was true of the OLD centre says nothing about the new one, and
// inheriting it would fire a phantom crossing on the next fix.
func TestMovingTheFenceResetsRecordedState(t *testing.T) {
	store := testStore(t)
	id, cleanup := geofencedWorkflow(t, "moved")
	t.Cleanup(cleanup)
	ctx := context.Background()

	if _, err := store.RecordGeofenceFix(ctx, id, true, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Move the fence to Paris.
	if err := store.SetWorkflowGeofence(ctx, id, 48.8566, 2.3522, 200); err != nil {
		t.Fatal(err)
	}
	got, err := store.RecordGeofenceFix(ctx, id, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Fired {
		t.Fatalf("the first fix after moving a fence must re-baseline, not fire: %+v", got)
	}
}

// Clearing the geofence must make the workflow un-pingable rather than
// silently accepting fixes against a fence that no longer exists.
func TestClearedGeofenceRejectsFixes(t *testing.T) {
	store := testStore(t)
	id, cleanup := geofencedWorkflow(t, "cleared")
	t.Cleanup(cleanup)
	ctx := context.Background()

	if err := store.ClearWorkflowGeofence(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordGeofenceFix(ctx, id, true, time.Now()); err == nil {
		t.Fatal("recording a fix against a cleared geofence should error, not succeed")
	}
}

// The precise case CI caught, and the one an offline flush actually produces:
// a client resending a fix it already sent, with the SAME timestamp. It must be
// recognised as a replay.
//
// This failed before RecordGeofenceFix truncated to microseconds. Postgres
// TIMESTAMPTZ stores microseconds and Go's time.Time carries nanoseconds, so a
// timestamp did not survive its own round trip: the incoming value compared as
// strictly newer than the stored copy of itself, and the replay guard let it
// through. Clients resend identical timestamps -- they do not invent fresh
// ones -- so this is the boundary that matters, not an edge case.
func TestResendingTheSameTimestampIsAReplay(t *testing.T) {
	store := testStore(t)
	id, cleanup := geofencedWorkflow(t, "same-ts")
	t.Cleanup(cleanup)
	ctx := context.Background()

	// A timestamp with sub-microsecond digits, which is what time.Now() gives
	// and therefore what a real client sends.
	at := time.Now().Add(-time.Hour).Truncate(time.Nanosecond).Add(432 * time.Nanosecond)

	if _, err := store.RecordGeofenceFix(ctx, id, false, at); err != nil {
		t.Fatal(err)
	}
	got, err := store.RecordGeofenceFix(ctx, id, false, at)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Stale {
		t.Fatalf("resending an identical timestamp must be reported stale, got %+v", got)
	}
	if got.Fired {
		t.Fatalf("a replay must never fire a crossing, got %+v", got)
	}
}

// Two fixes closer together than Postgres can represent are the same instant
// as far as the stored state is concerned. Treating the second as newer would
// reopen the same hole from the other side.
func TestSubMicrosecondDifferenceIsStillAReplay(t *testing.T) {
	store := testStore(t)
	id, cleanup := geofencedWorkflow(t, "sub-us")
	t.Cleanup(cleanup)
	ctx := context.Background()

	base := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	if _, err := store.RecordGeofenceFix(ctx, id, false, base); err != nil {
		t.Fatal(err)
	}
	got, err := store.RecordGeofenceFix(ctx, id, true, base.Add(500*time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Stale {
		t.Fatalf("a sub-microsecond step is the same instant once stored; want stale, got %+v", got)
	}
	if got.Fired {
		t.Fatalf("it must not fire a crossing either, got %+v", got)
	}
}
