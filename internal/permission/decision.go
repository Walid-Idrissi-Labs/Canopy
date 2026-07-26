// Package permission decides whether a tool call may run.
//
// **This is not the repository trust contract and must not be confused with it.** That one governs
// commands a user wrote in a configuration file and checked into a repository. This one governs
// commands a model generated, possibly in response to text it read out of a file somebody else
// wrote. Different threat model, different answers, and reusing one for the other would be the kind
// of mistake that only looks obvious afterwards.
//
// **Canopy does not sandbox and this package must never imply that it does.** A shell command runs
// as the user, with the user's filesystem and the user's network and the user's credentials. What
// this provides is a decision about whether to run it and a record of having run it. Those are worth
// a great deal and they are not isolation.
package permission

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Outcome is what the permission model decided about a call.
type Outcome string

const (
	// Allow means run it without asking.
	Allow Outcome = "allow"
	// Ask means a person has to say yes first.
	Ask Outcome = "ask"
	// Deny means it does not run, and asking would not help.
	//
	// Kept apart from Ask because they mean different things to the user and to the model. A denial
	// is structural: this agent's trust level does not include this, and clicking yes is not on
	// offer. Presenting it as a question that can only be answered no would train people to click
	// through prompts, which is the failure mode the whole design is trying to avoid.
	Deny Outcome = "deny"
)

// AllOutcomes returns every outcome.
func AllOutcomes() []Outcome { return []Outcome{Allow, Ask, Deny} }

// Valid reports whether o is a known outcome.
func (o Outcome) Valid() bool {
	for _, known := range AllOutcomes() {
		if o == known {
			return true
		}
	}
	return false
}

func (o Outcome) String() string { return string(o) }

// Request is a tool call awaiting a decision.
type Request struct {
	// AgentID and SessionID identify who is asking, so the audit trail can answer "what did this
	// agent actually do".
	AgentID   string
	SessionID string

	// Tool is what was called.
	Tool string
	Kind core.ToolKind

	// Paths are the workspace paths the call touches, already resolved. Empty for calls that touch
	// none.
	//
	// Resolved rather than as written, because an approval for `src/main.go` must not be satisfied
	// by `src/../src/main.go` or by a symlink, and comparing unresolved strings is how that
	// happens.
	Paths []string

	// Command is the shell command, for execute calls. Empty otherwise.
	Command string
}

// Decision is the answer, and why.
type Decision struct {
	Outcome Outcome

	// Reason is shown to the user when asking and returned to the model when denying, so it has to
	// read well as both. "Writing files needs at least confined trust, this agent is read-only"
	// works in both places; an error code does not.
	Reason string

	// Scope is what an approval would cover, for the prompt to display. Empty when nothing is being
	// asked.
	Scope Scope
}

// Scope is the breadth of an approval.
//
// Explicit rather than implied, because the difference between "yes, this file" and "yes, this
// agent, anything" is the entire safety margin and a user has to be able to see which one they are
// agreeing to.
type Scope struct {
	// Tool the approval covers. Always set.
	Tool string
	// Path the approval covers, empty when it is not path scoped.
	Path string
	// Command the approval covers verbatim, empty when it is not command scoped.
	Command string
	// Kind the approval covers, set only for approvals that cover a whole class of tool.
	Kind core.ToolKind
}

func (s Scope) String() string {
	switch {
	case s.Command != "":
		return fmt.Sprintf("running %q", s.Command)
	case s.Path != "":
		return fmt.Sprintf("%s on %s", s.Tool, s.Path)
	case s.Kind != "":
		return fmt.Sprintf("any %s tool", s.Kind)
	default:
		return s.Tool
	}
}

// key is how a granted approval is looked up again.
//
// Built from the same fields the scope displays, so an approval can never cover something the user
// was not shown. If the key were coarser than the prompt, somebody would approve one file and grant
// a directory.
func (s Scope) key() string {
	return strings.Join([]string{s.Tool, s.Path, s.Command, string(s.Kind)}, "\x00")
}

// Decide answers whether a call may run, given a trust level and what has already been approved.
//
// The order of the checks is the design. Structural denials come first, because a level that does
// not include writing at all should say so rather than prompting for something it would refuse
// anyway. Then existing approvals, so a user who already said yes is not asked twice. Then whether
// this level runs this kind without asking. Anything left over is a question.
func Decide(req Request, level core.TrustLevel, granted *Grants) Decision {
	if !level.Valid() {
		// Fail closed. An unrecognised level is a configuration somebody got wrong, and the safe
		// reading of "I do not know how much this agent is trusted" is "not at all".
		return Decision{
			Outcome: Deny,
			Reason: fmt.Sprintf(
				"this agent has an unrecognised trust level (%q), so nothing runs until it is fixed",
				level),
		}
	}

	if denial, denied := structurallyDenied(req, level); denied {
		return denial
	}

	scope := scopeFor(req)
	if granted != nil && granted.Covers(req, scope) {
		return Decision{Outcome: Allow, Reason: "already approved", Scope: scope}
	}

	if allowedWithoutAsking(req, level) {
		return Decision{Outcome: Allow, Reason: fmt.Sprintf("%s trust runs %s tools without asking",
			level, req.Kind), Scope: scope}
	}

	return Decision{Outcome: Ask, Reason: reasonToAsk(req, level), Scope: scope}
}

