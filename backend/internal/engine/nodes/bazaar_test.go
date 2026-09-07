package nodes_test

import (
	"encoding/json"
	"testing"

	"github.com/agentmesh/backend/internal/engine/nodes"
)

// declaredMethodAndEnum digs the two halves the catalog validator compares
// out of a built extension: the method named in info.input.method, and the
// enum the schema allows for that same field.
func declaredMethodAndEnum(t *testing.T, ext map[string]any) (string, []any) {
	t.Helper()
	bazaar, ok := ext["bazaar"].(map[string]any)
	if !ok {
		t.Fatalf("want a bazaar key, got %+v", ext)
	}
	info, ok := bazaar["info"].(map[string]any)
	if !ok {
		t.Fatalf("want bazaar.info, got %+v", bazaar)
	}
	input, ok := info["input"].(map[string]any)
	if !ok {
		t.Fatalf("want bazaar.info.input, got %+v", info)
	}
	method, ok := input["method"].(string)
	if !ok {
		t.Fatalf("want bazaar.info.input.method to be a string, got %+v", input["method"])
	}

	schema, ok := bazaar["schema"].(map[string]any)
	if !ok {
		t.Fatalf("want bazaar.schema, got %+v", bazaar)
	}
	schemaProps, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("want bazaar.schema.properties, got %+v", schema)
	}
	inputSchema, ok := schemaProps["input"].(map[string]any)
	if !ok {
		t.Fatalf("want bazaar.schema.properties.input, got %+v", schemaProps)
	}
	inputProps, ok := inputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("want bazaar.schema.properties.input.properties, got %+v", inputSchema)
	}
	methodSchema, ok := inputProps["method"].(map[string]any)
	if !ok {
		t.Fatalf("want a method entry in the input schema, got %+v", inputProps)
	}
	// Round-trips through JSON so this reads the enum the way the
	// facilitator's own validator does -- off the encoded wire value, not
	// off whatever concrete Go slice type happened to build it.
	raw, err := json.Marshal(methodSchema["enum"])
	if err != nil {
		t.Fatalf("method enum is not JSON-encodable: %v", err)
	}
	var enum []any
	if err := json.Unmarshal(raw, &enum); err != nil {
		t.Fatalf("want method enum to encode as an array, got %s", raw)
	}
	return method, enum
}

// TestBazaarMethodEnumMatchesDeclaredMethod is a reproduce-then-fix
// regression test for a silent, money-losing outage. Two hand-maintained
// copies of this declaration each set info.input.method to "GET" while
// listing enum ["GET","HEAD","DELETE"] in the schema. The facilitator's
// catalog validator requires those to be an exact single-value match, not a
// superset -- the x402 Doctor reported it verbatim as "bazaar.schema method
// enum must match the declared method" -- so every real settlement through
// /x402/relay and both self-settle paths was silently dropped by the catalog
// while verify, settle, and the on-chain transfer all reported success.
//
// Covers every declaration shape the platform actually emits, because the
// original bug was present in some call sites and not others.
func TestBazaarMethodEnumMatchesDeclaredMethod(t *testing.T) {
	cases := []struct {
		name string
		decl nodes.BazaarDeclaration
	}{
		{
			name: "relay self-listing",
			decl: nodes.BazaarDeclaration{
				RouteTemplate: "/api/x402/relay",
				OutputExample: map[string]any{"service": "AgentMesh x402 relay"},
			},
		},
		{
			name: "relay with target",
			decl: nodes.BazaarDeclaration{
				RouteTemplate: "/api/x402/relay",
				QueryParams:   map[string]any{"target": "https://example.test/paid"},
				OutputExample: map[string]any{"ok": true},
			},
		},
		{
			name: "self-settle route (no query params, no output)",
			decl: nodes.BazaarDeclaration{RouteTemplate: "/api/x402/relay/run-funding"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			method, enum := declaredMethodAndEnum(t, nodes.BazaarDiscoveryExtension(tc.decl))
			if len(enum) != 1 {
				t.Fatalf("want the method enum to hold exactly the declared method, got %v", enum)
			}
			if enum[0] != method {
				t.Fatalf("method enum %v does not match declared method %q -- the catalog validator drops this declaration", enum, method)
			}
		})
	}
}

