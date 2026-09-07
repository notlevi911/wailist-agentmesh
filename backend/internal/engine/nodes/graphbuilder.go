package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/agentmesh/backend/internal/models"
)

var graphNodeTypes = map[string]bool{
	"trigger": true, "agent": true, "provider": true, "tool": true,
	"tool402": true, "action": true, "end": true, "tendril": true,
}

var graphEdgeKinds = map[string]bool{"flow": true, "attach": true}

// nodeFieldSetters maps the config keys a build-mode tool call may set onto
// the corresponding WorkflowNode string field. Deliberately excludes
// DiscoveredParams/CustomParams/Secrets/Config/tendril* fields -- those are
// advanced per-node authoring surfaces out of scope for a first cut of
// chat-built graphs.
var nodeFieldSetters = map[string]func(n *models.WorkflowNode, v string){
	"systemPrompt":  func(n *models.WorkflowNode, v string) { n.SystemPrompt = v },
	"model":         func(n *models.WorkflowNode, v string) { n.Model = v },
	"keyMode":       func(n *models.WorkflowNode, v string) { n.KeyMode = v },
	"apiKey":        func(n *models.WorkflowNode, v string) { n.APIKey = v },
	"url":           func(n *models.WorkflowNode, v string) { n.URL = v },
	"method":        func(n *models.WorkflowNode, v string) { n.Method = v },
	"endpoint":      func(n *models.WorkflowNode, v string) { n.Endpoint = v },
	"price":         func(n *models.WorkflowNode, v string) { n.Price = v },
	"unit":          func(n *models.WorkflowNode, v string) { n.Unit = v },
	"provider":      func(n *models.WorkflowNode, v string) { n.Provider = v },
	"description":   func(n *models.WorkflowNode, v string) { n.Description = v },
	"emailTo":       func(n *models.WorkflowNode, v string) { n.EmailTo = v },
	"emailFrom":     func(n *models.WorkflowNode, v string) { n.EmailFrom = v },
	"emailSubject":  func(n *models.WorkflowNode, v string) { n.EmailSubject = v },
	"emailBody":     func(n *models.WorkflowNode, v string) { n.EmailBody = v },
	"emailProvider": func(n *models.WorkflowNode, v string) { n.EmailProvider = v },
}

func newGraphID(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
}

// applyGraphOp mutates graph in place per a single tool call the build-mode
// meta-agent requested, and returns a short human-readable result string fed
// back to the model as the tool's functionResponse.
func applyGraphOp(graph *models.WorkflowGraph, funcName string, args map[string]any) (string, error) {
	switch funcName {
	case "add_node":
		return addGraphNode(graph, args)
	case "update_node":
		return updateGraphNode(graph, args)
	case "remove_node":
		return removeGraphNode(graph, args)
	case "add_edge":
		return addGraphEdge(graph, args)
	case "remove_edge":
		return removeGraphEdge(graph, args)
	default:
		return "", fmt.Errorf("unknown graph tool %q", funcName)
	}
}

func argString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func argFields(args map[string]any) (map[string]string, error) {
	out := map[string]string{}
	raw, ok := args["fields"].(map[string]any)
	if !ok {
		return out, nil
	}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		} else {
			return nil, fmt.Errorf("field %q must be a string, got %T", k, v)
		}
	}
	return out, nil
}

