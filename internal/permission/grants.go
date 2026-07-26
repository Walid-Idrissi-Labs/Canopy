package permission

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Grants remembers what a user has already approved.
//
// Per session, and it dies with the session. An approval that outlives the conversation it was
// given in is one nobody remembers granting, and "I said yes to that last Tuesday" is not consent
// anybody would recognise. Persisting these would make Canopy quieter and would make the quiet
// meaningless.
type Grants struct {
	mu      sync.RWMutex
	granted map[string]Scope
}

// NewGrants builds an empty set of approvals.
func NewGrants() *Grants { return &Grants{granted: map[string]Scope{}} }

// Grant records an approval.
func (g *Grants) Grant(scope Scope) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.granted[scope.key()] = scope
}

// Revoke removes an approval.
//
// Exists because somebody who realises they approved too broadly needs a way back that is not
// restarting the session and losing the conversation.
func (g *Grants) Revoke(scope Scope) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.granted, scope.key())
}

// Granted returns every approval currently in force.
//
// So the interface can show them. An approval the user cannot see is one they cannot reconsider,
// and a growing invisible list of things this agent may now do without asking is exactly the state
// this design exists to keep out of.
func (g *Grants) Granted() []Scope {
	g.mu.RLock()
	defer g.mu.RUnlock()

	out := make([]Scope, 0, len(g.granted))
	for _, scope := range g.granted {
		out = append(out, scope)
	}
	return out
}

// Covers reports whether an existing approval already permits a request.
//
// The matching is deliberately narrow. An approval covers a request when it is the same scope, or
// when it is a directory approval containing every path the request touches, or when it is a kind
// approval for the request's kind. Nothing else. Anything cleverer here is a place for an approval
// to quietly stretch further than the sentence the user read.
func (g *Grants) Covers(req Request, scope Scope) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if _, exact := g.granted[scope.key()]; exact {
		return true
	}

	for _, held := range g.granted {
		if held.Kind != "" && held.Kind == req.Kind {
			return true
		}
		if held.covers(req) {
			return true
		}
	}
	return false
}

// covers reports whether this approval permits a request.
func (s Scope) covers(req Request) bool {
	if s.Tool != "" && s.Tool != req.Tool {
		return false
	}

	// A command approval is exact. Two shell commands that differ by a character do different
	// things, and an approval for one is not evidence about the other.
	if s.Command != "" {
		return s.Command == req.Command
	}

	if s.Path == "" {
		return false
	}

	// A directory approval, recognised by the trailing separator. Every path the request touches
	// has to be inside it: approving a directory and then having one path of a multi file call fall
	// outside it would let the call through on the strength of the paths that did match.
	if !strings.HasSuffix(s.Path, string(filepath.Separator)) {
		return false
	}
	if len(req.Paths) == 0 {
		return false
	}
	for _, path := range req.Paths {
		if !strings.HasPrefix(filepath.Clean(path)+string(filepath.Separator),
			s.Path) && filepath.Clean(path) != strings.TrimSuffix(s.Path, string(filepath.Separator)) {
			return false
		}
	}
	return true
}

// Kind approvals are bounded by the session for the same reason everything else here is, and there
// is deliberately no way to grant one for a kind the trust level would deny. A user cannot approve
// their way past a structural denial, because that denial is the thing they chose when they picked
// the level, and a prompt that could override it would make the level advisory.

// GrantableKinds returns the tool kinds a level could be asked to approve wholesale.
//
// Excludes anything the level denies structurally, so the interface never offers a button that
// would not work.
func GrantableKinds(level core.TrustLevel) []core.ToolKind {
	var out []core.ToolKind
	for _, kind := range core.AllToolKinds() {
		probe := Request{Kind: kind, Command: "harmless"}
		if _, denied := structurallyDenied(probe, level); denied {
			continue
		}
		out = append(out, kind)
	}
	return out
}
