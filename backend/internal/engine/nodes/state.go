package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/agentmesh/backend/internal/models"
)

// StateStore is the slice of the database a state node touches. An
// interface so the node package stays free of a db dependency and the
// executor is testable with a fake.
type StateStore interface {
	SetWorkflowVariable(ctx context.Context, workflowID, key string, valueJSON []byte) error
	IncrementWorkflowVariable(ctx context.Context, workflowID, key string, delta float64) (float64, error)
	DeleteWorkflowVariable(ctx context.Context, workflowID, key string) error
}

// StateResult is what a state node returns, and what the next node sees as
// the run's most recent output.
type StateResult struct {
	Op    string `json:"op"`
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// ExecuteState runs a state node: read, write, increment or delete one
// workflow variable.
//
// A "get" returns the value directly (not wrapped) so it can flow straight
// into the next node's message — reading a stored cursor and handing it to
// an agent is the whole point. The mutating ops return a StateResult so
// the console shows what changed.
func ExecuteState(
	ctx context.Context,
	node models.WorkflowNode,
	workflowID string,
	store StateStore,
	rc RunContexter,
	state map[string]any,
) (any, error) {
	key := ExpandState(node.StateKey, state)
	if key == "" {
		return nil, fmt.Errorf("state node %s: stateKey is required", node.ID)
	}

	switch node.StateOp {
	case "get", "":
		// Absent key reads as nil rather than an error: "nothing stored
		// yet" is the normal first-run case for every incremental-sync
		// workflow this feature exists for.
		return state[key], nil

	case "set":
		// The stored value is the expanded literal, falling back to the
		// previous node's output when no literal is configured — so
		// "remember what the agent just produced" needs no expression.
		raw := ExpandState(node.StateValue, state)
		if node.StateValue == "" {
			raw = rc.Message()
		}
		// Store valid JSON as JSON (numbers stay numbers, objects stay
		// objects); store anything else as a JSON string.
		var probe any
		valueJSON := []byte(raw)
		if json.Unmarshal(valueJSON, &probe) != nil {
			valueJSON, _ = json.Marshal(raw)
			probe = raw
		}
		if err := store.SetWorkflowVariable(ctx, workflowID, key, valueJSON); err != nil {
			return nil, fmt.Errorf("state node %s: set %q: %w", node.ID, key, err)
		}
		return StateResult{Op: "set", Key: key, Value: probe}, nil

	case "increment":
		delta := 1.0
		if v := ExpandState(node.StateValue, state); v != "" {
			parsed, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil, fmt.Errorf("state node %s: increment delta %q is not a number: %w", node.ID, v, err)
			}
			delta = parsed
		}
		n, err := store.IncrementWorkflowVariable(ctx, workflowID, key, delta)
		if err != nil {
			return nil, fmt.Errorf("state node %s: increment %q: %w", node.ID, key, err)
		}
		return StateResult{Op: "increment", Key: key, Value: n}, nil

	case "delete":
		if err := store.DeleteWorkflowVariable(ctx, workflowID, key); err != nil {
			return nil, fmt.Errorf("state node %s: delete %q: %w", node.ID, key, err)
		}
		return StateResult{Op: "delete", Key: key}, nil

	default:
		return nil, fmt.Errorf("state node %s: unknown stateOp %q", node.ID, node.StateOp)
	}
}
