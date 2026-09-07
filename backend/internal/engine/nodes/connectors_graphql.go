package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/agentmesh/backend/internal/models"
)

// sendGraphQL POSTs a GraphQL query to any endpoint and returns the decoded
// response. Generic on purpose: Linear, GitHub v4, Shopify Storefront and
// Monday.com are all reachable through this without a bespoke connector.
func sendGraphQL(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	endpoint := resolveTemplate(configVal(node, "graphqlEndpoint", ""), rc)
	if endpoint == "" {
		return "graphql_skipped_no_endpoint", ErrActionSkipped
	}
	query := configVal(node, "graphqlQuery", "")
	if query == "" {
		return "graphql_skipped_no_query", ErrActionSkipped
	}

	payload := map[string]any{"query": query}
	if raw := configVal(node, "graphqlVariables", ""); raw != "" {
		var vars map[string]any
		if err := json.Unmarshal([]byte(raw), &vars); err != nil {
			return nil, fmt.Errorf("graphql: `graphqlVariables` is not a valid JSON object: %w", err)
		}
		// Only string values are template-expanded; numbers, booleans and
		// nested objects pass through untouched so their JSON types survive.
		for k, v := range vars {
			if s, ok := v.(string); ok {
				vars[k] = resolveTemplate(s, rc)
			}
		}
		payload["variables"] = vars
	}

	headers := map[string]string{}
	// Sent verbatim: Monday.com wants a bare token, GitHub wants "Bearer ...".
	// Prefixing here would break one of them.
	if auth := secretVal(node, "graphqlAuthHeader"); auth != "" {
		headers["Authorization"] = auth
	}

	req, err := newJSONRequest(ctx, http.MethodPost, endpoint, headers, payload)
	if err != nil {
		return nil, fmt.Errorf("graphql: %w", err)
	}
	out, err := doAndDecode(req, "GraphQL")
	if err != nil {
		return nil, err
	}
	if err := checkGraphQLErrors(out, "graphql"); err != nil {
		return nil, err
	}
	return out, nil
}

// checkGraphQLErrors reports a GraphQL "errors" array as a Go error. GraphQL
// APIs (this generic connector, and any other connector built on top of a
// GraphQL endpoint, e.g. Monday.com) report failures as HTTP 200 carrying an
// "errors" array rather than a non-2xx status -- returning that as success
// would render a green node for a query/mutation that did nothing.
// errPrefix matches each caller's own error-message convention (e.g.
// "graphql", "Monday.com").
func checkGraphQLErrors(out any, errPrefix string) error {
	body, ok := out.(map[string]any)
	if !ok {
		return nil
	}
	errs, ok := body["errors"].([]any)
	if !ok || len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%s: server returned errors: %s", errPrefix, graphQLErrorText(errs))
}

// graphQLErrorText joins the "message" field of each GraphQL error for the
// wrapped error string, falling back to the raw JSON for non-standard shapes.
func graphQLErrorText(errs []any) string {
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		if m, ok := e.(map[string]any); ok {
			if s, ok := m["message"].(string); ok {
				msgs = append(msgs, s)
				continue
			}
		}
		b, _ := json.Marshal(e)
		msgs = append(msgs, string(b))
	}
	return strings.Join(msgs, "; ")
}
