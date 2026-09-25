package session

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// judgeDiffLimit bounds each attempt's diff, so several large attempts still fit one request.
const judgeDiffLimit = 12000

// judgePrompt asks for an opinion and says that it is one. Evidence already ranked the attempts; what
// a model reading the diffs adds is a second look at what evidence cannot see: a test edited to pass,
// an input special-cased, a failure silenced, and a preference between attempts the tests call equal.
const judgePrompt = `You review several attempts at the same task, each by a different agent. Their test results are ` +
	`evidence and are given to you; do not re-verify them. For each attempt, say in one or two sentences ` +
	`whether the change could be passing its tests without doing the work: editing or deleting tests, ` +
	`special-casing the inputs tests use, skipping or silencing failures, hard-coding expected output. ` +
	`Point at the lines. Then, among the attempts whose tests pass, say which you would choose and why, in ` +
	`two sentences. This is an opinion, not verification: say where you are unsure, and never call an ` +
	`attempt correct.`

// Judge asks the model a conversation runs on for an opinion of several agents' attempts at one task.
// One request, no tools and nothing added to any conversation; the answer is shown as an opinion
// beside the evidence, never in its place.
func (e *Engine) Judge(ctx context.Context, sessionID string, candidates []core.JudgeCandidate) (string, error) {
	if len(candidates) == 0 {
		return "", errors.New("there are no attempts to compare")
	}
	e.mu.Lock()
	stored, ok := e.sessions[sessionID]
	var keyName, model string
	if ok {
		keyName, model = stored.KeyName, stored.Model
	}
	resolver := e.resolver
	e.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("there is no conversation %s", sessionID)
	}
	client, _, err := resolver.Resolve(keyName, model)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	for _, c := range candidates {
		diff := c.Diff
		if len(diff) > judgeDiffLimit {
			diff = diff[:judgeDiffLimit] + "\n... (the rest of this diff is left out)"
		}
		if strings.TrimSpace(diff) == "" {
			diff = "(no changes)"
		}
		fmt.Fprintf(&b, "## %s (tests: %s)\n\n```diff\n%s\n```\n\n", c.Agent, c.Tests, diff)
	}
	stream, err := client.Stream(ctx, core.Request{
		Model:     model,
		System:    judgePrompt,
		Messages:  []core.Message{{Role: core.RoleUser, Text: strings.TrimSpace(b.String())}},
		MaxTokens: 2000,
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = stream.Close() }()
	var answer strings.Builder
	for stream.Next() {
		if event := stream.Event(); event.Kind == core.EventText {
			answer.WriteString(event.Text)
		}
	}
	if err := stream.Err(); err != nil {
		return "", err
	}
	if text := strings.TrimSpace(answer.String()); text != "" {
		return text, nil
	}
	return "", errors.New("the model answered with nothing")
}
