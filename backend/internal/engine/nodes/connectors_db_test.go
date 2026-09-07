package nodes_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
	"github.com/jackc/pgx/v5"
)

func TestPostgresAction_SkipsWithoutConnString(t *testing.T) {
	node := models.WorkflowNode{
		ID: "db1", Type: models.NodeTypeAction, Template: "db",
		Config: map[string]string{"pgTable": "events", "pgColumn": "payload"},
	}
	rc := engine.NewRunContext("r1", nil)
	got, _ := nodes.ExecuteAction(context.Background(), node, rc)
	if got != "db_skipped_no_conn_string" {
		t.Errorf("want skip sentinel, got %v", got)
	}
}

func TestPostgresAction_SkipsWithoutTableOrColumn(t *testing.T) {
	node := models.WorkflowNode{
		ID: "db1", Type: models.NodeTypeAction, Template: "db",
		Secrets: map[string]string{"pgConnString": "postgres://localhost/x"},
	}
	rc := engine.NewRunContext("r1", nil)
	got, _ := nodes.ExecuteAction(context.Background(), node, rc)
	if got != "db_skipped_missing_config" {
		t.Errorf("want skip sentinel, got %v", got)
	}
}

// Identifiers reach SQL as text and cannot be bound as parameters, so they
// must be rejected rather than escaped-and-hoped.
func TestPostgresAction_RejectsUnsafeIdentifiers(t *testing.T) {
	for _, bad := range []string{
		`events; DROP TABLE users--`,
		`events"`,
		`ev ents`,
		`"events"`,
		``,
	} {
		node := models.WorkflowNode{
			ID: "db1", Type: models.NodeTypeAction, Template: "db",
			Secrets: map[string]string{"pgConnString": "postgres://localhost/x"},
			Config:  map[string]string{"pgTable": bad, "pgColumn": "payload"},
		}
		rc := engine.NewRunContext("r1", nil)
		got, err := nodes.ExecuteAction(context.Background(), node, rc)
		if err == nil && got != "db_skipped_missing_config" {
			t.Errorf("table %q should be rejected, got %v (err %v)", bad, got, err)
		}
	}
}

// A host that accepts the TCP connection but never speaks the Postgres wire
// protocol back (a silent black hole, not a refusal) must not hang the node
// forever — pgConnectTimeout should cut it off.
func TestPostgresAction_ConnectTimesOutInsteadOfHangingForever(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Accept but never respond — deliberately never Close either,
			// so the client blocks reading the startup response until its
			// own context deadline fires.
			_ = conn
		}
	}()

	nodes.SetPostgresConnectTimeoutForTest(50 * time.Millisecond)
	defer nodes.SetPostgresConnectTimeoutForTest(0)

	node := models.WorkflowNode{
		ID: "db1", Type: models.NodeTypeAction, Template: "db",
		Secrets: map[string]string{
			"pgConnString": fmt.Sprintf("postgres://user:pass@%s/db?sslmode=disable", ln.Addr().String()),
		},
		Config: map[string]string{"pgTable": "events", "pgColumn": "payload"},
	}
	rc := engine.NewRunContext("r1", nil)

	done := make(chan struct{})
	var gotErr error
	go func() {
		_, gotErr = nodes.ExecuteAction(context.Background(), node, rc)
		close(done)
	}()

	select {
	case <-done:
		if gotErr == nil {
			t.Error("want a connect-timeout error, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sendPostgres hung well past pgConnectTimeout — the connect timeout is not being applied")
	}
}

// Real insert against a live Postgres. Skipped unless TEST_POSTGRES_URL is set,
// so the default `go test ./...` needs no database.
// A table created via unquoted DDL (CREATE TABLE Events (...)) is actually
// stored lowercase ("events") -- Postgres folds unquoted identifiers. A user
// who types pgTable="Events", matching what they typed in their own DDL,
// must still resolve: quoting the mixed-case value verbatim instead of
// lowercasing it first would send INSERT INTO "Events", which Postgres
// rejects with `relation "Events" does not exist` even though the table is
// right there as "events".
func TestPostgresAction_ResolvesMixedCaseUnquotedTableName(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_URL to run the live Postgres insert test")
	}
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), `DROP TABLE IF EXISTS mixedcaseevents`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	// Deliberately unquoted DDL with mixed-case input -- Postgres folds this
	// to "mixedcaseevents".
	if _, err := conn.Exec(context.Background(), `CREATE TABLE MixedCaseEvents (payload text)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	defer conn.Exec(context.Background(), `DROP TABLE mixedcaseevents`)

	node := models.WorkflowNode{
		ID: "db2", Type: models.NodeTypeAction, Template: "db",
		Secrets: map[string]string{"pgConnString": dsn},
		Config: map[string]string{
			"pgTable":  "MixedCaseEvents",
			"pgColumn": "payload",
		},
	}
	rc := engine.NewRunContext("r1", []byte(`"row from the workflow"`))
	got, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatalf("insert into a mixed-case-but-unquoted-DDL table should succeed: %v", err)
	}
	if got != "db_row_inserted" {
		t.Errorf("want 'db_row_inserted', got %v", got)
	}
}

// A table created via quoted DDL (CREATE TABLE "Events" (...), common with
// ORM-managed schemas) keeps its case exactly -- the opposite of the
// unquoted-DDL case above. pgTable="Events" must still resolve here too:
// quotePGIdentifier's lowercase-first attempt will miss, and sendPostgres
// must retry with the identifier exactly as configured rather than give up.
func TestPostgresAction_ResolvesMixedCaseQuotedTableName(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_URL to run the live Postgres insert test")
	}
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), `DROP TABLE IF EXISTS "QuotedCaseEvents"`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	// Deliberately quoted DDL -- Postgres preserves this exact case, unlike
	// the sibling unquoted-DDL test above.
	if _, err := conn.Exec(context.Background(), `CREATE TABLE "QuotedCaseEvents" (payload text)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	defer conn.Exec(context.Background(), `DROP TABLE "QuotedCaseEvents"`)

	node := models.WorkflowNode{
		ID: "db3", Type: models.NodeTypeAction, Template: "db",
		Secrets: map[string]string{"pgConnString": dsn},
		Config: map[string]string{
			"pgTable":  "QuotedCaseEvents",
			"pgColumn": "payload",
		},
	}
	rc := engine.NewRunContext("r1", []byte(`"row from the workflow"`))
	got, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatalf("insert into a quoted-case-preserved table should succeed via the fallback retry: %v", err)
	}
	if got != "db_row_inserted" {
		t.Errorf("want 'db_row_inserted', got %v", got)
	}
}

func TestPostgresAction_InsertsRow(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_URL to run the live Postgres insert test")
	}
	node := models.WorkflowNode{
		ID: "db1", Type: models.NodeTypeAction, Template: "db",
		Secrets: map[string]string{"pgConnString": dsn},
		Config: map[string]string{
			"pgTable":  "agentmesh_test_events",
			"pgColumn": "payload",
		},
	}
	rc := engine.NewRunContext("r1", []byte(`"row from the workflow"`))
	got, err := nodes.ExecuteAction(context.Background(), node, rc)
	if err != nil {
		t.Fatalf("insert failed — create the table first:\n"+
			"  CREATE TABLE agentmesh_test_events (payload text);\n%v", err)
	}
	if got != "db_row_inserted" {
		t.Errorf("want 'db_row_inserted', got %v", got)
	}
}
