package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	execpkg "github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/agent"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	gitpkg "github.com/Walid-Idrissi-Labs/Canopy/internal/git"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
)

// Exit codes for a headless run, so a script can tell what happened without reading prose.
const (
	exitOK        = 0
	exitFailed    = 1   // the turn failed, was refused, or was cut off
	exitUsage     = 2   // the command line or configuration is wrong
	exitRed       = 3   // the turn completed and the project's tests do not pass
	exitTimeout   = 124 // what timeout(1) uses
	exitCancelled = 130
)

// runHeadless runs one prompt through the full agent loop, tools included, without the interface:
// for scripts, CI and editors. Nothing that needs a person is approved unless --yes says so, and
// then only within the mode's own limits.
func runHeadless(args []string, stdin io.Reader, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(errOut)
	prompt := flags.String("p", "", "the prompt; read from stdin when omitted")
	modeName := flags.String("mode", core.ModeBuild, "plan, confined, build, runway or cruise")
	keyName := flags.String("key", "", "the named key to use; the default key when omitted")
	model := flags.String("model", "", "the model; the key's default when omitted")
	format := flags.String("output", "text", "text, json or stream-json")
	yes := flags.Bool("yes", false, "approve the calls this mode would ask a person about")
	maxSteps := flags.Int("max-steps", 0, "stop after this many model calls")
	timeout := flags.Duration("timeout", 30*time.Minute, "stop after this long")
	effortName := flags.String("effort", "", "low, medium, high, xhigh or max; the provider's default when omitted")
	verify := flags.Bool("verify", false, "run the project's tests after the turn; exit 3 when they fail")
	budget := flags.Float64("budget", 0, "stop once the run has spent this many dollars, retries included")
	escalate := flags.Int("escalate", 0, "with -verify, retry up to this many times at a higher effort when the tests fail")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if *prompt == "" && flags.NArg() > 0 {
		*prompt = strings.Join(flags.Args(), " ")
	}
	if *prompt == "" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			_, _ = fmt.Fprintf(errOut, "reading the prompt from stdin: %v\n", err)
			return exitUsage
		}
		*prompt = strings.TrimSpace(string(data))
	}
	if *prompt == "" {
		_, _ = fmt.Fprintln(errOut, "a prompt is required: canopy run -p \"...\" or pipe it on stdin")
		return exitUsage
	}
	if *format != "text" && *format != "json" && *format != "stream-json" {
		_, _ = fmt.Fprintf(errOut, "unknown output format %q: use text, json or stream-json\n", *format)
		return exitUsage
	}
	mode, ok := core.ModeByName(*modeName)
	if !ok {
		_, _ = fmt.Fprintf(errOut, "unknown mode %q: use %s\n", *modeName, strings.Join(core.ModeNames(), ", "))
		return exitUsage
	}

	keyStore, err := openKeyStore()
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return exitUsage
	}
	resolver := session.NewKeyResolver(keyStore, version)
	resolver.Renews(signInSources())
	engine := session.New(resolver)
	defer engine.Close()
	// One prompt and done: a summary compacted at the end would be paid for and never read.
	engine.SetAutoCompact(false)
	if err := attachHistory(engine); err != nil {
		_, _ = fmt.Fprintf(errOut, "warning: history is not being saved: %v\n", err)
	}

	dir, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return exitUsage
	}
	engine.SetProjectID(gitpkg.WorkspaceID(dir))
	// No one is there to answer a trust prompt, so an untrusted repository's configuration is
	// withheld and the warning says how to trust it.
	project := gateProject(dir, loadProjectRaw(dir, errOut), stdin, errOut, false)
	engine.WithInstructions(projectInstructions(dir, project, errOut))
	engine.SetWebSearch(webSearchWanted())

	// Checked before anything is spent: -verify with nothing to verify against would exit 0 on
	// a run nobody checked, and a script reading that status would take it for green.
	if *escalate > 0 && !*verify {
		_, _ = fmt.Fprintln(errOut, "-escalate retries when the tests fail, so it needs -verify")
		return exitUsage
	}
	if *verify && len(testsFor(project)) == 0 {
		_, _ = fmt.Fprintln(errOut, "-verify needs the project's tests, and none are configured or the "+
			"repository is not trusted; run canopy trust, or add tests to canopy.json")
		return exitUsage
	}

	registry, err := toolsFor(dir)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "warning: tools are not available: %v\n", err)
	} else {
		approver := agent.DenyAll
		if *yes {
			approver = agent.ApproverFunc(func(context.Context, permission.Request, permission.Decision) bool { return true })
		}
		engine.WithTools(registry, projectTrust(project), approver)
	}
	stopServers := attachMCP(engine, dir, project)
	defer stopServers()

	if *keyName == "" {
		*keyName = resolver.DefaultKeyName()
	}
	if *model == "" {
		*model = defaultModelFor(keyStore, *keyName)
	}
	main, err := engine.AddAgent(context.Background(), session.Agent{
		Name: "main", KeyName: *keyName, Model: *model, Dir: dir, Trust: projectTrust(project),
	})
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "starting the agent: %v\n", err)
		return exitUsage
	}
	if reason := engine.ModeUnusable(main.SessionID, mode); reason != nil {
		_, _ = fmt.Fprintf(errOut, "%s cannot be used here: %v\n", mode.Name, reason)
		return exitUsage
	}
	if err := engine.SetMode(main.SessionID, mode); err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return exitUsage
	}
	if *maxSteps > 0 {
		engine.SetMaxSteps(*maxSteps)
	}
	if *budget < 0 {
		_, _ = fmt.Fprintln(errOut, "-budget is an amount in dollars")
		return exitUsage
	}
	if *budget > 0 {
		// Across every agent, so agents the run starts are held to it as well.
		_ = engine.SetOverallBudget(*budget)
	}
	effort := core.Effort(*effortName)
	if !effort.Valid() {
		_, _ = fmt.Fprintf(errOut, "unknown effort %q\n", *effortName)
		return exitUsage
	}
	engine.SetEffort(effort)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	events := engine.Events(0)
	turnID, err := engine.Send(main.SessionID, *prompt)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "sending the prompt: %v\n", err)
		return exitFailed
	}
	turn := follow(ctx, engine, events, main.SessionID, turnID, *format, out)
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil && !turn.State.Terminal() {
			engine.Cancel(main.SessionID)
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return exitTimeout
			}
			return exitCancelled
		}
		if !*verify || turn.State != core.TurnComplete {
			return reportRun(turn, main.SessionID, *format, out, errOut)
		}
		failures, ok := verifyWorkspace(ctx, dir, project, errOut)
		if ok {
			return reportRun(turn, main.SessionID, *format, out, errOut)
		}
		if attempt >= *escalate {
			_ = reportRun(turn, main.SessionID, *format, out, errOut)
			_, _ = fmt.Fprintln(errOut, "the project's tests do not pass")
			return exitRed
		}
		// Escalate on red: the cheap attempt failed its own evidence, so the next one thinks harder.
		// Running cheap first and paying for depth only on failure is where the saving comes from.
		effort = nextEffort(effort)
		engine.SetEffort(effort)
		_, _ = fmt.Fprintf(errOut, "tests failed; trying again at %s effort\n", effort)
		next, err := engine.Send(main.SessionID, "The project's tests fail after your change. What failed:\n\n"+
			failures+"\n\nFix the cause, then say what you changed.")
		if err != nil {
			_, _ = fmt.Fprintf(errOut, "sending the retry: %v\n", err)
			return exitFailed
		}
		turn = follow(ctx, engine, events, main.SessionID, next, *format, out)
	}
}