// structurallyDenied reports the cases where asking would be pointless.
func structurallyDenied(req Request, level core.TrustLevel) (Decision, bool) {
	switch req.Kind {
	case core.ToolWrite:
		if !level.AllowsWrites() {
			return Decision{
				Outcome: Deny,
				Reason: fmt.Sprintf(
					"changing files needs at least confined trust, and this agent is %s", level),
			}, true
		}

	case core.ToolExecute:
		if !level.AllowsShell() {
			return Decision{
				Outcome: Deny,
				Reason: fmt.Sprintf(
					"running commands needs at least standard trust, and this agent is %s", level),
			}, true
		}

	case core.ToolGit:
		// Ordinary git is a read or a write and is handled by those rules. Only the destructive
		// operations are gated separately, because a bad edit is recoverable from git and a bad
		// `git checkout` is what you would have recovered from.
		if isDestructiveGit(req.Command) && !level.AllowsDestructiveGit() {
			return Decision{
				Outcome: Deny,
				Reason: fmt.Sprintf(
					"%q can destroy uncommitted work, which needs broad trust, and this agent is %s",
					req.Command, level),
			}, true
		}
	}
	return Decision{}, false
}

// allowedWithoutAsking reports whether this level runs this kind unprompted.
//
// Reading is always allowed. Everything else is a judgement about the level, and the judgements are
// deliberately conservative at the low end: the point of having levels at all is that somebody can
// run a scratch agent broadly and an agent near `main` narrowly, and that only works if the narrow
// one is genuinely narrow.
func allowedWithoutAsking(req Request, level core.TrustLevel) bool {
	switch req.Kind {
	case core.ToolRead:
		return true

	case core.ToolWrite:
		// Confined and above write inside their own workspace without asking. The confinement is
		// what makes this safe, and it is enforced in the tools rather than here: a path that
		// reached this function has already been resolved inside the workspace.
		return level.AllowsWrites()

	case core.ToolGit:
		// Broad trust is defined as running destructive git on its own branch without asking, so
		// this has to actually do that. Anything else makes the level a label rather than a
		// setting.
		return !isDestructiveGit(req.Command) || level.AllowsDestructiveGit()

	case core.ToolExecute:
		// Never silent below broad, even for a level that allows shell at all. A shell command is
		// an opaque string that can do anything the user can, and the difference between standard
		// and broad is exactly whether somebody sees it first.
		return level.AtLeast(core.TrustBroad)

	case core.ToolNetwork:
		// Fetching a URL brings untrusted text into the model's context, which is the injection
		// path. Cheap to approve, expensive to have been wrong about.
		return false

	default:
		// An unrecognised kind is one nobody has reasoned about. Ask.
		return false
	}
}

func reasonToAsk(req Request, level core.TrustLevel) string {
	switch req.Kind {
	case core.ToolExecute:
		return fmt.Sprintf("%s trust shows shell commands before running them", level)
	case core.ToolNetwork:
		return "fetching brings text from outside into the conversation"
	case core.ToolGit:
		return "this git operation can destroy work that is not committed"
	default:
		return fmt.Sprintf("%s trust asks before %s tools", level, req.Kind)
	}
}

// destructiveGit are the operations that can lose work which is not recoverable from git itself.
//
// Matched on the subcommand and its flags rather than on the whole string, because a command is
// approved by what it does and `git commit -m "reset the counter"` is not a reset.
var destructiveGit = []string{
	"reset --hard",
	"checkout --force",
	"checkout -f",
	"clean -f",
	"clean -d",
	"branch -D",
	"push --force",
	"push -f",
	"rebase",
	"stash drop",
	"stash clear",
	"filter-branch",
	"reflog delete",
}

// isDestructiveGit reports whether a git command can lose uncommitted or unpushed work.
//
// **Case is preserved, deliberately.** `git branch -d` deletes a branch only if it has been merged;
// `git branch -D` deletes it regardless. Lowercasing the command to make matching easier would
// conflate the two and quietly allow the destructive one at a level that should ask about it. Git
// subcommands are lowercase in practice, so nothing is lost by keeping case.
//
// Conservative about what it does not recognise: an unfamiliar git command carrying `--force` or a
// bare `-f` is treated as destructive, because the flag means the same thing wherever it appears
// and a list of subcommands will always be behind the tool.
func isDestructiveGit(command string) bool {
	normalised := strings.Join(strings.Fields(command), " ")
	if normalised == "" {
		return false
	}
	for _, pattern := range destructiveGit {
		if strings.Contains(normalised, pattern) {
			return true
		}
	}
	return strings.Contains(normalised, "--force") || hasFlag(normalised, "-f")
}

func hasFlag(command, flag string) bool {
	for _, field := range strings.Fields(command) {
		if field == flag {
			return true
		}
	}
	return false
}

// scopeFor picks the narrowest approval that would cover a call.
//
// Narrowest, always. A broader approval is something a user can choose explicitly, and offering it
// as the default is how "yes" comes to mean "yes to everything" without anybody deciding that.
func scopeFor(req Request) Scope {
	scope := Scope{Tool: req.Tool}
	switch {
	case req.Command != "":
		scope.Command = req.Command
	case len(req.Paths) > 0:
		sorted := append([]string(nil), req.Paths...)
		sort.Strings(sorted)
		scope.Path = sorted[0]
	}
	return scope
}

// PathScope builds an approval covering a directory and everything under it.
//
// Offered as a deliberate widening, for the case where an agent is going to touch many files in one
// place and approving each is theatre rather than review.
func PathScope(tool, dir string) Scope {
	return Scope{Tool: tool, Path: filepath.Clean(dir) + string(filepath.Separator)}
}

// KindScope builds an approval covering every tool of a kind, for this agent, for this session.
//
// The broadest thing on offer and it is still bounded by the session, because an approval that
// outlives the conversation it was given in is one nobody remembers granting.
func KindScope(kind core.ToolKind) Scope { return Scope{Kind: kind} }
