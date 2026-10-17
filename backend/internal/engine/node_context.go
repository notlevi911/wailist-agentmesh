package engine

import (
	"sort"

	"github.com/agentmesh/backend/internal/models"
)

// nodeRunContext is the run context a single node executes against (#68).
//
// It shares everything with the run's RunContext -- outputs, Set, Get, state,
// the trigger input -- except which output counts as "the message":
// Message() and LastOutput() resolve this node's own flow-edge predecessors
// instead of whichever node anywhere in the run happened to call Set last.
//
// The runner executes every node of a topology level in its own goroutine, so
// "last Set" meant goroutine scheduling decided what a node received: a node
// fed by two parallel branches could get either branch's output on different
// runs of the same workflow, and even a node with a single predecessor could
// receive the output of an unrelated sibling branch that finished later.
//
// Because every connector reaches the run context through nodes.RunContexter,
// handing each node this view changes what all of them read without touching
// any connector code.
type nodeRunContext struct {
	*RunContext
	// preds are this node's flow-edge predecessors, highest precedence
	// first -- see messagePredecessors.
	preds []string
}

// forNode returns the view one node with the given predecessors executes
// against. Writes (Set, state) go straight to rc, so every other node sees
// them exactly as before.
func (rc *RunContext) forNode(preds []string) *nodeRunContext {
	return &nodeRunContext{RunContext: rc, preds: preds}
}

// Message returns this node's input message: LastOutput flattened to a
// string, exactly as RunContext.Message flattens RunContext.LastOutput.
//
// LOAD-BEARING FOR PAYMENTS: see RunContext.Message. This is the value a
// node actually sends -- the body of a paid x402 call, Tendril's Python
// source -- so the selection rule below decides what real money is spent on.
func (n *nodeRunContext) Message() string {
	return anyToString(n.LastOutput())
}

// LastOutput returns the raw output of this node's highest-precedence flow
// predecessor that has produced one. When none has -- a trigger, a node wired
// only through {{ node.<id> }} references, or predecessors that were never
// run -- it falls back to the run-wide rule (the most recently Set output, or
// the trigger input), so such nodes behave exactly as they did before.
func (n *nodeRunContext) LastOutput() any {
	n.mu.RLock()
	defer n.mu.RUnlock()
	for _, id := range n.preds {
		if v, ok := n.outputs[id]; ok {
			return v
		}
	}
	// Inlined rather than calling RunContext.LastOutput, which would take
	// the read lock a second time while it is already held.
	if len(n.order) == 0 {
		return n.input
	}
	return n.outputs[n.order[len(n.order)-1]]
}

// messagePredecessors maps each node to its flow-edge predecessors in the
// order nodeRunContext consults them:
//
//  1. Deepest topology level first. Levels run one after another, so this is
//     the predecessor that finished most recently -- the same answer "last
//     Set" always gave for a plain chain, and for a node fed from several
//     depths (a→c and a→b→c), still b.
//  2. Within one level -- predecessors that ran concurrently, where "last
//     Set" was a race -- the predecessor whose flow edge comes later in the
//     workflow's saved edge list wins. The editor appends edges as they are
//     drawn, so this is the most recently connected input, and it is the same
//     on every run.
//
// Attach edges are ignored (attached tools and providers are an agent's
// resources, never steps that Set output), as are self-loops, duplicate edges
// and edges from nodes that are not in levels.
func messagePredecessors(levels [][]models.WorkflowNode, edges []models.WorkflowEdge) map[string][]string {
	levelOf := make(map[string]int)
	for i, level := range levels {
		for _, n := range level {
			levelOf[n.ID] = i
		}
	}

	preds := make(map[string][]string)
	seen := make(map[[2]string]bool)
	// Walk edges newest-first, so within a level the stable sort below keeps
	// later edges ahead of earlier ones.
	for i := len(edges) - 1; i >= 0; i-- {
		e := edges[i]
		if e.Kind != models.EdgeKindFlow || e.From == e.To {
			continue
		}
		if _, ok := levelOf[e.From]; !ok {
			continue
		}
		key := [2]string{e.From, e.To}
		if seen[key] {
			continue
		}
		seen[key] = true
		preds[e.To] = append(preds[e.To], e.From)
	}

	for _, p := range preds {
		sort.SliceStable(p, func(a, b int) bool { return levelOf[p[a]] > levelOf[p[b]] })
	}
	return preds
}
