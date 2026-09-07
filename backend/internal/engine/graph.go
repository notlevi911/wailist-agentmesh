package engine

import (
	"errors"
	"log"
	"regexp"
	"strings"

	"github.com/agentmesh/backend/internal/models"
)

// nodeRefPattern matches a complete "{{ node.<id> }}" or
// "{{ node.<id>.field.path }}" placeholder -- kept in sync by hand with
// nodes.templateRef/lookupRef's "node.<id>" / "node.<id>.field" forms
// (package nodes can't be imported from here: engine -> nodes -> engine
// would cycle, same constraint stubs.go documents for RunContexter). Scoped
// to extracting an ID, not resolving a value, so staying in sync just means
// matching the same {{ node.<id> ... }} shape, not duplicating the resolver.
// Requires the closing "}}", same as templateRef -- an unterminated
// "{{ node.n5" (a typo, or stray example text in a SystemPrompt/Description)
// would otherwise add a real implicit dependency edge for text templateRef
// itself would never treat as a reference at runtime, risking a spurious
// "cycle detected" failure over functionally inert text.
var nodeRefPattern = regexp.MustCompile(`\{\{\s*node\.([A-Za-z0-9_\-]+)(?:\.[A-Za-z0-9_.\-]+)?\s*\}\}`)

// templateEligibleStrings returns every string on n a connector might run
// through resolveTemplate -- see that function's doc comment (nodes/
// resolve.go) for the {{ node.<id> ... }} syntax this field list backs.
// Deliberately excludes Secrets/APIKey/EmailAPIKey/TendrilLeaseToken: those
// are credentials, never template text.
//
// Also deliberately excludes SystemPrompt, BodyTemplate, and Description,
// even though they're plain strings that could happen to contain
// "{{ node.<id> }}"-shaped text: none of them is ever passed through
// resolveTemplate at runtime. SystemPrompt goes to the LLM verbatim;
// Description goes into the function-call tool schema verbatim;
// BodyTemplate is expanded by expandBodyTemplate's own, unrelated
// {{param:x}}/{{file:x}} placeholder syntax, which doesn't understand
// "node.<id>" at all. Scanning them anyway would add a real implicit
// ordering edge -- and a real cycle-detection failure -- for what is, at
// runtime, functionally inert prose (e.g. a SystemPrompt that happens to
// describe or quote the templating syntax).
func templateEligibleStrings(n models.WorkflowNode) []string {
	out := []string{n.EmailBody}
	for _, v := range n.Config {
		out = append(out, v)
	}
	for _, v := range n.ParamDefaults {
		out = append(out, v)
	}
	for _, p := range n.CustomParams {
		out = append(out, p.Value)
	}
	return out
}

// referencedNodeIDs returns every node ID n's template-eligible fields
// reference via a {{ node.<id> ... }} placeholder.
func referencedNodeIDs(n models.WorkflowNode) []string {
	var ids []string
	for _, s := range templateEligibleStrings(n) {
		if s == "" || !strings.Contains(s, "{{") {
			continue
		}
		for _, m := range nodeRefPattern.FindAllStringSubmatch(s, -1) {
			ids = append(ids, m[1])
		}
	}
	return ids
}