// TestBazaarDiscoveryExtensionWireShape pins the exact JSON each declaration
// shape serializes to. The enum bug above survived because the declaration
// was built by hand in two places; this fixes the output of the one shared
// builder that replaced them, so a future edit there has to be deliberate
// rather than an unnoticed change to what the facilitator receives.
func TestBazaarDiscoveryExtensionWireShape(t *testing.T) {
	cases := []struct {
		name string
		decl nodes.BazaarDeclaration
		want string
	}{
		{
			// The relay's fixed self-listing: no query string at all.
			name: "relay self-listing",
			decl: nodes.BazaarDeclaration{
				RouteTemplate: "/api/x402/relay",
				OutputExample: map[string]any{"service": "AgentMesh x402 relay", "docs": true, "txId": "..."},
			},
			want: `{"bazaar":{
				"routeTemplate":"/api/x402/relay",
				"info":{
					"input":{"type":"http","method":"GET"},
					"output":{"type":"json","example":{"service":"AgentMesh x402 relay","docs":true,"txId":"..."}}
				},
				"schema":{
					"$schema":"https://json-schema.org/draft/2020-12/schema",
					"type":"object",
					"properties":{
						"input":{
							"type":"object",
							"properties":{
								"type":{"type":"string","const":"http"},
								"method":{"type":"string","enum":["GET"]}
							},
							"required":["type","method"],
							"additionalProperties":false
						},
						"output":{
							"type":"object",
							"properties":{"type":{"type":"string"},"example":{"type":"object"}},
							"required":["type"]
						}
					},
					"required":["input"]
				}
			}}`,
		},
		{
			// A ?target= relay: queryParams appear in info AND get a matching
			// (deliberately loose) schema property, but are never added to the
			// input's required list -- the self-listing has to stay valid
			// against this same schema.
			name: "relay with target",
			decl: nodes.BazaarDeclaration{
				RouteTemplate: "/api/x402/relay",
				QueryParams:   map[string]any{"target": "https://example.test/paid"},
				OutputExample: map[string]any{"ok": true, "note": "forwarded unmodified"},
			},
			want: `{"bazaar":{
				"routeTemplate":"/api/x402/relay",
				"info":{
					"input":{"type":"http","method":"GET","queryParams":{"target":"https://example.test/paid"}},
					"output":{"type":"json","example":{"ok":true,"note":"forwarded unmodified"}}
				},
				"schema":{
					"$schema":"https://json-schema.org/draft/2020-12/schema",
					"type":"object",
					"properties":{
						"input":{
							"type":"object",
							"properties":{
								"type":{"type":"string","const":"http"},
								"method":{"type":"string","enum":["GET"]},
								"queryParams":{"type":"object"}
							},
							"required":["type","method"],
							"additionalProperties":false
						},
						"output":{
							"type":"object",
							"properties":{"type":{"type":"string"},"example":{"type":"object"}},
							"required":["type"]
						}
					},
					"required":["input"]
				}
			}}`,
		},
		{
			// What FundRunReserve and SettlePlatformFee send. Neither route
			// takes query params or returns a payable body, so both optional
			// blocks are omitted entirely rather than declared empty.
			name: "self-settle route",
			decl: nodes.BazaarDeclaration{RouteTemplate: "/api/x402/relay/run-funding"},
			want: `{"bazaar":{
				"routeTemplate":"/api/x402/relay/run-funding",
				"info":{"input":{"type":"http","method":"GET"}},
				"schema":{
					"$schema":"https://json-schema.org/draft/2020-12/schema",
					"type":"object",
					"properties":{
						"input":{
							"type":"object",
							"properties":{
								"type":{"type":"string","const":"http"},
								"method":{"type":"string","enum":["GET"]}
							},
							"required":["type","method"],
							"additionalProperties":false
						}
					},
					"required":["input"]
				}
			}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(nodes.BazaarDiscoveryExtension(tc.decl))
			if err != nil {
				t.Fatalf("extension is not JSON-encodable: %v", err)
			}
			// Compared as decoded values, not as bytes: Go's map key ordering
			// is not what is under test here, the content is.
			var gotAny, wantAny any
			if err := json.Unmarshal(got, &gotAny); err != nil {
				t.Fatalf("failed to decode built extension: %v", err)
			}
			if err := json.Unmarshal([]byte(tc.want), &wantAny); err != nil {
				t.Fatalf("bad test fixture: %v", err)
			}
			gotNorm, _ := json.Marshal(gotAny)
			wantNorm, _ := json.Marshal(wantAny)
			if string(gotNorm) != string(wantNorm) {
				t.Errorf("extension shape changed\n got: %s\nwant: %s", gotNorm, wantNorm)
			}
		})
	}
}

// TestBazaarDiscoveryExtensionOmitsEmptyOptionalBlocks guards the specific
// distinction the self-settle routes depend on: a nil QueryParams/OutputExample
// must drop the key, not declare an empty object. An empty `queryParams: {}`
// in info would tell the catalog this route takes a query string it does not
// take, and an empty output block would advertise a response shape that no
// caller ever receives.
func TestBazaarDiscoveryExtensionOmitsEmptyOptionalBlocks(t *testing.T) {
	ext := nodes.BazaarDiscoveryExtension(nodes.BazaarDeclaration{RouteTemplate: "/api/x402/relay/platform-fee"})
	bazaar := ext["bazaar"].(map[string]any)
	info := bazaar["info"].(map[string]any)

	if _, present := info["output"]; present {
		t.Errorf("want info.output omitted when no OutputExample is given, got %v", info["output"])
	}
	input := info["input"].(map[string]any)
	if _, present := input["queryParams"]; present {
		t.Errorf("want info.input.queryParams omitted when no QueryParams are given, got %v", input["queryParams"])
	}

	schemaProps := bazaar["schema"].(map[string]any)["properties"].(map[string]any)
	if _, present := schemaProps["output"]; present {
		t.Errorf("want the output schema property omitted alongside info.output, got %v", schemaProps["output"])
	}
	inputProps := schemaProps["input"].(map[string]any)["properties"].(map[string]any)
	if _, present := inputProps["queryParams"]; present {
		t.Errorf("want the queryParams schema property omitted alongside info.input.queryParams, got %v", inputProps["queryParams"])
	}
}