func addGraphNode(graph *models.WorkflowGraph, args map[string]any) (string, error) {
	nodeType := argString(args, "type")
	if !graphNodeTypes[nodeType] {
		return "", fmt.Errorf("add_node: invalid type %q", nodeType)
	}
	fields, err := argFields(args)
	if err != nil {
		return "", err
	}
	id := newGraphID("n_")
	node := models.WorkflowNode{
		ID:       id,
		Type:     models.NodeType(nodeType),
		Template: argString(args, "template"),
		Name:     argString(args, "name"),
		X:        80 + 240*float64(len(graph.Nodes)%4),
		Y:        120 + 160*float64(len(graph.Nodes)/4),
	}
	for k, v := range fields {
		if set, ok := nodeFieldSetters[k]; ok {
			set(&node, v)
		}
	}
	// Default a new Provider node to platform-key mode unless the model
	// explicitly chose "byok" -- resolveAPIKey (provider.go) treats any
	// KeyMode other than "platform" as BYOK and reads node.APIKey, which a
	// chat-built node never has. Left to the model's own judgment (via the
	// fieldsSchema description alone) this defaulted to "" == BYOK in
	// practice, so every chat-built agent needed a manual Inspector trip
	// before it could run at all. Deterministic here rather than relying on
	// prompt compliance -- this must hold every time, not most of the time.
	if node.Type == models.NodeTypeProvider && node.KeyMode == "" {
		node.KeyMode = "platform"
	}
	graph.Nodes = append(graph.Nodes, node)
	return fmt.Sprintf("added node %s (%s/%s)", id, nodeType, node.Template), nil
}

func updateGraphNode(graph *models.WorkflowGraph, args map[string]any) (string, error) {
	id := argString(args, "id")
	for i := range graph.Nodes {
		if graph.Nodes[i].ID != id {
			continue
		}
		if name := argString(args, "name"); name != "" {
			graph.Nodes[i].Name = name
		}
		if template := argString(args, "template"); template != "" {
			graph.Nodes[i].Template = template
		}
		fields, err := argFields(args)
		if err != nil {
			return "", err
		}
		for k, v := range fields {
			if set, ok := nodeFieldSetters[k]; ok {
				set(&graph.Nodes[i], v)
			}
		}
		return fmt.Sprintf("updated node %s", id), nil
	}
	return "", fmt.Errorf("update_node: node %q not found", id)
}

func removeGraphNode(graph *models.WorkflowGraph, args map[string]any) (string, error) {
	id := argString(args, "id")
	found := false
	nodes := graph.Nodes[:0]
	for _, n := range graph.Nodes {
		if n.ID == id {
			found = true
			continue
		}
		nodes = append(nodes, n)
	}
	if !found {
		return "", fmt.Errorf("remove_node: node %q not found", id)
	}
	graph.Nodes = nodes
	edges := graph.Edges[:0]
	for _, e := range graph.Edges {
		if e.From == id || e.To == id {
			continue
		}
		edges = append(edges, e)
	}
	graph.Edges = edges
	return fmt.Sprintf("removed node %s", id), nil
}

func addGraphEdge(graph *models.WorkflowGraph, args map[string]any) (string, error) {
	from := argString(args, "from")
	to := argString(args, "to")
	kind := argString(args, "kind")
	if kind == "" {
		kind = "flow"
	}
	if !graphEdgeKinds[kind] {
		return "", fmt.Errorf("add_edge: invalid kind %q", kind)
	}
	if !graphHasNode(graph, from) {
		return "", fmt.Errorf("add_edge: node %q not found", from)
	}
	if !graphHasNode(graph, to) {
		return "", fmt.Errorf("add_edge: node %q not found", to)
	}
	edge := models.WorkflowEdge{
		ID:     newGraphID("e_"),
		From:   from,
		To:     to,
		Kind:   models.EdgeKind(kind),
		ToPort: argString(args, "toPort"),
	}
	graph.Edges = append(graph.Edges, edge)
	return fmt.Sprintf("added edge %s (%s -> %s)", edge.ID, from, to), nil
}

func removeGraphEdge(graph *models.WorkflowGraph, args map[string]any) (string, error) {
	id := argString(args, "id")
	found := false
	edges := graph.Edges[:0]
	for _, e := range graph.Edges {
		if e.ID == id {
			found = true
			continue
		}
		edges = append(edges, e)
	}
	if !found {
		return "", fmt.Errorf("remove_edge: edge %q not found", id)
	}
	graph.Edges = edges
	return fmt.Sprintf("removed edge %s", id), nil
}

