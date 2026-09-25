package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// Long output costs a summary in the conversation, keeps the failures and the end visible, and
// stays fully readable through the handle.
func TestLongOutputIsSummarisedAndStillReadable(t *testing.T) {
	store := OutputStoreAt(t.TempDir())
	var out strings.Builder
	for i := 1; i <= 3000; i++ {
		fmt.Fprintf(&out, "ok   pkg/%d  0.01s\n", i)
		if i == 1500 {
			out.WriteString("--- FAIL: TestParser (0.00s)\n")
		}
	}
	out.WriteString("FAIL\n")
	summary := offload(store, out.String())
	if len(summary) > inlineOutputBytes+4096 {
		t.Fatalf("the summary is %d bytes; long output must not reach the conversation whole", len(summary))
	}
	if !strings.Contains(summary, "--- FAIL: TestParser") || !strings.HasSuffix(strings.TrimSpace(summary), "FAIL") {
		t.Fatalf("the failure or the ending is missing from the summary:\n%s", summary)
	}
	id := regexp.MustCompile(`handle "([0-9a-f]{16})"`).FindStringSubmatch(summary)
	if id == nil {
		t.Fatalf("no handle in the summary:\n%s", summary)
	}
	result, err := ReadOutputTool(store).Run(context.Background(),
		json.RawMessage(`{"handle":"`+id[1]+`","grep":"pkg/2999"}`))
	if err != nil || result.IsError || !strings.Contains(result.Content, "pkg/2999") {
		t.Fatalf("the middle is not readable through the handle: %v %+v", err, result)
	}
}

// A handle is a name the model was given, never a path it can choose.
func TestAHandleCannotNameAFile(t *testing.T) {
	result, _ := ReadOutputTool(OutputStoreAt(t.TempDir())).Run(context.Background(),
		json.RawMessage(`{"handle":"../../etc/passwd"}`))
	if !result.IsError {
		t.Fatalf("a path was accepted as a handle: %+v", result)
	}
}

func TestShortOutputIsSentWhole(t *testing.T) {
	if got := offload(OutputStoreAt(t.TempDir()), "all good"); got != "all good" {
		t.Fatalf("short output was altered: %q", got)
	}
}

// With nowhere to store it, long output is still bounded rather than sent whole.
func TestUnstorableOutputIsStillBounded(t *testing.T) {
	summary := offload(&OutputStore{}, strings.Repeat("line of output\n", 200_000))
	if len(summary) > 16*1024 {
		t.Fatalf("%d bytes reached the conversation when the store was unavailable", len(summary))
	}
	if !strings.Contains(summary, "could not be stored") {
		t.Fatalf("the summary does not say the rest is gone:\n%s", summary[:200])
	}
}
