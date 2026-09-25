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
	"crypto/sha256"
	"encoding/hex"
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

	// Paths are the path arguments the call touches. Empty for calls that touch none.
	//
	// These describe approval scope. The tool remains responsible for resolving every path against
	// its workspace before performing an operation, and reports a refusal if it escapes.
	Paths []string

	// Command is the shell command, for execute calls. Empty otherwise.
	Command string

	// Arguments is the whole call in canonical form, for scoping an approval and for showing one.
	//
	// The text rather than a hash of it, deliberately. The prompt has to be able to display exactly
	// what an approval would cover, and a hash is not something anybody can read: offering "always,
	// this tool with exactly these arguments" while showing no arguments asks somebody to agree to
	// something they cannot see. The digest that keys the approval is derived from this, so the
	// thing displayed and the thing remembered cannot come apart.
	Arguments string

	// Opaque says the argument names in this call follow a vocabulary Canopy did not define.
	//
	// True for anything reached over MCP. It matters because the scope below is otherwise chosen by
	// looking for arguments called "path" or "command", which is a sound reading of the tools Canopy
	// wrote and a guess about everybody else's. A remote tool is free to call something "path" that
	// is not a path: these two differ only outside that field, and scoping by it would let one
	// standing approval cover both.
	//
	//	{"path": "project-1", "operation": "read"}
	//	{"path": "project-1", "operation": "delete"}
	Opaque bool

	// Tainted says the conversation has taken in content from outside, a fetched page, a web search
	// or an MCP tool's result, which may carry instructions nobody here wrote. From then on an action
	// that could send data out is asked about whatever the level or earlier approvals say.
	Tainted bool
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
	// Arguments the approval covers, as a fingerprint. Set only when there was no path and no
	// command to scope by, which is the case for a tool on an MCP server.
	Arguments string
}

func (s Scope) String() string {
	switch {
	case s.Command != "":
		return fmt.Sprintf("running %q", s.Command)
	case s.Path != "":
		return fmt.Sprintf("%s on %s", s.Tool, s.Path)
	case s.Kind != "":
		return fmt.Sprintf("any %s tool", s.Kind)
	case s.Arguments != "":
		// The fingerprint itself is not shown. It is a hash, it means nothing to a reader, and the
		// sentence has to be one somebody can act on: what they are agreeing to is this call again,
		// not this tool again.
		return fmt.Sprintf("%s with exactly these arguments", s.Tool)
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
	return strings.Join([]string{s.Tool, s.Path, s.Command, string(s.Kind), s.Arguments}, "\x00")
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
	// Before standing approvals and the level: an approval given before outside text arrived was
	// given to the conversation as it was then, and injected instructions aim precisely at the
	// actions a broad level runs unasked.
	if req.Tainted && exfiltrationCapable(req) {
		return Decision{Outcome: Ask, Scope: scope, Reason: "this conversation has read content from " +
			"outside (a fetched page, a web search or an MCP tool), which can carry instructions, and " +
			"this could send data out; it is asked about every time from here on"}
	}
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
		// what makes this safe, and it is enforced in the tools rather than here. A tool that
		// cannot resolve a path inside its workspace refuses before performing the operation.
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
	case req.Opaque && req.Arguments != "":
		// First for anything Canopy did not define the arguments of, because for those the field
		// names below are a guess. A remote tool naming something "path" does not make it one, and
		// scoping by it would cover every other call that happened to use the same value there.
		scope.Arguments = fingerprint(req.Arguments)
	case req.Command != "":
		scope.Command = req.Command
	case len(req.Paths) > 0:
		sorted := append([]string(nil), req.Paths...)
		sort.Strings(sorted)
		scope.Path = sorted[0]
	case req.Arguments != "":
		// A tool of Canopy's own that names neither. The alternative is a scope of the tool alone,
		// which is an approval far wider than the one being displayed.
		scope.Arguments = fingerprint(req.Arguments)
	}
	return scope
}

