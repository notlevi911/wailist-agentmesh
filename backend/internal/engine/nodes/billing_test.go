package nodes_test

import (
	"testing"

	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

func TestBillableFlatFee(t *testing.T) {
	cases := []struct {
		name     string
		nodeType models.NodeType
		template string
		want     bool
	}{
		{"agent is always billable", models.NodeTypeAgent, "", true},
		{"action with a connector template is billable", models.NodeTypeAction, "slack", true},
		{"action with no template is still billable", models.NodeTypeAction, "", true},
		{"tool with http template is billable", models.NodeTypeTool, "http", true},
		{"tool with calc template is free", models.NodeTypeTool, "calc", false},
		{"tool with datetime template is free", models.NodeTypeTool, "datetime", false},
		{"tool with no template is free", models.NodeTypeTool, "", false},
		{"trigger is free", models.NodeTypeTrigger, "", false},
		{"end is free", models.NodeTypeEnd, "", false},
		{"provider is free", models.NodeTypeProvider, "", false},
		{"tool402 is handled separately, not flat-fee billable", models.NodeTypeTool402, "", false},
		{"google (gmail/sheets/calendar/drive) is billable like any connector", models.NodeTypeGoogle, "gmail_send", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := nodes.BillableFlatFee(c.nodeType, c.template)
			if got != c.want {
				t.Fatalf("BillableFlatFee(%v, %q) = %v, want %v", c.nodeType, c.template, got, c.want)
			}
		})
	}
}
