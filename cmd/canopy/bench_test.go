package main

import "testing"

// The attempt's usage comes from the result line a JSON run ends with, whatever came before it.
func TestTheBenchReadsTheRunsResultLine(t *testing.T) {
	out := []byte(`{"type":"text","text":"working"}` + "\n" + `not json` + "\n" +
		`{"type":"result","usage":{"input_tokens":120,"output_tokens":30,"cache_read_tokens":900},` +
		`"cost_usd":0.02,"tool_calls":4}` + "\n")
	usage, ok := lastResult(out)
	if !ok || usage.InputTokens != 120 || usage.CacheReadTokens != 900 || usage.CostUSD != 0.02 || usage.ToolCalls != 4 {
		t.Fatalf("usage = %+v, %v", usage, ok)
	}
	if _, ok := lastResult([]byte("crashed\n")); ok {
		t.Fatal("a run with no result line was read as one")
	}
}
