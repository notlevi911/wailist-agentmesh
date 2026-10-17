package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/testutil"
)

func TestConnect(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := db.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()
}

// testStore is shared by all db tests.
func testStore(t *testing.T) *db.Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := db.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(store.Close)
	testutil.EnsureUsers(t, "dev")
	return store
}
