package handlers

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/respond"
)

// Usage endpoints report real spend out of debit_ledger. Every amount is USD
// micros in the database; responses are plain USD, which is the unit users
// actually top up and are billed in.
//
// The frontend's field names still say "algo" (they predate credits being
// denominated in USD). They are left alone here rather than renamed on both
// sides mid-flight — the values are USD, and the page labels them as such.
//
// Tendril compute is folded into these same x402 figures rather than living
// behind its own endpoint or panel: renting a machine is still paying an
// x402 endpoint (Tendril's), just through the console's direct-action
// buttons instead of a canvas node. It reads from tendril_credit_ledger
// (Tendril's own per-user sub-ledger — see tendril.go's ExecuteTendril doc
// comments for why that ledger exists separately from debit_ledger) and
// merges in here rather than there.

func rangeWindow(raw string) (since time.Duration, bucket string) {
	switch raw {
	case "24h":
		return 24 * time.Hour, "hour"
	case "30d":
		return 30 * 24 * time.Hour, "day"
	default: // 7d
		return 7 * 24 * time.Hour, "day"
	}
}

func usd(micros int64) float64 { return float64(micros) / 1e6 }

// tendrilWindow is Tendril activity reduced to what UsageSummary/
// UsageByEndpoint/UsageSettlements each need, from one ledger read.
type tendrilWindow struct {
	NetSpentUSDMicros int64 // charged minus refunded — real compute cost, never the gross reservation
	Charges           int64 // count of rents in this window — this feature's equivalent of an x402 call
	LastChargeAt      time.Time
	Topups            []tendrilTopup
}

type tendrilTopup struct {
	AmountUSDMicros int64
	TxID            string
	CreatedAt       time.Time
}

// tendrilUsageWindow reads the ledger from `since` and optionally trims to
// before `until` (for the "previous window" comparison UsageSummary needs) —
// same shape as UsageTotalsSince's since/until pair, kept separate because
// this reads a different table.
func (d *Deps) tendrilUsageWindow(ctx context.Context, userID string, since time.Time, until *time.Time) (tendrilWindow, error) {
	rows, err := d.Store.TendrilCreditLedgerSince(ctx, userID, since)
	if err != nil {
		return tendrilWindow{}, err
	}
	var out tendrilWindow
	for _, e := range rows {
		if until != nil && !e.CreatedAt.Before(*until) {
			continue
		}
		switch e.Kind {
		case "charge":
			out.NetSpentUSDMicros += e.AmountUSDMicros
			out.Charges++
			if e.CreatedAt.After(out.LastChargeAt) {
				out.LastChargeAt = e.CreatedAt
			}
		case "refund":
			out.NetSpentUSDMicros -= e.AmountUSDMicros
		case "topup":
			// Only a topup has a real on-chain leg — charge/refund are
			// internal movements against credit already sitting in the pool.
			if e.TxID != nil && *e.TxID != "" {
				out.Topups = append(out.Topups, tendrilTopup{
					AmountUSDMicros: e.AmountUSDMicros, TxID: *e.TxID, CreatedAt: e.CreatedAt,
				})
			}
		}
	}
	// A refund can legitimately land inside the window while its paired
	// charge (written at rent time, potentially hours earlier) falls
	// outside it — an early release right after a window boundary is
	// ordinary usage, not an edge case. Unclamped, that goes negative and
	// corrupts three call sites: UsageSummary's totalAlgo (renders as
	// negative dollars) and deltaPct, and UsageByEndpoint's shared
	// denominator (inflating every other row's pctOfSpend, sometimes past
	// 100%). Same clamp the release handler already applies to `refunded`
	// in leases.go.
	if out.NetSpentUSDMicros < 0 {
		out.NetSpentUSDMicros = 0
	}
	return out, nil
}

