package core

import (
	"encoding/json"
	"strings"
)

// SummaryHeading opens the message that stands in for compacted turns.
const SummaryHeading = "Summary of the earlier part of this conversation:\n\n"

// Inventory is what one request carries, part by part, in estimated tokens. It answers "where do
// my tokens go" with the same pieces the request is built from, so what it shows is what is sent.
type Inventory struct {
	System       int
	Instructions int
	Tools        int
	ToolCount    int
	Summary      int
	// Asked is what the person wrote, with Canopy's notes and other agents' reports on it;
	// Answered the model's replies.
	Asked    int
	Answered int
	// Reasoning is the thinking replayed with earlier replies: the text of thinking blocks and the
	// data of redacted ones, signatures and framing left out.
	Reasoning int
	Calls     int
	Results   int
	Messages  int
}

// Total is the whole request.
func (i Inventory) Total() int {
	return i.System + i.Instructions + i.Tools + i.Summary + i.Asked + i.Answered + i.Reasoning +
		i.Calls + i.Results
}

// TakeInventory measures a request from its parts: the core prompt, the project's instructions
// block as appended to it, the tool definitions and the history.
func TakeInventory(system, instructions string, tools []ToolDefinition, history []Message) Inventory {
	inv := Inventory{
		System:       estimateTokens(len(system)),
		Instructions: estimateTokens(len(instructions)),
		ToolCount:    len(tools),
		Messages:     len(history),
	}
	var toolBytes int
	for _, tool := range tools {
		toolBytes += len(tool.Name) + len(tool.Description) + len(tool.InputSchema)
	}
	inv.Tools = estimateTokens(toolBytes)

	var asked, answered, reasoning, calls, results int
	for i, m := range history {
		if i == 0 && m.Role == RoleUser && strings.HasPrefix(m.Text, SummaryHeading) {
			inv.Summary = estimateTokens(len(m.Text))
			continue
		}
		var callBytes int
		for _, call := range m.ToolCalls {
			callBytes += len(call.Name) + len(call.Input)
		}
		for _, result := range m.ToolResults {
			results += len(result.Content)
		}
		calls += callBytes
		if m.Role == RoleAssistant {
			answered += len(m.Text)
			reasoning += m.reasoningBytes()
			continue
		}
		asked += len(m.Text) + len(m.Note)
		for _, report := range m.Reports {
			asked += len(report)
		}
	}
	inv.Asked, inv.Answered = estimateTokens(asked), estimateTokens(answered)
	inv.Reasoning, inv.Calls, inv.Results = estimateTokens(reasoning), estimateTokens(calls), estimateTokens(results)
	return inv
}

// reasoningBytes is the thinking a natively replayed reply carries: the text of its thinking
// blocks and the data of redacted ones. Signatures, escaping and framing are left out, since they
// are not what the model reads back.
func (m Message) reasoningBytes() int {
	if m.Native == nil {
		return 0
	}
	var msg struct {
		Content []struct {
			Type     string `json:"type"`
			Thinking string `json:"thinking"`
			Data     string `json:"data"`
		} `json:"content"`
	}
	if json.Unmarshal(m.Native.Data, &msg) != nil {
		return 0
	}
	var n int
	for _, block := range msg.Content {
		switch block.Type {
		case "thinking":
			n += len(block.Thinking)
		case "redacted_thinking":
			n += len(block.Data)
		}
	}
	return n
}