func graphHasNode(graph *models.WorkflowGraph, id string) bool {
	for _, n := range graph.Nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

func graphToolDecls() []funcDecl {
	// The legal keys are enumerated structurally from nodeFieldSetters rather
	// than listed in prose: an OBJECT schema with no declared properties is
	// rejected by some Gemini schema validators, and all five declarations go
	// up in one tools array, so a rejection here would fail every build call.
	fieldProperties := make(map[string]any, len(nodeFieldSetters))
	for k := range nodeFieldSetters {
		fieldProperties[k] = map[string]any{"type": "string"}
	}
	fieldsSchema := map[string]any{
		"type":        "OBJECT",
		"description": "Extra node fields, all optional strings. keyMode is either \"byok\" or \"platform\" -- a new Provider node defaults to \"platform\" already, only set this to \"byok\" if the user specifically asks to use their own API key.",
		"properties":  fieldProperties,
	}
	return []funcDecl{
		{
			Name:        "add_node",
			Description: "Add a new node to the workflow canvas.",
			Parameters: map[string]any{
				"type": "OBJECT",
				"properties": map[string]any{
					"type":     map[string]any{"type": "string", "description": "One of: trigger, agent, provider, tool, tool402, action, end, tendril."},
					"template": map[string]any{"type": "string", "description": "Template id within the type, e.g. \"chat\" for trigger, \"gemini\" for provider, \"agent\" for agent, \"email\" for action."},
					"name":     map[string]any{"type": "string", "description": "Display name for the node."},
					"fields":   fieldsSchema,
				},
				"required": []string{"type", "template"},
			},
		},
		{
			Name:        "update_node",
			Description: "Update fields on an existing node.",
			Parameters: map[string]any{
				"type": "OBJECT",
				"properties": map[string]any{
					"id":       map[string]any{"type": "string", "description": "Node id to update."},
					"name":     map[string]any{"type": "string"},
					"template": map[string]any{"type": "string"},
					"fields":   fieldsSchema,
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "remove_node",
			Description: "Remove a node and any edges connected to it.",
			Parameters: map[string]any{
				"type":       "OBJECT",
				"properties": map[string]any{"id": map[string]any{"type": "string"}},
				"required":   []string{"id"},
			},
		},
		{
			Name:        "add_edge",
			Description: "Connect two nodes. Use kind=\"attach\" with toPort=\"model\" or \"tools\" to attach a provider or tool to an agent; otherwise use kind=\"flow\" for the main execution path.",
			Parameters: map[string]any{
				"type": "OBJECT",
				"properties": map[string]any{
					"from":   map[string]any{"type": "string"},
					"to":     map[string]any{"type": "string"},
					"kind":   map[string]any{"type": "string", "description": "flow or attach"},
					"toPort": map[string]any{"type": "string", "description": "model or tools, only for kind=attach"},
				},
				"required": []string{"from", "to"},
			},
		},
		{
			Name:        "remove_edge",
			Description: "Remove an edge by id.",
			Parameters: map[string]any{
				"type":       "OBJECT",
				"properties": map[string]any{"id": map[string]any{"type": "string"}},
				"required":   []string{"id"},
			},
		},
	}
}

const buildAgentModel = "gemini-2.5-flash"

const buildSystemPrompt = `You are the workflow builder for AgentMesh, a visual agent-workflow canvas.
You edit a workflow graph by calling the add_node, update_node, remove_node, add_edge, and remove_edge tools.
Node types and their templates:
- trigger: manual, chat, webhook, cron
- agent: agent, router, human
- provider: gemini, openai, anthropic, mistral, groq (attach to an agent's "model" port via an attach edge)
- tool: http, calc, datetime, websearch (attach to an agent's "tools" port via an attach edge). websearch answers
  a query grounded in a live Google Search via the platform's own Gemini key -- use it for anything needing
  current/real-world information, regardless of which provider the agent itself uses.
- tool402: no preset templates -- every x402 tool is a real, live endpoint the workflow owner supplies (a
  fictitious provider name like "tavily" or "firecrawl" is NOT wired to anything real). Only add one when the
  user gives you an actual endpoint URL; set node fields url/endpoint accordingly and pick a short descriptive
  template label. Prefer websearch for search unless the user specifically wants a paid x402 data source.
- action: email, slack, db, discord, teams, google_chat, ntfy, telegram, github, notion, airtable, hubspot, trello, asana, clickup, jira, mailchimp, linear, todoist, gitlab, sentry, supabase, woocommerce, elevenlabs
- end: http, done
- tendril: tendril_topup, tendril_rent, tendril_run, tendril_release

A typical workflow: a trigger node, connected via a flow edge to an agent node, with a provider node attached to
the agent's "model" port and zero or more tool/tool402 nodes attached to its "tools" port, flowing on to an
action or end node. Make small, sensible workflows unless asked for something more elaborate. When you are done
making changes, reply with a short plain-text summary of what you built or changed -- do not call any more tools
once you're done.`

// BuildGraphResult is what a build-mode chat turn resolves to: a
// user-facing summary plus the graph after every requested tool call has
// been applied.
type BuildGraphResult struct {
	Reply string
	Graph models.WorkflowGraph
}

// BuildGraph runs a bounded tool-calling loop against the Gemini Flash
// meta-agent, letting it edit graph via the 5 graph_tool_decls tools until
// it responds with plain text instead of a function call (or the iteration
// cap is hit). graph should already be masked by the caller -- see
// handlers.BuildWorkflow's doc comment for why.
func BuildGraph(ctx context.Context, apiKey, userMessage string, graph models.WorkflowGraph) (BuildGraphResult, error) {
	apiURL := fmt.Sprintf("%s/v1beta/models/%s:generateContent", geminiBaseURL, buildAgentModel)
	apiHeaders := map[string]string{"x-goog-api-key": apiKey}

	graphJSON, _ := json.Marshal(graph)
	contents := []map[string]any{
		{"role": "user", "parts": []map[string]any{{"text": fmt.Sprintf("Current graph:\n%s\n\nRequest: %s", graphJSON, userMessage)}}},
	}
	payload := map[string]any{
		"contents": contents,
		"systemInstruction": map[string]any{
			"parts": []map[string]string{{"text": buildSystemPrompt}},
		},
		"tools": []map[string]any{{"functionDeclarations": graphToolDecls()}},
	}

	for iter := 0; iter < maxToolIterations; iter++ {
		resp, err := postLLMJSON(ctx, apiURL, apiHeaders, payload)
		if err != nil {
			return BuildGraphResult{}, err
		}
		calls := extractGeminiFunctionCalls(resp)
		if len(calls) == 0 {
			text, err := extractGeminiText(resp)
			if err != nil {
				return BuildGraphResult{}, err
			}
			return BuildGraphResult{Reply: text, Graph: graph}, nil
		}

		modelParts := make([]map[string]any, len(calls))
		for i, c := range calls {
			modelParts[i] = map[string]any{"functionCall": map[string]any{"name": c.name, "args": c.args}}
		}
		contents = append(contents, map[string]any{"role": "model", "parts": modelParts})

		responseParts := make([]map[string]any, 0, len(calls))
		for _, c := range calls {
			result, err := applyGraphOp(&graph, c.name, c.args)
			if err != nil {
				result = "error: " + err.Error()
			}
			responseParts = append(responseParts, map[string]any{
				"functionResponse": map[string]any{
					"name":     c.name,
					"response": map[string]any{"result": result},
				},
			})
		}
		contents = append(contents, map[string]any{"role": "user", "parts": responseParts})
		payload["contents"] = contents
	}

	return BuildGraphResult{}, fmt.Errorf("workflow builder exceeded maximum tool call iterations (%d)", maxToolIterations)
}
