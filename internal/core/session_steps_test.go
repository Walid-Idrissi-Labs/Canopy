package core

import (
	"strings"
	"testing"
	"time"
)

// Turns kept verbatim across a compaction lose their reasoning blocks, which are bound to the
// conversation the summary replaced; turns after the compaction keep theirs.
func TestReasoningFromBeforeACompactionIsNotReplayed(t *testing.T) {
	native := func(sig string) *Native {
		return &Native{Provider: "anthropic", Data: []byte(`{"role":"assistant","content":[` +
			`{"type":"thinking","thinking":"t","signature":"` + sig + `"},{"type":"text","text":"ok"}]}`)}
	}
	at := time.Unix(1000, 0)
	s := Session{
		Turns: []Turn{
			{Request: Message{Role: RoleUser, Text: "a"}, StartedAt: at.Add(-3 * time.Second),
				Steps: []Message{{Role: RoleAssistant, Text: "ok", Native: native("OLD1")}}},
			{Request: Message{Role: RoleUser, Text: "b"}, StartedAt: at.Add(-2 * time.Second),
				Steps: []Message{{Role: RoleAssistant, Text: "ok", Native: native("KEPT")}}},
			{Request: Message{Role: RoleUser, Text: "c"}, StartedAt: at.Add(time.Second),
				Steps: []Message{{Role: RoleAssistant, Text: "ok", Native: native("NEW")}}},
		},
		Compactions: []Compaction{{Summary: "earlier", Through: 1, At: at}},
	}
	var sent strings.Builder
	for _, m := range s.History() {
		if m.Native != nil {
			sent.Write(m.Native.Data)
		}
	}
	got := sent.String()
	if strings.Contains(got, "OLD1") || strings.Contains(got, "KEPT") {
		t.Fatalf("reasoning from before the compaction was replayed: %s", got)
	}
	if !strings.Contains(got, "NEW") || !strings.Contains(got, `"text":"ok"`) {
		t.Fatalf("a later turn lost its reasoning, or a kept turn lost its text: %s", got)
	}
}
