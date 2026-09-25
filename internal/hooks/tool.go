package hooks

// Hooks around a tool call and at the end of a turn.
//
// These talk JSON: the hook is given what happened on its standard input, and may answer on its
// standard output. A pre-tool hook answers {"decision": "deny", "reason": "..."} to refuse a call, or
// exits 2 with the reason on stderr; a post-tool hook answers {"note": "..."} to add a line to what
// the model is told of the result. Nothing a hook says can approve anything: the permission layer
// has already decided by the time a pre-tool hook runs, and a hook can only take a yes away.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/sandbox"
)

// Answer is what a JSON hook ran to: its stdout, its stderr and how it ended.
type Answer struct {
	Stdout, Stderr string
	ExitCode       int
	// Failed says the hook could not give an answer at all: it could not start, timed out or was
	// stopped.
	Failed string
}

// JSONExecutor runs a hook with input on its stdin.
type JSONExecutor func(ctx context.Context, command, dir string, timeout time.Duration, input []byte) Answer

// ConfinedJSON runs a JSON hook in the sandbox confine gives for its directory, like Confined.
func ConfinedJSON(confine func(dir string) (*sandbox.Policy, []string)) JSONExecutor {
	return func(ctx context.Context, command, dir string, timeout time.Duration, input []byte) Answer {
		policy, base := confine(dir)
		result, err := exec.Run(ctx, shellPath(), []string{"-c", command}, exec.Options{
			Dir: dir, Env: withInherited(base), Sandbox: policy, Timeout: timeout,
			Stdin: input, SplitStderr: true, MaxOutput: 64 << 10,
		})
		switch {
		case err != nil:
			return Answer{Failed: err.Error()}
		case !result.Ran:
			return Answer{Failed: "it could not be run: " + strings.TrimSpace(result.Output)}
		case result.TimedOut:
			return Answer{Failed: "it timed out"}
		case result.Cancelled:
			return Answer{Failed: "it was stopped"}
		}
		return Answer{Stdout: result.Output, Stderr: result.Stderr, ExitCode: result.ExitCode}
	}
}

// defaultToolHookTimeout bounds a hook that names no timeout. Short, since a pre-tool hook holds up
// the call it is about.
const defaultToolHookTimeout = 30 * time.Second

// maxResultForHook is how much of a tool's result a post-tool hook is given.
const maxResultForHook = 16 << 10

// ToolRunner runs a project's pre-tool, post-tool and turn-end hooks.
type ToolRunner struct {
	pre, post, turnEnd []Hook
	dir                string
	exec               JSONExecutor
	report             func(Report)
	running            sync.WaitGroup
}

// NewToolRunner builds the runner for the tool and turn hooks among hooks, or nil when there are
// none. Reports go to report, as the other hooks' do.
func NewToolRunner(hooks []Hook, dir string, exec JSONExecutor, report func(Report)) *ToolRunner {
	r := &ToolRunner{dir: dir, exec: exec, report: report}
	for _, h := range hooks {
		switch h.On {
		case PreTool:
			r.pre = append(r.pre, h)
		case PostTool:
			r.post = append(r.post, h)
		case TurnEnd:
			r.turnEnd = append(r.turnEnd, h)
		}
	}
	if len(r.pre)+len(r.post)+len(r.turnEnd) == 0 {
		return nil
	}
	return r
}

// callInput is what a tool hook is told of the call.
type callInput struct {
	Event     Event           `json:"event"`
	Session   string          `json:"session"`
	Agent     string          `json:"agent"`
	Tool      string          `json:"tool"`
	CallID    string          `json:"call_id,omitempty"`
	Kind      string          `json:"kind"`
	Arguments json.RawMessage `json:"arguments"`
	Paths     []string        `json:"paths,omitempty"`
	Command   string          `json:"command,omitempty"`
	Tainted   bool            `json:"tainted"`
	Result    *resultInput    `json:"result,omitempty"`
}

type resultInput struct {
	Content   string `json:"content"`
	IsError   bool   `json:"is_error"`
	Truncated bool   `json:"truncated,omitempty"`
}

func (r *ToolRunner) input(event Event, req permission.Request) callInput {
	args := json.RawMessage(req.Arguments)
	if !json.Valid(args) {
		args, _ = json.Marshal(req.Arguments)
	}
	return callInput{Event: event, Session: req.SessionID, Agent: req.AgentID, Tool: req.Tool,
		CallID: req.CallID, Kind: string(req.Kind), Arguments: args, Paths: req.Paths,
		Command: req.Command, Tainted: req.Tainted}
}

func matches(h Hook, tool string) bool {
	if len(h.Tools) == 0 {
		return true
	}
	for _, name := range h.Tools {
		if name == tool {
			return true
		}
	}
	return false
}

func timeoutOf(h Hook) time.Duration {
	if h.Timeout > 0 {
		return h.Timeout
	}
	return defaultToolHookTimeout
}

