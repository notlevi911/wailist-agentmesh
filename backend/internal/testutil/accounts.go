package testutil

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

// EnsureUsers gives older fixtures real owners under the account ownership constraints.
func EnsureUsers(t *testing.T, ids ...string) {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	for _, id := range ids {
		if _, err := conn.Exec(t.Context(), `
			INSERT INTO users (id, email, password_hash) VALUES ($1, $1 || '@fixture.test', '')
			ON CONFLICT (id) DO NOTHING
		`, id); err != nil {
			t.Fatal(err)
		}
	}
}
