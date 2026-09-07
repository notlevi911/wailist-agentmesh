package engine

import (
	"testing"

	"github.com/agentmesh/backend/internal/models"
)

// TestPrependRunFundingReceipt pins the reporting row for a run-level
// pre-fund. Without it, the single real inbound settlement a run-funded
// agent makes (Wallet 1 -> Wallet 2, the whole run's tool budget) had no
// node of its own and never reached the console or the DB run_logs at all --
// only the per-call receipts did, and those carry a slice of the amount
// each. It must come FIRST: consumers de-duplicate by tx id (every per-call
// receipt repeats this same one), so first place is what makes the accurate
// full amount the row that survives.
func TestPrependRunFundingReceipt(t *testing.T) {
	perCall := map[string]any{"nodeId": "x1", "settledUsdMicros": int64(20000), "txId": "FUNDTX"}
	result := map[string]any{"x402Payments": []map[string]any{perCall}}
	rf := runFundResult{FundingTxID: "FUNDTX", FundedUSDMicros: 50000}
	node := models.WorkflowNode{ID: "agent1", Name: "Staking Agent"}

	prependRunFundingReceipt(result, rf, node, 31566704) // mainnet USDC

	payments, ok := result["x402Payments"].([]map[string]any)
	if !ok {
		t.Fatalf("want []map[string]any (the type Run's publish loop asserts on), got %T", result["x402Payments"])
	}
	if len(payments) != 2 {
		t.Fatalf("want the funding receipt added alongside the per-call one, got %d entries", len(payments))
	}
	if payments[1]["nodeId"] != "x1" {
		t.Fatalf("want the existing per-call receipt kept after the funding row, got %v", payments[1])
	}
	got := payments[0]
	if got["txId"] != "FUNDTX" {
		t.Fatalf("want the funding tx id first, got %v", got["txId"])
	}
	if got["settledUsdMicros"] != int64(50000) {
		t.Fatalf("want the full funded amount 50000, got %v", got["settledUsdMicros"])
	}
	if got["amount"] != "0.050000" {
		t.Fatalf("want a decimal USDC amount the console can parseFloat, got %v", got["amount"])
	}
	if got["explorerURL"] != "https://lora.algokit.io/mainnet/transaction/FUNDTX" {
		t.Fatalf("want a mainnet explorer link for the mainnet USDC asset id, got %v", got["explorerURL"])
	}
	if got["nodeId"] != "agent1" {
		t.Fatalf("want the funding attributed to the agent node that triggered it, got %v", got["nodeId"])
	}
	if got["debitKind"] != models.DebitKindX402RelayCost {
		t.Fatalf("want debitKind %q, got %v", models.DebitKindX402RelayCost, got["debitKind"])
	}
	if got["isFundingReceipt"] != true {
		t.Fatalf("want isFundingReceipt true so a tool-call counter can skip this row, got %v", got["isFundingReceipt"])
	}
	if payments[1]["isFundingReceipt"] != nil {
		t.Fatalf("want the real per-call receipt unmarked, got %v", payments[1]["isFundingReceipt"])
	}
}

// TestPrependRunFundingReceiptNoFunding covers the ordinary per-call relay
// path (and any agent with no attached v2 tools): no pre-fund happened, so
// nothing must be added to a result that may already carry real receipts.
func TestPrependRunFundingReceiptNoFunding(t *testing.T) {
	result := map[string]any{"x402Payments": []map[string]any{{"nodeId": "x1"}}}

	prependRunFundingReceipt(result, runFundResult{}, models.WorkflowNode{ID: "agent1"}, 31566704)

	payments, _ := result["x402Payments"].([]map[string]any)
	if len(payments) != 1 || payments[0]["nodeId"] != "x1" {
		t.Fatalf("want the payments list untouched when no pre-fund happened, got %v", payments)
	}
}

// TestPrependRunFundingReceiptNoPayments covers a run-funded agent whose
// tool was never actually called: the pre-fund's real settlement still has
// to be reported (the money moved on-chain regardless; only the unspent
// credit reservation is released).
func TestPrependRunFundingReceiptNoPayments(t *testing.T) {
	result := map[string]any{"message": "no tool needed"}

	prependRunFundingReceipt(result, runFundResult{FundingTxID: "FUNDTX", FundedUSDMicros: 50000}, models.WorkflowNode{ID: "agent1"}, 31566704)

	payments, ok := result["x402Payments"].([]map[string]any)
	if !ok || len(payments) != 1 || payments[0]["txId"] != "FUNDTX" {
		t.Fatalf("want the funding receipt reported even with no per-call payments, got %v", result["x402Payments"])
	}
}
