package session

import (
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
)

// A standing approval is listed, and taken back, after which it covers nothing.
func TestAGrantIsListedAndRevoked(t *testing.T) {
	e := New(fixedResolver{})
	defer e.Close()
	scope := permission.Scope{Tool: "run_command", Command: "go test ./..."}
	req := permission.Request{SessionID: "s1", Tool: "run_command", Command: "go test ./..."}
	e.grantsFor("s1").Grant(scope)
	if got := e.Grants("s1"); len(got) != 1 || got[0] != scope {
		t.Fatalf("listed %v", got)
	}
	e.Revoke("s1", scope)
	if len(e.Grants("s1")) != 0 || e.grantsFor("s1").Covers(req, scope) {
		t.Fatal("a revoked approval still covers the call")
	}
}
