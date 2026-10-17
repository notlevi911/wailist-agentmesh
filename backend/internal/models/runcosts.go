package models

// RunCosts is what one run has been charged, per step and in total, read from
// its debit_ledger rows (#111). It lets the run log show a cost for every
// billable step -- an agent's platform-key fee, a connector's flat fee, an
// x402 call's platform fee and relay cost -- through one mechanism, instead of
// only the x402 steps whose output happens to carry a payment receipt.
//
// Only charges are counted. debit_ledger rows are written when a charge is
// committed, so a balance held by a reservation that was later released never
// appears, and a run that failed partway shows exactly what it was charged
// before it failed.
type RunCosts struct {
	TotalUSDMicros int64      `json:"totalUsdMicros"`
	Steps          []StepCost `json:"steps"`
}

// StepCost is one node's share of a run's charges. A node can be charged more
// than once -- a paid x402 call writes both an x402_relay_cost and an
// x402_platform_fee row -- so ByKind breaks the total down by debit kind.
type StepCost struct {
	NodeID         string           `json:"nodeId"`
	TotalUSDMicros int64            `json:"totalUsdMicros"`
	ByKind         map[string]int64 `json:"byKind"`
}

// SummarizeRunCosts folds a run's debit ledger entries into per-step and total
// costs. Steps are ordered by each node's first charge, matching the ledger's
// created_at order. Steps is never nil, so it encodes as [] for a run that was
// charged nothing.
func SummarizeRunCosts(entries []DebitEntry) RunCosts {
	costs := RunCosts{Steps: []StepCost{}}
	index := make(map[string]int)
	for _, e := range entries {
		i, ok := index[e.NodeID]
		if !ok {
			i = len(costs.Steps)
			index[e.NodeID] = i
			costs.Steps = append(costs.Steps, StepCost{NodeID: e.NodeID, ByKind: map[string]int64{}})
		}
		costs.Steps[i].TotalUSDMicros += e.AmountUSDMicros
		costs.Steps[i].ByKind[e.Kind] += e.AmountUSDMicros
		costs.TotalUSDMicros += e.AmountUSDMicros
	}
	return costs
}
