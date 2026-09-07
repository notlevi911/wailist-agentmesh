package db

import (
	"context"
	"time"
)

// Usage reporting reads debit_ledger — the same rows the engine writes when it
// actually charges a user (byok_flat_fee for an LLM step, x402_platform_fee
// and x402_relay_cost for a paid x402 call). That makes these numbers the real
// spend, not an estimate reconstructed from logs.
//
// A single paid x402 call can produce two ledger rows (the platform fee and
// the relay cost), so anything counting *calls* counts distinct (run, node)
// pairs rather than rows — otherwise every call would report as two.

const x402KindFilter = `kind <> 'byok_flat_fee'`

type UsageTotals struct {
	TotalUSDMicros int64
	X402Calls      int64
}

// UsageTotalsSince returns spend and call count for one window. Callers ask
// twice (current window, preceding window of equal length) to compute a delta.
func (s *Store) UsageTotalsSince(ctx context.Context, userID string, since time.Time, until *time.Time) (UsageTotals, error) {
	var t UsageTotals
	query := `
		SELECT COALESCE(SUM(amount_usd_micros), 0),
		       COUNT(DISTINCT (run_id, node_id)) FILTER (WHERE ` + x402KindFilter + `)
		FROM debit_ledger
		WHERE user_id = $1 AND created_at >= $2 AND ($3::timestamptz IS NULL OR created_at < $3)`
	err := s.pool.QueryRow(ctx, query, userID, since, until).Scan(&t.TotalUSDMicros, &t.X402Calls)
	return t, err
}

type UsageBucket struct {
	Bucket        time.Time
	X402USDMicros int64
	LLMUSDMicros  int64
	Calls         int64
}

