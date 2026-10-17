package nodes

import (
	"testing"

	"github.com/agentmesh/backend/internal/models"
)

func TestIsDegradable(t *testing.T) {
	tests := []struct {
		name string
		node models.WorkflowNode
		want bool
	}{
		{"http GET", models.WorkflowNode{Type: models.NodeTypeTool, Template: "http", Method: "GET"}, true},
		{"http blank method defaults to GET", models.WorkflowNode{Type: models.NodeTypeTool, Template: "http"}, true},
		{"http lowercase get", models.WorkflowNode{Type: models.NodeTypeTool, Template: "http", Method: "get"}, true},
		{"http POST", models.WorkflowNode{Type: models.NodeTypeTool, Template: "http", Method: "POST"}, false},
		{"http DELETE", models.WorkflowNode{Type: models.NodeTypeTool, Template: "http", Method: "DELETE"}, false},
		// The method token is extensible (RFC 7231 §4.1), so anything not
		// recognised as a read must fail closed. Negating the body list read
		// every one of these as a GET.
		{"http MKCOL writes, and is in no body list", models.WorkflowNode{Type: models.NodeTypeTool, Template: "http", Method: "MKCOL"}, false},
		{"http PURGE", models.WorkflowNode{Type: models.NodeTypeTool, Template: "http", Method: "PURGE"}, false},
		{"http with a typo for POST", models.WorkflowNode{Type: models.NodeTypeTool, Template: "http", Method: "PSOT"}, false},
		{"http HEAD", models.WorkflowNode{Type: models.NodeTypeTool, Template: "http", Method: "HEAD"}, true},
		{"http OPTIONS", models.WorkflowNode{Type: models.NodeTypeTool, Template: "http", Method: "OPTIONS"}, true},
		{"websearch leaves the workflow", models.WorkflowNode{Type: models.NodeTypeTool, Template: "websearch"}, true},
		// Pure computation. A jsonPath that does not match is an authoring
		// bug that repeats on every run, so degrading it would answer "the
		// step failed" forever and tell nobody to fix it.
		{"json_extract is computation, not a source", models.WorkflowNode{Type: models.NodeTypeTool, Template: "json_extract"}, false},
		{"calc", models.WorkflowNode{Type: models.NodeTypeTool, Template: "calc"}, false},
		{"template", models.WorkflowNode{Type: models.NodeTypeTool, Template: "template"}, false},
		{"html_extract", models.WorkflowNode{Type: models.NodeTypeTool, Template: "html_extract"}, false},
		{"coingecko", models.WorkflowNode{Type: models.NodeTypeAction, Template: "coingecko"}, true},
		{"rss", models.WorkflowNode{Type: models.NodeTypeAction, Template: "rss"}, true},
		{"slack send", models.WorkflowNode{Type: models.NodeTypeAction, Template: "slack"}, false},
		{"postgres write", models.WorkflowNode{Type: models.NodeTypeAction, Template: "db"}, false},
		{"graphql can mutate", models.WorkflowNode{Type: models.NodeTypeAction, Template: "graphql"}, false},
		{"sheets_read", models.WorkflowNode{Type: models.NodeTypeGoogle, Template: "sheets_read"}, true},
		// Removed with its restricted scope: no longer in the catalog, so it
		// fails closed like any unknown template.
		{"gmail_list (removed)", models.WorkflowNode{Type: models.NodeTypeGoogle, Template: "gmail_list"}, false},
		{"gmail_send", models.WorkflowNode{Type: models.NodeTypeGoogle, Template: "gmail_send"}, false},
		// This workflow's own saved value, not a live source: a miss is a
		// wrong key, which no retry and no later run will fix.
		{"state get", models.WorkflowNode{Type: models.NodeTypeState, Template: "get"}, false},
		{"state set", models.WorkflowNode{Type: models.NodeTypeState, Template: "set"}, false},
		{"agent", models.WorkflowNode{Type: models.NodeTypeAgent, Template: "agent"}, false},
		{"tendril rent", models.WorkflowNode{Type: models.NodeTypeTendril, Template: "tendril_rent"}, false},
		// tool402 and walletpay are not catalog templates at all. The
		// fail-closed default must cover them: they move real money.
		{"tool402 is not in the catalog", models.WorkflowNode{Type: models.NodeTypeTool402, Template: ""}, false},
		{"unknown template", models.WorkflowNode{Type: models.NodeTypeAction, Template: "does_not_exist"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsDegradable(tt.node); got != tt.want {
				t.Fatalf("IsDegradable(%s/%s) = %v, want %v", tt.node.Type, tt.node.Template, got, tt.want)
			}
		})
	}
}

// TestEveryTemplateIsClassified is the test that catches a connector added
// later without a kind. An unclassified template still behaves safely (the
// default is "action"), but silently: this makes the omission loud.
func TestEveryTemplateIsClassified(t *testing.T) {
	for _, ty := range NodeCatalogData().Types {
		for _, tpl := range ty.Templates {
			switch tpl.Kind {
			case "read", "compute", "action":
			default:
				t.Errorf("template %s/%s has kind %q, want \"read\", \"compute\" or \"action\" -- set it in frontend/src/lib/nodeCatalog.ts and run `npm run gen:node-catalog`", ty.Type, tpl.ID, tpl.Kind)
			}
		}
	}
}

// Only a template that fetches from outside the workflow may degrade. A
// "compute" template runs entirely in-process, so nothing outside can make it
// fail and the same input fails the same way on every run -- degrading one
// turns an authoring bug into a permanently partial answer.
func TestOnlyLiveSourcesAreDegradable(t *testing.T) {
	for _, ty := range NodeCatalogData().Types {
		for _, tpl := range ty.Templates {
			n := models.WorkflowNode{Type: models.NodeType(ty.Type), Template: tpl.ID}
			// tool/http is classified by method, not by its kind.
			if ty.Type == "tool" && tpl.ID == "http" {
				continue
			}
			if got, want := IsDegradable(n), tpl.Kind == "read"; got != want {
				t.Errorf("IsDegradable(%s/%s) = %v for kind %q, want %v", ty.Type, tpl.ID, got, tpl.Kind, want)
			}
		}
	}
}