// follow streams a turn as it happens and returns it once it has ended.
func follow(ctx context.Context, engine *session.Engine, events <-chan core.Event,
	sessionID, turnID, format string, out io.Writer) core.Turn {
	var printed, tools int
	encoder := json.NewEncoder(out)
	for {
		s, _ := engine.Session(sessionID)
		var turn core.Turn
		for _, t := range s.Turns {
			if t.ID == turnID {
				turn = t
			}
		}
		// New text, and each tool call once, as they arrive.
		if len(turn.Text) > printed {
			chunk := turn.Text[printed:]
			printed = len(turn.Text)
			switch format {
			case "text":
				_, _ = io.WriteString(out, terminalText(chunk))
			case "stream-json":
				_ = encoder.Encode(map[string]any{"type": "text", "text": chunk})
			}
		}
		for ; tools < len(turn.ToolCalls); tools++ {
			call := turn.ToolCalls[tools]
			if format == "stream-json" {
				_ = encoder.Encode(map[string]any{"type": "tool_call", "name": call.Name,
					"input": json.RawMessage(validJSON(call.Input))})
			}
		}
		if turn.State.Terminal() {
			return turn
		}
		select {
		case <-ctx.Done():
			return turn
		case _, ok := <-events:
			if !ok {
				return turn
			}
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func validJSON(raw []byte) []byte {
	if json.Valid(raw) {
		return raw
	}
	quoted, _ := json.Marshal(string(raw))
	return quoted
}

// report prints the result and chooses the exit code.
func reportRun(turn core.Turn, sessionID, format string, out, errOut io.Writer) int {
	code := exitOK
	if turn.State != core.TurnComplete {
		code = exitFailed
	}
	summary := map[string]any{
		"type": "result", "state": turn.State, "session": session.Code(sessionID),
		"text": turn.Text, "error": turn.Error,
		"usage": map[string]any{
			"input_tokens": turn.Usage.InputTokens, "output_tokens": turn.Usage.OutputTokens,
			"cache_read_tokens": turn.Usage.CacheReadTokens, "cache_write_tokens": turn.Usage.CacheWriteTokens,
		},
		"tool_calls": len(turn.ToolCalls),
	}
	if turn.Usage.CostKnown {
		summary["cost_usd"] = turn.Usage.CostUSD
	}
	switch format {
	case "json", "stream-json":
		_ = json.NewEncoder(out).Encode(summary)
	default:
		if !strings.HasSuffix(turn.Text, "\n") {
			_, _ = io.WriteString(out, "\n")
		}
		if turn.Error != "" {
			_, _ = fmt.Fprintf(errOut, "%s: %s\n", turn.State, turn.Error)
		}
	}
	return code
}

// loadProjectRaw reads canopy.json without asking about trust; the caller gates it.
func loadProjectRaw(dir string, errOut io.Writer) config.Project {
	project, _, err := config.Load(dir)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "warning: %v\nwarning: continuing with nothing configured\n", err)
		return config.Project{}
	}
	return project
}

// verifyWorkspace runs the project's configured tests in dir and returns the tail of each failing
// required test's output.
func verifyWorkspace(ctx context.Context, dir string, project config.Project, errOut io.Writer) (string, bool) {
	tests := testsFor(project)
	if len(tests) == 0 {
		return "no tests are configured", false
	}
	var failures []string
	for i, test := range tests {
		outcome := execpkg.RunTest(ctx, test, execpkg.Target{Dir: dir}, fmt.Sprintf("verify-%d", i))
		if outcome.Run.State == core.TestPassing || !test.Required {
			continue
		}
		tail := outcome.Output
		if len(tail) > 3000 {
			tail = "..." + tail[len(tail)-3000:]
		}
		failures = append(failures, fmt.Sprintf("%s (%s):\n%s", test.Name, outcome.Run.State, tail))
	}
	return strings.Join(failures, "\n\n"), len(failures) == 0
}

// nextEffort is one step up the effort ladder, from the provider's default to high.
func nextEffort(e core.Effort) core.Effort {
	switch e {
	case core.EffortLow:
		return core.EffortMedium
	case core.EffortMedium:
		return core.EffortHigh
	case core.EffortHigh, core.EffortDefault:
		// The default is already high on current models, so stepping from it to high would
		// retry at the same depth and pay again for the same answer.
		return core.EffortXHigh
	default:
		return core.EffortMax
	}
}