// Before runs the pre-tool hooks for a call, and returns why it is refused, or "" to let it run.
// The first refusal stands, and the hooks after it are not run.
func (r *ToolRunner) Before(ctx context.Context, req permission.Request) string {
	if r == nil {
		return ""
	}
	input, _ := json.Marshal(r.input(PreTool, req))
	for _, h := range r.pre {
		if !matches(h, req.Tool) {
			continue
		}
		answer := r.exec(ctx, h.Run, r.dir, timeoutOf(h), input)
		why, failed := preToolVerdict(answer)
		if failed != "" {
			r.tell(Report{Event: PreTool, Subject: req.Tool, Command: h.Run, Err: fmt.Errorf("%s", failed)})
		}
		if why != "" {
			return why
		}
	}
	return ""
}

// preToolVerdict reads a pre-tool hook's answer: the reason for a refusal, if any, and what went
// wrong, if anything did. A hook that could not answer refuses, since a guard that fails open is
// one that stops guarding without anybody noticing.
func preToolVerdict(a Answer) (refusal, failed string) {
	if a.Failed != "" {
		return "the pre-tool hook could not give an answer (" + a.Failed + "), so the call was not run", a.Failed
	}
	switch a.ExitCode {
	case 0:
	case 2:
		reason := strings.TrimSpace(a.Stderr)
		if reason == "" {
			reason = "a pre-tool hook refused it"
		}
		return short(reason), ""
	default:
		failed := fmt.Sprintf("it exited %d", a.ExitCode)
		return "the pre-tool hook failed (" + failed + "), so the call was not run", failed
	}
	out := strings.TrimSpace(a.Stdout)
	if out == "" {
		return "", ""
	}
	var said struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &said); err != nil {
		return "the pre-tool hook's answer is not JSON, so the call was not run", "its answer is not JSON"
	}
	switch strings.ToLower(said.Decision) {
	case "", "allow", "approve":
		// No objection. "allow" takes nothing further: the permission layer has already decided.
		return "", ""
	case "deny", "block", "refuse":
		reason := strings.TrimSpace(said.Reason)
		if reason == "" {
			reason = "a pre-tool hook refused it"
		}
		return short(reason), ""
	default:
		return "the pre-tool hook answered " + short(said.Decision) + ", which is not allow or deny, " +
			"so the call was not run", "it answered " + short(said.Decision)
	}
}

// After runs the post-tool hooks for a call and returns the notes they add, one per line.
func (r *ToolRunner) After(ctx context.Context, req permission.Request, result core.ToolResult) string {
	if r == nil {
		return ""
	}
	in := r.input(PostTool, req)
	content := result.Content
	truncated := len(content) > maxResultForHook
	if truncated {
		content = content[:maxResultForHook]
	}
	in.Result = &resultInput{Content: content, IsError: result.IsError, Truncated: truncated}
	input, _ := json.Marshal(in)
	var notes []string
	for _, h := range r.post {
		if !matches(h, req.Tool) {
			continue
		}
		answer := r.exec(ctx, h.Run, r.dir, timeoutOf(h), input)
		if answer.Failed != "" || answer.ExitCode != 0 {
			failed := answer.Failed
			if failed == "" {
				failed = fmt.Sprintf("it exited %d", answer.ExitCode)
			}
			r.tell(Report{Event: PostTool, Subject: req.Tool, Command: h.Run, Err: fmt.Errorf("%s", failed)})
			continue
		}
		var said struct {
			Note string `json:"note"`
		}
		out := strings.TrimSpace(answer.Stdout)
		if out == "" {
			continue
		}
		if err := json.Unmarshal([]byte(out), &said); err != nil {
			r.tell(Report{Event: PostTool, Subject: req.Tool, Command: h.Run, Err: fmt.Errorf("its answer is not JSON")})
			continue
		}
		if note := strings.TrimSpace(said.Note); note != "" {
			notes = append(notes, short(note))
		}
	}
	return strings.Join(notes, "\n")
}

// TurnEnded runs the turn-end hooks, in the background: nothing waits on them.
func (r *ToolRunner) TurnEnded(sessionID string, turn core.Turn) {
	if r == nil || len(r.turnEnd) == 0 {
		return
	}
	text := turn.Text
	if len(text) > maxResultForHook {
		text = text[:maxResultForHook]
	}
	input, _ := json.Marshal(map[string]any{"event": TurnEnd, "session": sessionID, "turn": turn.ID,
		"state": turn.State, "error": turn.Error, "text": text, "tool_calls": len(turn.ToolCalls)})
	for _, h := range r.turnEnd {
		r.running.Add(1)
		go func() {
			defer r.running.Done()
			answer := r.exec(context.Background(), h.Run, r.dir, timeoutOf(h), input)
			if answer.Failed != "" || answer.ExitCode != 0 {
				failed := answer.Failed
				if failed == "" {
					failed = fmt.Sprintf("it exited %d", answer.ExitCode)
				}
				r.tell(Report{Event: TurnEnd, Subject: sessionID, Command: h.Run, Err: fmt.Errorf("%s", failed)})
			}
		}()
	}
}

// Wait returns once every turn-end hook started has finished.
func (r *ToolRunner) Wait() {
	if r != nil {
		r.running.Wait()
	}
}

func (r *ToolRunner) tell(report Report) {
	if r.report != nil {
		r.report(report)
	}
}

// short keeps what a hook says to a length a transcript can hold.
func short(s string) string {
	const limit = 2000
	if len(s) > limit {
		return s[:limit] + "..."
	}
	return s
}