func (d *Deps) UsageSummary(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	window, _ := rangeWindow(r.URL.Query().Get("range"))
	now := time.Now()
	since := now.Add(-window)

	current, err := d.Store.UsageTotalsSince(r.Context(), userID, since, nil)
	if err != nil {
		log.Printf("usage summary: %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	currentTendril, err := d.tendrilUsageWindow(r.Context(), userID, since, nil)
	if err != nil {
		log.Printf("usage summary (tendril): %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	// The immediately preceding window of equal length, so the headline
	// percentage compares like with like.
	prevEnd := since
	previous, err := d.Store.UsageTotalsSince(r.Context(), userID, since.Add(-window), &prevEnd)
	if err != nil {
		log.Printf("usage summary (previous window): %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	previousTendril, err := d.tendrilUsageWindow(r.Context(), userID, since.Add(-window), &prevEnd)
	if err != nil {
		log.Printf("usage summary (previous window, tendril): %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	totalCurrent := current.TotalUSDMicros + currentTendril.NetSpentUSDMicros
	totalPrevious := previous.TotalUSDMicros + previousTendril.NetSpentUSDMicros

	// Growth from a zero baseline has no defined percentage — reporting a
	// spike from nothing would be noise, so it reads as 0% change.
	deltaPct := 0.0
	if totalPrevious > 0 {
		deltaPct = (float64(totalCurrent-totalPrevious) / float64(totalPrevious)) * 100
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"totalAlgo":  usd(totalCurrent),
		"x402Calls":  current.X402Calls + currentTendril.Charges,
		"llmTokens":  0,
		"llmEstAlgo": nil,
		"budget":     nil,
		"deltas":     map[string]any{"totalAlgoPct": deltaPct},
	})
}

func (d *Deps) UsageTimeseries(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	window, bucket := rangeWindow(r.URL.Query().Get("range"))
	// The client sends its own bucket hint; honour it only if it is one of the
	// two we support, so an arbitrary value can never reach date_trunc.
	if b := r.URL.Query().Get("bucket"); b == "hour" || b == "day" {
		bucket = b
	}

	buckets, err := d.Store.UsageTimeseries(r.Context(), userID, time.Now().Add(-window), bucket)
	if err != nil {
		log.Printf("usage timeseries: %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	layout := "Jan 2"
	if bucket == "hour" {
		layout = "15:04"
	}
	out := make([]map[string]any, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, map[string]any{
			"ts":       b.Bucket.Format(layout),
			"x402Algo": usd(b.X402USDMicros),
			"llmAlgo":  usd(b.LLMUSDMicros),
			"calls":    b.Calls,
		})
	}
	respond.JSON(w, http.StatusOK, out)
}

func (d *Deps) UsageByWorkflow(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	window, _ := rangeWindow(r.URL.Query().Get("range"))

	rows, err := d.Store.UsageByWorkflow(r.Context(), userID, time.Now().Add(-window))
	if err != nil {
		log.Printf("usage by-workflow: %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"workflowId": row.WorkflowID,
			"name":       row.Name,
			"status":     row.Status,
			"algo":       usd(row.USDMicros),
			"calls":      row.Calls,
		})
	}
	respond.JSON(w, http.StatusOK, out)
}

func (d *Deps) UsageByEndpoint(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	window, _ := rangeWindow(r.URL.Query().Get("range"))

	rows, err := d.Store.UsageByEndpoint(r.Context(), userID, time.Now().Add(-window))
	if err != nil {
		log.Printf("usage by-endpoint: %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	tendril, err := d.tendrilUsageWindow(r.Context(), userID, time.Now().Add(-window), nil)
	if err != nil {
		log.Printf("usage by-endpoint (tendril): %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var total int64
	for _, row := range rows {
		total += row.USDMicros
	}
	total += tendril.NetSpentUSDMicros

	out := make([]map[string]any, 0, len(rows)+1)
	for _, row := range rows {
		host := row.Endpoint
		if u, err := url.Parse(row.Endpoint); err == nil && u.Host != "" {
			host = u.Host
		}
		kind := "llm"
		if row.IsX402 {
			kind = "x402"
		}
		// A per-call unit price is only meaningful where each call costs the
		// same; token-billed LLM steps have no such figure, so they report
		// null rather than an average masquerading as a price.
		var unitPrice any
		if row.IsX402 && row.Calls > 0 {
			unitPrice = usd(row.USDMicros) / float64(row.Calls)
		}
		pct := 0.0
		if total > 0 {
			pct = float64(row.USDMicros) / float64(total) * 100
		}
		provider := row.Name
		if provider == "" {
			provider = strings.TrimPrefix(host, "www.")
		}
		out = append(out, map[string]any{
			"endpoint":    row.Endpoint,
			"host":        host,
			"provider":    provider,
			"type":        kind,
			"calls":       row.Calls,
			"unitPrice":   unitPrice,
			"unit":        "call",
			"totalAlgo":   usd(row.USDMicros),
			"pctOfSpend":  pct,
			"successRate": nil,
			"lastUsedAt":  row.LastUsedAt.UTC().Format(time.RFC3339),
		})
	}
	if tendril.Charges > 0 {
		pct := 0.0
		if total > 0 {
			pct = float64(tendril.NetSpentUSDMicros) / float64(total) * 100
		}
		out = append(out, map[string]any{
			"endpoint":    "https://tendrilregister.007575.xyz",
			"host":        "tendrilregister.007575.xyz",
			"provider":    "Tendril",
			"type":        "x402",
			"calls":       tendril.Charges,
			"unitPrice":   usd(tendril.NetSpentUSDMicros) / float64(tendril.Charges),
			"unit":        "rental",
			"totalAlgo":   usd(tendril.NetSpentUSDMicros),
			"pctOfSpend":  pct,
			"successRate": nil,
			"lastUsedAt":  tendril.LastChargeAt.UTC().Format(time.RFC3339),
		})
	}
	respond.JSON(w, http.StatusOK, out)
}

// maxSettlementsLimit bounds ?limit= on the settlements endpoint. Without a
// ceiling, one authenticated request could pass a limit large enough that
// allocating the result slice up front exhausts memory.
//
// It bounds the rows returned, NOT the work done to find them: ListSettlements
// must de-duplicate a run-funded run's repeated tx id before any limit can
// apply, so it reads and sorts every settlement receipt the user owns whatever
// this is set to. Migration 000032 indexes the join; keeping the read itself
// proportional to the page size would need a different query shape (a windowed
// scan over a bounded time range).
const maxSettlementsLimit = 200

// UsageSettlements reports real on-chain payments, from two user-scoped
// sources merged newest-first.
//
// The general x402 relay settlement rows (x402_relay_settlements) are still
// NOT one of them: that table carries no user_id — the relay is a public,
// unauthenticated route, so at insert time there is no user to attribute a
// settlement to — and fabricating an owner would be worse than omitting them.
//
//  1. x402 settlements from the user's own runs, read from the run_logs
//     receipts the engine persists per settlement (db.ListSettlements). These
//     reach a user through runs -> workflows, which is a different and
//     answerable question from the relay table's. The usage page used to
//     rebuild this list client-side into localStorage, which was per-browser
//     and lost on sign-out or a device change.
//  2. Tendril top-ups — a real Wallet 1 -> Wallet 2 payment recorded in
//     tendril_credit_ledger, scoped to a user from the moment it's written.
func (d *Deps) UsageSettlements(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		// A negative value passes Atoi (it's a valid number) but panics
		// make([]T, 0, v) below with "makeslice: cap out of range" — caught
		// by chi.Recoverer as a 500 instead of the 400 this guard intends.
		if err != nil || v < 1 {
			respond.Error(w, http.StatusBadRequest, "limit must be a positive number")
			return
		}
		if v > maxSettlementsLimit {
			v = maxSettlementsLimit
		}
		limit = v
	}

	tendril, err := d.tendrilUsageWindow(r.Context(), userID, time.Time{}, nil)
	if err != nil {
		log.Printf("usage settlements (tendril): %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Ask for the full limit from each source, then merge and truncate: either
	// source alone could legitimately fill the whole page.
	x402, err := d.Store.ListSettlements(r.Context(), userID, limit)
	if err != nil {
		log.Printf("usage settlements (x402): %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	type settlement struct {
		ts  time.Time
		row map[string]any
	}
	merged := make([]settlement, 0, len(x402)+len(tendril.Topups))

	for _, s := range x402 {
		merged = append(merged, settlement{ts: s.Ts, row: map[string]any{
			"ts":         s.Ts.UTC().Format(time.RFC3339),
			"endpoint":   s.Endpoint,
			"amountAlgo": usd(s.USDMicros),
			// A settlement whose tx id never reached us is still a real debit,
			// so it reports with an empty id rather than being dropped — the
			// same call the client-side version had to make (see the frontend's
			// old recordSettlements dedup note).
			"txId":        s.TxID,
			"explorerURL": s.ExplorerURL,
			"workflowId":  s.WorkflowID,
		}})
	}

	for _, t := range tendril.Topups {
		merged = append(merged, settlement{ts: t.CreatedAt, row: map[string]any{
			"ts":          t.CreatedAt.UTC().Format(time.RFC3339),
			"endpoint":    "Tendril credit top-up",
			"amountAlgo":  usd(t.AmountUSDMicros),
			"txId":        t.TxID,
			"explorerURL": nodes.ExplorerURLForAsset(d.USDCAssetID, t.TxID),
			"workflowId":  "",
		}})
	}

	sort.Slice(merged, func(i, j int) bool {
		return merged[i].ts.After(merged[j].ts)
	})

	out := make([]map[string]any, 0, limit)
	for _, m := range merged {
		if len(out) >= limit {
			break
		}
		out = append(out, m.row)
	}
	respond.JSON(w, http.StatusOK, out)
}