// TopologicalSort returns nodes grouped into parallel execution levels using Kahn's algorithm.
// Only flow edges, plus the implicit dependencies below, determine order; attach edges are ignored.
func TopologicalSort(nodes []models.WorkflowNode, edges []models.WorkflowEdge) ([][]models.WorkflowNode, error) {
	nodeMap := make(map[string]models.WorkflowNode, len(nodes))
	inDegree := make(map[string]int, len(nodes))
	successors := make(map[string][]string)
	// seenDep dedupes (from,to) pairs across real flow edges and the
	// implicit template-ref dependencies added below, so a pair present in
	// both never double-increments inDegree.
	seenDep := make(map[[2]string]bool)

	for _, n := range nodes {
		nodeMap[n.ID] = n
		inDegree[n.ID] = 0
	}

	addDep := func(from, to string) {
		if from == to {
			return
		}
		if _, ok := nodeMap[from]; !ok {
			return
		}
		if _, ok := nodeMap[to]; !ok {
			return
		}
		key := [2]string{from, to}
		if seenDep[key] {
			return
		}
		seenDep[key] = true
		successors[from] = append(successors[from], to)
		inDegree[to]++
	}

	for _, e := range edges {
		if e.Kind != models.EdgeKindFlow {
			continue
		}
		addDep(e.From, e.To)
	}

	// A {{ node.<id> }} template ref makes the referencing node depend on
	// the referenced node's output already existing, even with no flow
	// edge between them -- without this, two nodes with no flow edge (e.g.
	// unrelated branches that happen to land in the same level) can run
	// concurrently, and the ref may or may not have a value yet depending
	// on goroutine scheduling. See nodes/resolve.go's resolveTemplate doc
	// comment for the full race description; this is the fix for it.
	for _, n := range nodes {
		for _, refID := range referencedNodeIDs(n) {
			addDep(refID, n.ID)
		}
	}

	queue := make([]string, 0)
	for _, n := range nodes {
		if inDegree[n.ID] == 0 {
			queue = append(queue, n.ID)
		}
	}

	var levels [][]models.WorkflowNode
	visited := 0

	for len(queue) > 0 {
		level := make([]models.WorkflowNode, 0, len(queue))
		next := make([]string, 0)
		for _, id := range queue {
			level = append(level, nodeMap[id])
			visited++
			for _, succ := range successors[id] {
				inDegree[succ]--
				if inDegree[succ] == 0 {
					next = append(next, succ)
				}
			}
		}
		levels = append(levels, level)
		queue = next
	}

	if visited != len(nodes) {
		return nil, errors.New("cycle detected in workflow graph")
	}
	return levels, nil
}

// BuildAttachMap maps each agent node ID to its attached provider and tools.
func BuildAttachMap(nodes []models.WorkflowNode, edges []models.WorkflowEdge) map[string]models.AttachConfig {
	nodeMap := make(map[string]models.WorkflowNode, len(nodes))
	for _, n := range nodes {
		nodeMap[n.ID] = n
	}

	result := make(map[string]models.AttachConfig)
	for _, e := range edges {
		if e.Kind != models.EdgeKindAttach {
			continue
		}
		cfg := result[e.To]
		src, ok := nodeMap[e.From]
		if !ok {
			continue
		}
		switch e.ToPort {
		case "model":
			// Only a real Provider node has meaningful model-attach
			// semantics -- guards the same class of gap as "tools" below.
			if src.Type == models.NodeTypeProvider {
				s := src
				cfg.Provider = &s
			} else {
				log.Printf("engine: dropping model-attach edge %s->%s: source node type %q is not a Provider", e.From, e.To, src.Type)
			}
		case "tools":
			// Only tool/tool402 nodes have real dispatch as an
			// agent-attached tool (executeFunctionCall in provider.go
			// special-cases tool402 and falls back to ExecuteTool, which
			// understands tool's own templates, for everything else).
			// Any other type reaching here -- e.g. a Google node, which
			// the frontend deliberately never offers as an attach source --
			// would silently no-op in ExecuteTool's default case while
			// still being billed as a successful call. The frontend
			// already blocks wiring this client-side; this is the
			// server-side backstop for a hand-crafted or future-regressed
			// edge that bypasses it (UpdateWorkflow persists edges from
			// request JSON with no server-side edge-kind/node-type check).
			if src.Type == models.NodeTypeTool || src.Type == models.NodeTypeTool402 {
				cfg.Tools = append(cfg.Tools, src)
			} else {
				log.Printf("engine: dropping tools-attach edge %s->%s: source node type %q is not a Tool/Tool402", e.From, e.To, src.Type)
			}
		}
		result[e.To] = cfg
	}
	return result
}
