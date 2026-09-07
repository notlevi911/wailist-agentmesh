package engine

import (
	"encoding/json"
	"sync"
)

type RunContext struct {
	mu      sync.RWMutex
	outputs map[string]any
	// order records node IDs in the sequence they were Set. Message() reads
	// the tail of this rather than ranging over `outputs` — Go randomizes map
	// iteration order, so the old "most recent" was actually "an arbitrary
	// one", non-deterministic across runs of the same workflow. A re-Set
	// moves the node to the tail (see Set below), so "most recent" stays
	// accurate even for a node that runs more than once.
	order []string
	input any
	runID string
	// state is the workflow's persisted variables, loaded once at run
	// start. Reads during the run see this snapshot; a state node's write
	// updates both the database and this map so a later node in the same
	// run sees its own write.
	state map[string]any
}

func NewRunContext(runID string, inputJSON []byte) *RunContext {
	var input any
	if len(inputJSON) > 0 {
		json.Unmarshal(inputJSON, &input)
	}
	return &RunContext{
		outputs: make(map[string]any),
		input:   input,
		runID:   runID,
	}
}

func (rc *RunContext) Set(nodeID string, value any) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	// A re-Set moves the node to the tail: it just produced the newest output.
	for i, id := range rc.order {
		if id == nodeID {
			rc.order = append(rc.order[:i], rc.order[i+1:]...)
			break
		}
	}
	rc.order = append(rc.order, nodeID)
	rc.outputs[nodeID] = value
}

// OutputOrder returns node IDs in the order their outputs were Set, oldest
// first. The returned slice is a copy — callers may retain it safely.
func (rc *RunContext) OutputOrder() []string {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	out := make([]string, len(rc.order))
	copy(out, rc.order)
	return out
}

func (rc *RunContext) Get(nodeID string) (any, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	v, ok := rc.outputs[nodeID]
	return v, ok
}

// UserInput returns the original trigger input — always the human's message.
func (rc *RunContext) UserInput() string {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return anyToString(rc.input)
}

// ToolOutputs returns all outputs keyed by nodeID, excluding the trigger output.
func (rc *RunContext) ToolOutputs() map[string]any {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	out := make(map[string]any, len(rc.outputs))
	for k, v := range rc.outputs {
		out[k] = v
	}
	return out
}

// Message returns the most recent node output as a string, falling back to the
// trigger input when nothing has run yet.
//
// LOAD-BEARING FOR PAYMENTS: this value is the request body sent to paid x402
// endpoints (nodes/tool402.go) and the Python source sent to Tendril's metered
// /x402/run (nodes/tendril.go). Changing which output it selects changes what
// real money is spent on. Do not alter the selection rule.
//
// KNOWN ISSUE: "most recent" means last call to Set(), and runner.go runs
// every node in the same topological level concurrently in its own
// goroutine (see the wg.Add/go func loop in Run) -- so when two sibling
// nodes in the same level both call Set(), which one's output Message()/
// LastOutput() returns is decided by goroutine scheduling, not by the
// workflow graph. A downstream node fed by two parallel upstream branches
// can see either branch's output on any given run of the identical
// workflow. Fixing this properly means Message()/LastOutput() resolving
// the CALLING node's actual flow-edge predecessor rather than "whatever was
// set last" -- which needs the predecessor's node ID threaded through
// RunContexter into every one of the connector call sites that read
// Message()/LastOutput() (20+ across nodes/connectors_*.go), not a change
// local to this file. Left undone here; only genuinely single-predecessor
// chains (the common case in practice) are unaffected.
func (rc *RunContext) Message() string {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if len(rc.order) == 0 {
		return anyToString(rc.input)
	}
	return anyToString(rc.outputs[rc.order[len(rc.order)-1]])
}

// LastOutput returns the most recent output's raw value, unlike Message()
// which always flattens it to a string. A connector's message template
// (see nodes.expandTemplate) needs the real structure to pick one field out
// of it -- e.g. {{ result.extract }} against a JSON API response -- which a
// pre-stringified value can't offer.
func (rc *RunContext) LastOutput() any {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if len(rc.order) == 0 {
		return rc.input
	}
	return rc.outputs[rc.order[len(rc.order)-1]]
}

func anyToString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if m, ok := v.(map[string]any); ok {
		if msg, ok := m["message"].(string); ok {
			return msg
		}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// SetState installs the workflow's persisted variables for this run.
func (rc *RunContext) SetState(state map[string]any) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.state = state
}

// State returns a copy of the run's view of the workflow's variables.
// A copy, not the map itself, so a node cannot mutate shared state
// without going through the store.
func (rc *RunContext) State() map[string]any {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	out := make(map[string]any, len(rc.state))
	for k, v := range rc.state {
		out[k] = v
	}
	return out
}

// setStateKey updates one key in the run's snapshot after a successful
// write, so a later node in the same run reads the value this run wrote.
func (rc *RunContext) setStateKey(key string, value any) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.state == nil {
		rc.state = make(map[string]any)
	}
	rc.state[key] = value
}
