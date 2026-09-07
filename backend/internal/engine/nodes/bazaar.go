package nodes

// bazaarDeclaredMethod is the single source of truth for the HTTP method
// every one of this platform's Bazaar declarations advertises. It is written
// into BOTH halves of the declaration below -- info.input.method and the
// schema's method enum -- because the facilitator's catalog validator
// requires those two to agree exactly, not merely to overlap.
//
// This constant exists as the structural fix for a real, silent outage: the
// two hand-maintained copies of this extension (this file's ancestor in
// runfund.go and bazaarDiscoveryExtension in api/handlers/x402relay.go) both
// declared info.input.method "GET" while listing enum ["GET","HEAD","DELETE"]
// in the schema. The x402 Doctor's report named it precisely -- "bazaar.schema
// method enum must match the declared method" -- and a superset fails that
// check, so every real settlement through /x402/relay and both self-settle
// paths was rejected by the catalog validator despite verify and settle
// succeeding and real USDC moving. Deriving the enum from the same constant
// as the declared method makes that particular drift unrepresentable.
const bazaarDeclaredMethod = "GET"

// BazaarDeclaration describes the parts of a Bazaar discovery extension that
// genuinely differ between this platform's declaring routes. Everything else
// -- the method, the schema skeleton, the input type const -- is fixed by the
// facilitator's validator and is filled in by BazaarDiscoveryExtension.
type BazaarDeclaration struct {
	// RouteTemplate is the path the facilitator canonicalizes the resource
	// as (origin+RouteTemplate), so it MUST be the public, externally
	// reachable path -- the one behind our own branded domain's /api proxy
	// -- not a backend-internal route. A stale value here does not merely
	// mislabel the catalog entry, it names a URL that 404s.
	RouteTemplate string

	// QueryParams, when non-nil, declares the route's query parameters: the
	// map is emitted verbatim as info.input.queryParams and mirrored in the
	// schema as a plain object property. Left nil for routes that take no
	// query string at all, which omits the key from both halves rather than
	// declaring an empty object.
	QueryParams map[string]any

	// OutputExample, when non-nil, adds the optional info.output block (and
	// its matching schema property). Only `input` is in the schema's own
	// required list, so this is genuinely optional -- but every live,
	// successfully-cataloged entry pulled from the real facilitator has one,
	// so routes that can describe their response should. Nil omits output
	// from both halves.
	OutputExample map[string]any
}

// BazaarDiscoveryExtension builds the `extensions.bazaar` value for a
// PaymentRequirements / PaymentPayload -- the declaration that actually
// registers a route in the Bazaar catalog. extra.tag alone only attributes an
// already-discovered route's activity to the challenge; it does not register
// anything.
//
// The returned shape mirrors @x402/extensions' declareDiscoveryExtension, and
// the facilitator runs it through an ajv validator
// (extractDiscoveryInfo -> validateDiscoveryExtension) before it will build a
// catalog entry at all. Both `info` and `schema` are required siblings, and
// info.input.type must be set -- a declaration missing either is dropped
// silently, with verify and settle still reporting success.
//
// Callers pass only what differs per route (see BazaarDeclaration); the
// method and its enum come from bazaarDeclaredMethod so the two cannot drift.
//
// The facilitator only catalogs a route once it observes this on a real
// settlement, never from an informational 402 challenge alone.
func BazaarDiscoveryExtension(d BazaarDeclaration) map[string]any {
	input := map[string]any{"type": "http", "method": bazaarDeclaredMethod}
	inputSchemaProps := map[string]any{
		"type":   map[string]any{"type": "string", "const": "http"},
		"method": map[string]any{"type": "string", "enum": []string{bazaarDeclaredMethod}},
	}
	if d.QueryParams != nil {
		input["queryParams"] = d.QueryParams
		inputSchemaProps["queryParams"] = map[string]any{"type": "object"}
	}

	info := map[string]any{"input": input}
	schemaProps := map[string]any{
		"input": map[string]any{
			"type":                 "object",
			"properties":           inputSchemaProps,
			"required":             []string{"type", "method"},
			"additionalProperties": false,
		},
	}
	if d.OutputExample != nil {
		info["output"] = map[string]any{"type": "json", "example": d.OutputExample}
		schemaProps["output"] = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type":    map[string]any{"type": "string"},
				"example": map[string]any{"type": "object"},
			},
			"required": []string{"type"},
		}
	}

	return map[string]any{
		"bazaar": map[string]any{
			"routeTemplate": d.RouteTemplate,
			"info":          info,
			"schema": map[string]any{
				"$schema":    "https://json-schema.org/draft/2020-12/schema",
				"type":       "object",
				"properties": schemaProps,
				"required":   []string{"input"},
			},
		},
	}
}
