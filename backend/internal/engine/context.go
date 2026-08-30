package engine

import (
	"encoding/json"
	"sync"
)

type RunContext struct {
	mu      sync.RWMutex
	outputs map[string]any
	input   any
	runID   string
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
	rc.outputs[nodeID] = value
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

// Message returns the most recent string output for use as LLM user message.
// Kept for backwards compatibility with non-agent nodes.
func (rc *RunContext) Message() string {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if len(rc.outputs) == 0 {
		return anyToString(rc.input)
	}
	var last any
	for _, v := range rc.outputs {
		last = v
	}
	return anyToString(last)
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
