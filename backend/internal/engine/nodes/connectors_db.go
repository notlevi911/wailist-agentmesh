package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/agentmesh/backend/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgConnectTimeout bounds how long sendPostgres waits to establish a
// connection. Without it, a misconfigured or firewalled host that silently
// drops SYN packets (rather than refusing the connection) hangs this node —
// and the whole run, since node execution is sequential per topological
// level — until the caller cancels it by hand.
var pgConnectTimeout = 10 * time.Second

// pgQueryTimeout bounds each Exec call (the INSERT itself, and its
// lowercase-then-verbatim retry -- see quotePGIdentifier's doc comment).
// pgConnectTimeout alone doesn't cover this: a successful connection can
// still hang indefinitely on a row/table lock held by another transaction,
// or a network stall after the handshake, which is the same "hangs the
// whole run" failure pgConnectTimeout exists to prevent, just relocated
// from connect to query.
var pgQueryTimeout = 10 * time.Second

// SetPostgresConnectTimeoutForTest overrides pgConnectTimeout and
// pgQueryTimeout together. Call only from tests. Pass 0 to reset both to
// their real 10s default.
func SetPostgresConnectTimeoutForTest(d time.Duration) {
	if d <= 0 {
		pgConnectTimeout = 10 * time.Second
		pgQueryTimeout = 10 * time.Second
	} else {
		pgConnectTimeout = d
		pgQueryTimeout = d
	}
}

// pgIdentifier matches a safe unquoted SQL identifier. Table and column names
// cannot be sent as bind parameters — they are part of the statement text — so
// anything not matching this is rejected outright rather than escaped. An
// optional schema qualifier is allowed: "public.events".
var pgIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`)

// quotePGIdentifier wraps each dot-separated part in double quotes, optionally
// lowercasing each part first. Only ever called on strings already validated
// by pgIdentifier.
//
// Two real, both-common cases disagree about what a mixed-case pgTable value
// should resolve to, and there's no way to tell which one a given user meant
// from the string alone:
//   - A table created via unquoted DDL (`CREATE TABLE Events (...)`) is
//     folded to lowercase by Postgres and stored as `events`. Quoting a
//     mixed-case value verbatim would send `INSERT INTO "Events"`, which
//     Postgres rejects with `relation "Events" does not exist` even though
//     the table is right there as "events".
//   - A table created via quoted DDL (`CREATE TABLE "Events" (...)`, common
//     with ORM-managed schemas e.g. Prisma) keeps its case exactly.
//     Lowercasing that value would send `INSERT INTO "events"`, which fails
//     the exact same way against a table that's actually "Events".
//
// sendPostgres tries lower=true first (the more common unquoted-DDL case)
// and retries once with lower=false on an undefined-table/column error,
// rather than picking one interpretation and permanently breaking whichever
// case doesn't match it.
func quotePGIdentifier(ident string, lower bool) string {
	parts := strings.Split(ident, ".")
	for i, p := range parts {
		if lower {
			p = strings.ToLower(p)
		}
		parts[i] = `"` + p + `"`
	}
	return strings.Join(parts, ".")
}

// sendPostgres inserts one row containing the run output. Values are always
// bound as parameters; only the table and column names are interpolated, and
// only after passing pgIdentifier.
func sendPostgres(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	connString := secretVal(node, "pgConnString")
	if connString == "" {
		return "db_skipped_no_conn_string", ErrActionSkipped
	}
	table := configVal(node, "pgTable", "")
	column := configVal(node, "pgColumn", "")
	if table == "" || column == "" {
		return "db_skipped_missing_config", ErrActionSkipped
	}
	if !pgIdentifier.MatchString(table) {
		return nil, fmt.Errorf("db: table name %q is not a valid SQL identifier", table)
	}
	if !pgIdentifier.MatchString(column) {
		return nil, fmt.Errorf("db: column name %q is not a valid SQL identifier", column)
	}

	extraCols := []string{column}
	vals := []any{resolveMessage(node, rc)}

	if extra := configVal(node, "pgExtraColumns", ""); extra != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(extra), &m); err != nil {
			return nil, fmt.Errorf("db: `pgExtraColumns` is not a valid JSON object: %w", err)
		}
		for k, v := range m {
			if !pgIdentifier.MatchString(k) {
				return nil, fmt.Errorf("db: extra column name %q is not a valid SQL identifier", k)
			}
			extraCols = append(extraCols, k)
			if s, ok := v.(string); ok {
				vals = append(vals, resolveTemplate(s, rc))
				continue
			}
			vals = append(vals, v)
		}
	}

	placeholders := make([]string, len(vals))
	for i := range vals {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	buildStmt := func(lower bool) string {
		cols := make([]string, len(extraCols))
		for i, c := range extraCols {
			cols[i] = quotePGIdentifier(c, lower)
		}
		return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
			quotePGIdentifier(table, lower), strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	}

	// A fresh connection per call, not a pool: pgConnString is per-node,
	// user-supplied config that can point at a different Postgres instance
	// (and different credentials) on every node in a workflow, so a shared
	// pool would need its own per-connString pool management -- effectively
	// a second credential-scoped pooling layer on top of pgxpool, whose
	// eviction/lifecycle is its own real feature, not a drop-in swap here.
	// pgConnectTimeout keeps the cost of that bounded rather than free.
	connCtx, cancel := context.WithTimeout(ctx, pgConnectTimeout)
	defer cancel()
	conn, err := pgx.Connect(connCtx, connString)
	if err != nil {
		return nil, fmt.Errorf("db: could not connect: %w", err)
	}
	defer conn.Close(ctx)

	// pgConnectTimeout only bounds the connection above -- a successful
	// connection can still hang indefinitely on a row/table lock held by
	// another transaction, or a network stall after the handshake, which is
	// the same "hangs the whole run" failure pgConnectTimeout exists to
	// prevent, just relocated from connect to query. Each Exec (including
	// the retry) gets its own fresh budget rather than sharing one across
	// both attempts, so a slow-but-eventually-successful first attempt
	// doesn't starve the retry's budget.
	queryCtx, queryCancel := context.WithTimeout(ctx, pgQueryTimeout)
	defer queryCancel()
	if _, err := conn.Exec(queryCtx, buildStmt(true), vals...); err != nil {
		// See quotePGIdentifier's doc comment: a lowercased identifier is the
		// right call for a table/column created via unquoted DDL, but wrong
		// for one created via quoted, case-preserved DDL. Retry once with
		// the identifiers exactly as configured before giving up, rather
		// than permanently breaking whichever case the first attempt didn't
		// match.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "42P01" || pgErr.Code == "42703") {
			retryCtx, retryCancel := context.WithTimeout(ctx, pgQueryTimeout)
			defer retryCancel()
			if _, err2 := conn.Exec(retryCtx, buildStmt(false), vals...); err2 != nil {
				return nil, fmt.Errorf("db: insert failed (tried both lowercased and as-configured identifiers): %w", err2)
			}
			return "db_row_inserted", nil
		}
		return nil, fmt.Errorf("db: insert failed: %w", err)
	}
	return "db_row_inserted", nil
}
