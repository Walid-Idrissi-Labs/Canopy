package session

import (
	"context"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"strings"
	"testing"
)

// The judge sends every attempt's diff with its test result, asks for an opinion that calls itself
// one, and adds nothing to the conversation it borrows its model from.
func TestTheJudgeReadsEveryAttempt(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("parser special-cases the test input")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	t.Cleanup(e.Close)
	s := e.Create("claude", "claude-opus-5")
	opinion, err := e.Judge(context.Background(), s.ID, []core.JudgeCandidate{
		{Agent: "alpha", Tests: "passing", Diff: "+if input == \"12\" { return 12 }"},
		{Agent: "bravo", Tests: "failing", Diff: strings.Repeat("x", judgeDiffLimit+100)},
	})
	if err != nil || !strings.Contains(opinion, "special-cases") {
		t.Fatalf("opinion = %q, %v", opinion, err)
	}
	client.mu.Lock()
	sent, system := client.history, client.system
	client.mu.Unlock()
	text := sent[len(sent)-1].Text
	if !strings.Contains(text, "## alpha (tests: passing)") || !strings.Contains(text, "## bravo (tests: failing)") ||
		!strings.Contains(text, "the rest of this diff is left out") {
		t.Fatalf("the request did not carry both attempts, bounded:\n%.500s", text)
	}
	if !strings.Contains(system, "opinion, not verification") {
		t.Fatalf("the prompt does not say it is an opinion: %q", system)
	}
	if got, _ := e.Session(s.ID); len(got.Turns) != 0 {
		t.Fatal("judging added to the conversation")
	}
}
