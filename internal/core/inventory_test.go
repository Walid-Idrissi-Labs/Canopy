package core

import "testing"

// Reasoning is the thinking text a replayed reply carries, not its JSON: a plain reply full of
// characters that need escaping counts no reasoning at all, and signatures are left out.
func TestReasoningIsOnlyTheThinking(t *testing.T) {
	plain := Message{Role: RoleAssistant, Text: `a "quoted" <tag> & more` + "\n\n",
		Native: &Native{Provider: "anthropic", Data: []byte(`{"role":"assistant","content":[` +
			`{"type":"text","text":"a \"quoted\" <tag> & more\n\n"}]}`)}}
	thought := Message{Role: RoleAssistant, Text: "ok",
		Native: &Native{Provider: "anthropic", Data: []byte(`{"role":"assistant","content":[` +
			`{"type":"thinking","thinking":"sixteen letters!","signature":"` +
			"SIGNATURESIGNATURESIGNATURESIGNATURESIGNATURESIGNATURE" + `"},` +
			`{"type":"redacted_thinking","data":"12345678"},{"type":"text","text":"ok"}]}`)}}
	if inv := TakeInventory("", "", nil, []Message{plain}); inv.Reasoning != 0 {
		t.Errorf("a reply with no thinking counts %d reasoning tokens", inv.Reasoning)
	}
	if got := thought.reasoningBytes(); got != 16+8 {
		t.Errorf("reasoning is %d bytes; the thinking and redacted data are 24", got)
	}
}

// Reports from other agents ride on a user message and are part of what is asked.
func TestReportsAreCounted(t *testing.T) {
	m := Message{Role: RoleUser, Text: "go", Reports: []string{string(make([]byte, 4000))}}
	if inv := TakeInventory("", "", nil, []Message{m}); inv.Asked < 1000 {
		t.Errorf("a 4000-byte report counts %d tokens", inv.Asked)
	}
}