// fingerprint keys an approval by the exact call it was given for.
//
// Hashed rather than held whole because a scope is a map key and an approval is remembered for the
// life of a session, and some tools take a great deal of input. What the person agreed to is the
// canonical text, which the prompt shows them; this only has to tell two of them apart.
func fingerprint(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
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

// exfiltrationCapable reports whether a call could carry data out of the machine: anything on the
// network, any MCP tool, a shell command that reaches the network or hides what it runs, and a git
// push or a change of remote.
func exfiltrationCapable(req Request) bool {
	switch {
	case req.Kind == core.ToolNetwork, req.Opaque:
		return true
	case req.Kind == core.ToolGit:
		command := strings.TrimSpace(req.Command)
		return strings.HasPrefix(command, "push") || strings.HasPrefix(command, "remote")
	case req.Kind == core.ToolExecute:
		return networkish(req.Command)
	}
	return false
}

// networkCommands are programs whose purpose is to reach another machine, or to run text that is
// not in the command line itself and so could do anything.
var networkCommands = map[string]bool{
	"curl": true, "wget": true, "nc": true, "ncat": true, "netcat": true, "socat": true, "telnet": true,
	"ssh": true, "scp": true, "sftp": true, "rsync": true, "ftp": true, "tftp": true, "sendmail": true,
	"mail": true, "mailx": true, "nslookup": true, "dig": true, "host": true, "ping": true,
	"gh": true, "aws": true, "gcloud": true, "gsutil": true, "az": true, "kubectl": true, "twine": true,
	"openssl": true, "eval": true, "base64": true, "xxd": true,
}

// networkSubcommands are the network uses of tools that mostly work locally.
var networkSubcommands = map[string][]string{
	"git":    {"push", "fetch", "pull", "clone", "remote", "ls-remote", "submodule", "send-email", "archive"},
	"npm":    {"publish"},
	"yarn":   {"publish"},
	"pnpm":   {"publish"},
	"cargo":  {"publish"},
	"gem":    {"push"},
	"docker": {"push", "login"},
}

// networkish reports whether a shell command runs, as the command of any of its stages, a program
// that reaches the network. Only command words count, not paths or arguments, so building a package
// named mail or grepping for curl is not mistaken for sending anything.
func networkish(command string) bool {
	for _, stage := range stages(command) {
		words := strings.Fields(stage)
		// Leading assignments and wrappers that run the command after them.
		for len(words) > 0 && (strings.Contains(words[0], "=") || wrapper[words[0]]) {
			words = words[1:]
			// A wrapper's own options and numbers, as in timeout 10 or nice -n 5.
			for len(words) > 0 && (strings.HasPrefix(words[0], "-") || strings.Trim(words[0], "0123456789.smh") == "") {
				words = words[1:]
			}
		}
		if len(words) == 0 {
			continue
		}
		name := words[0]
		if i := strings.LastIndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		}
		if networkCommands[name] {
			return true
		}
		// A shell given its commands as a string or on its input runs text the command line does
		// not show; one given a script file is the script's own business.
		if name == "sh" || name == "bash" || name == "zsh" {
			if len(words) == 1 || words[1] == "-c" || words[1] == "-s" {
				return true
			}
		}
		if subs, ok := networkSubcommands[name]; ok {
			if sub := subcommand(words[1:]); sub != "" {
				for _, s := range subs {
					if sub == s {
						return true
					}
				}
			}
		}
	}
	return false
}

// subcommand is the first word that is not an option, skipping the values of the options that take
// one, as in git -C dir push.
func subcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		switch w := args[i]; {
		case w == "-C" || w == "-c" || w == "--git-dir" || w == "--work-tree" || w == "--namespace":
			i++
		case strings.HasPrefix(w, "-"):
		default:
			return w
		}
	}
	return ""
}

// wrapper are commands that run the command after them.
var wrapper = map[string]bool{"sudo": true, "env": true, "nohup": true, "time": true, "command": true,
	"exec": true, "xargs": true, "nice": true, "timeout": true, "stdbuf": true}

// stages splits a shell command into the commands it runs: at pipes, sequences, conditionals,
// newlines, subshells and command substitutions.
func stages(command string) []string {
	replacer := strings.NewReplacer("||", "\n", "&&", "\n", "|", "\n", ";", "\n", "&", "\n",
		"$(", "\n", "`", "\n", "(", "\n", ")", "\n", "{", "\n", "}", "\n")
	return strings.Split(replacer.Replace(command), "\n")
}
