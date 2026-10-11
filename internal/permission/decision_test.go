package permission

import (
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// No approval is offered as a blank: every shape of scope says what always would cover.
func TestNoScopeRendersEmpty(t *testing.T) {
	for _, scope := range []Scope{{Tool: "spawn_agents"}, {Tool: "t", Path: "a"}, {Tool: "t", Command: "c"},
		{Tool: "t", Kind: core.ToolWrite}, {Tool: "t", Arguments: "f"}} {
		if s := scope.String(); s == "" || s == scope.Tool {
			t.Errorf("%+v renders as %q", scope, s)
		}
	}
}

// A scope that names nothing renders as nothing, so no prompt can dress it up as an always.
func TestAnEmptyScopeRendersAsNothing(t *testing.T) {
	if s := (Scope{}).String(); s != "" {
		t.Fatalf("an empty scope renders as %q", s)
	}
}