// UsageTimeseries buckets spend by hour or day. bucket is validated by the
// caller against a fixed set — it reaches date_trunc as a bind parameter, so
// it can never be SQL, but an unrecognised value would still error at runtime.
func (s *Store) UsageTimeseries(ctx context.Context, userID string, since time.Time, bucket string) ([]UsageBucket, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT date_trunc($3, created_at) AS b,
		       COALESCE(SUM(amount_usd_micros) FILTER (WHERE `+x402KindFilter+`), 0),
		       COALESCE(SUM(amount_usd_micros) FILTER (WHERE kind = 'byok_flat_fee'), 0),
		       COUNT(DISTINCT (run_id, node_id)) FILTER (WHERE `+x402KindFilter+`)
		FROM debit_ledger
		WHERE user_id = $1 AND created_at >= $2
		GROUP BY b ORDER BY b`, userID, since, bucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UsageBucket
	for rows.Next() {
		var b UsageBucket
		if err := rows.Scan(&b.Bucket, &b.X402USDMicros, &b.LLMUSDMicros, &b.Calls); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

type WorkflowSpendRow struct {
	WorkflowID string
	Name       string
	Status     string
	USDMicros  int64
	Calls      int64
}

func (s *Store) UsageByWorkflow(ctx context.Context, userID string, since time.Time) ([]WorkflowSpendRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.workflow_id, w.name, w.status,
		       COALESCE(SUM(d.amount_usd_micros), 0),
		       COUNT(DISTINCT (d.run_id, d.node_id)) FILTER (WHERE d.`+x402KindFilter+`)
		FROM debit_ledger d
		JOIN workflows w ON w.id = d.workflow_id
		WHERE d.user_id = $1 AND d.created_at >= $2
		GROUP BY d.workflow_id, w.name, w.status
		ORDER BY 4 DESC`, userID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WorkflowSpendRow
	for rows.Next() {
		var r WorkflowSpendRow
		if err := rows.Scan(&r.WorkflowID, &r.Name, &r.Status, &r.USDMicros, &r.Calls); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type EndpointSpendRow struct {
	Endpoint   string
	Name       string
	IsX402     bool
	USDMicros  int64
	Calls      int64
	LastUsedAt time.Time
}

// UsageByEndpoint resolves each ledger row's node_id back to the node inside
// its workflow's graph JSON, so spend can be attributed to the actual URL that
// was called rather than an opaque node id. A node deleted since the call
// leaves no match; those rows fall back to the node id so their spend is still
// reported instead of silently vanishing from the totals.
func (s *Store) UsageByEndpoint(ctx context.Context, userID string, since time.Time) ([]EndpointSpendRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(NULLIF(n.node->>'endpoint', ''), NULLIF(n.node->>'url', ''), d.node_id) AS endpoint,
		       COALESCE(n.node->>'name', '') AS name,
		       bool_or(d.`+x402KindFilter+`) AS is_x402,
		       COALESCE(SUM(d.amount_usd_micros), 0),
		       COUNT(DISTINCT (d.run_id, d.node_id)),
		       MAX(d.created_at)
		FROM debit_ledger d
		JOIN workflows w ON w.id = d.workflow_id
		LEFT JOIN LATERAL (
			SELECT elem AS node
			FROM jsonb_array_elements(w.graph->'nodes') elem
			WHERE elem->>'id' = d.node_id
			LIMIT 1
		) n ON true
		WHERE d.user_id = $1 AND d.created_at >= $2
		GROUP BY 1, 2
		ORDER BY 4 DESC`, userID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EndpointSpendRow
	for rows.Next() {
		var r EndpointSpendRow
		if err := rows.Scan(&r.Endpoint, &r.Name, &r.IsX402, &r.USDMicros, &r.Calls, &r.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SettlementRow is one on-chain x402 payment a user's own run produced, read
// back from the run_logs receipt the engine persisted at the moment it
// settled.
type SettlementRow struct {
	Ts          time.Time
	WorkflowID  string
	Endpoint    string
	USDMicros   int64
	TxID        string
	ExplorerURL string
}

// ListSettlements returns the x402 settlements belonging to a user's runs,
// newest first.
//
// Source is run_logs, not x402_relay_settlements: the relay table records
// every payment the public relay ever brokered and has no user_id (the relay
// route is unauthenticated, so at insert time there is no user to attribute
// to). The receipts a user's OWN runs produced are a different, answerable
// question — runner.go writes one run_logs row per settlement precisely so a
// dropped SSE stream cannot lose the on-chain record, and run_logs reaches a
// user through runs -> workflows.
//
// This replaces a localStorage copy the usage page kept, which was per-browser
// and therefore vanished on sign-out or a device change while the underlying
// receipts sat in the database the whole time.
//
// Amount matches the frontend's old settledUsdOf(): the settled amount plus
// the platform markup, since a v2 call's real total is the sum of both (see
// nodes.paymentReceipt).
//
// Rows are de-duplicated by tx id, which is not optional. A run-funded agent
// settles ONE real inbound payment up front and every tool call it then makes
// reports that same tx id with only its own slice of the amount — stated at
// nodes/tool402.go's "The same id repeats across every call in the run by
// design ... so consumers that key on tx id must de-duplicate". Listing them
// all shows a run's spend at roughly double what moved on-chain (a $0.50
// pre-fund plus four per-call receipts reads as $1.05).
//
// Two details the naive dedup gets wrong:
//
//   - The winner of a tx id group must be the run-funding row, which carries
//     the full amount that actually moved rather than one call's slice. It is
//     identified by isFundingReceipt (runner.go sets it for exactly this) and
//     not by ordering alone, since the funding row and the per-call receipts
//     it covers are written in one loop and can share a timestamp.
//   - A settlement that returned no tx id is still a real debit and has
//     nothing to dedupe against. Collapsing those together (every "" being
//     equal) dropped all but one and under-reported real spend, so each falls
//     back to its own row id and survives on its own.
func (s *Store) ListSettlements(ctx context.Context, userID string, limit int) ([]SettlementRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ts, workflow_id, endpoint, usd_micros, tx_id, explorer_url FROM (
			SELECT DISTINCT ON (
			         CASE WHEN COALESCE(l.output->>'txId', '') = ''
			              THEN l.id ELSE l.output->>'txId' END)
			       l.ts,
			       r.workflow_id,
			       COALESCE(NULLIF(l.output->>'nodeName', ''), 'x402 endpoint') AS endpoint,
			       COALESCE((l.output->>'settledUsdMicros')::bigint, 0)
			         + COALESCE((l.output->>'platformFeeUsdMicros')::bigint, 0) AS usd_micros,
			       COALESCE(l.output->>'txId', '') AS tx_id,
			       COALESCE(l.output->>'explorerURL', '') AS explorer_url
			FROM run_logs l
			JOIN runs r ON r.id = l.run_id
			JOIN workflows w ON w.id = r.workflow_id
			WHERE w.user_id = $1
			  -- Not the "?" existence operator: pgx also treats "?" as a
			  -- placeholder in some query modes, and this backend runs the
			  -- simple protocol behind PgBouncer. ->> IS NOT NULL asks the
			  -- same question with no operator mistakable for a bind marker.
			  AND l.output->>'settledUsdMicros' IS NOT NULL
			-- DISTINCT ON requires its expression to lead ORDER BY; the rest
			-- picks the winner within each tx id group (funding row first).
			ORDER BY CASE WHEN COALESCE(l.output->>'txId', '') = ''
			              THEN l.id ELSE l.output->>'txId' END,
			         (l.output->>'isFundingReceipt') IS NOT NULL DESC,
			         l.ts ASC, l.id ASC
		) s
		ORDER BY s.ts DESC, s.usd_micros DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Non-nil so a user with no settlements marshals as [] rather than null.
	out := make([]SettlementRow, 0, limit)
	for rows.Next() {
		var r SettlementRow
		if err := rows.Scan(&r.Ts, &r.WorkflowID, &r.Endpoint, &r.USDMicros, &r.TxID, &r.ExplorerURL); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
