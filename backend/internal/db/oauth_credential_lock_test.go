package db_test

import (
	"context"
	"testing"
	"time"
)

// TestLockOAuthCredentialForRefresh_SerializesSameID verifies the advisory
// lock actually blocks a second acquirer for the SAME credential ID until
// the first releases -- this is the whole point of the lock (simulating two
// backend replicas both trying to refresh the same expired OAuth
// credential), so an assertion that never blocks would silently defeat it.
func TestLockOAuthCredentialForRefresh_SerializesSameID(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	const credID = "lock-test-cred-same-id"

	release1, err := store.LockOAuthCredentialForRefresh(ctx, credID)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		release2, err := store.LockOAuthCredentialForRefresh(ctx, credID)
		if err != nil {
			t.Errorf("second lock: %v", err)
			return
		}
		defer release2(context.Background())
		close(acquired)
	}()

	select {
	case <-acquired:
		t.Fatal("want second acquirer to block while the first still holds the lock")
	case <-time.After(200 * time.Millisecond):
		// Expected: still blocked.
	}

	release1(ctx)

	select {
	case <-acquired:
		// Expected: unblocks once the first lock is released.
	case <-time.After(2 * time.Second):
		t.Fatal("want second acquirer to unblock after the first releases")
	}
}

// TestLockOAuthCredentialForRefresh_DifferentIDsDontBlock guards against an
// overly-coarse lock key (e.g. accidentally locking a fixed constant instead
// of hashtext(id)) that would serialize refreshes across UNRELATED
// credentials, which would be a correctness regression of its own --
// concurrent refreshes for two different users' credentials must not
// contend with each other.
func TestLockOAuthCredentialForRefresh_DifferentIDsDontBlock(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	release1, err := store.LockOAuthCredentialForRefresh(ctx, "lock-test-cred-a")
	if err != nil {
		t.Fatalf("lock a: %v", err)
	}
	defer release1(ctx)

	done := make(chan struct{})
	go func() {
		release2, err := store.LockOAuthCredentialForRefresh(ctx, "lock-test-cred-b")
		if err != nil {
			t.Errorf("lock b: %v", err)
			return
		}
		defer release2(context.Background())
		close(done)
	}()

	select {
	case <-done:
		// Expected: a different credential ID's lock isn't blocked by "a"'s.
	case <-time.After(2 * time.Second):
		t.Fatal("want a different credential ID to acquire its lock without waiting on an unrelated one")
	}
}
