package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

func toolset(t *testing.T) (*Workspace, map[string]core.Tool) {
	t.Helper()

	w := testWorkspace(t)
	tools := map[string]core.Tool{}
	for _, tool := range FileTools(w) {
		tools[tool.Name()] = tool
	}
	return w, tools
}

func call(t *testing.T, tool core.Tool, args any) core.ToolResult {
	t.Helper()

	input, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshalling arguments: %v", err)
	}
	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("%s returned a Go error rather than a result the model can act on: %v",
			tool.Name(), err)
	}
	return result
}

func TestReadingAFile(t *testing.T) {
	_, tools := toolset(t)

	result := call(t, tools["read_file"], map[string]string{"path": "main.go"})
	if result.IsError {
		t.Fatalf("read failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "package main") {
		t.Errorf("content = %q", result.Content)
	}
	// A model that can see line numbers describes a place in a file accurately far more often than
	// one counting newlines in its head.
	if !strings.Contains(result.Content, "1\t") {
		t.Errorf("lines are not numbered: %q", result.Content)
	}
}

// The whole point of the read ledger. Applying an edit against content that has moved is how an
// agent silently destroys work, including another agent's.
func TestAnEditAgainstAFileThatMovedIsRefused(t *testing.T) {
	w, tools := toolset(t)

	if r := call(t, tools["read_file"], map[string]string{"path": "main.go"}); r.IsError {
		t.Fatalf("read: %s", r.Content)
	}

	// Somebody else changes the file. Another agent, the user's editor, a rebase.
	if err := os.WriteFile(filepath.Join(w.Root(), "main.go"),
		[]byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result := call(t, tools["edit_file"], map[string]string{
		"path": "main.go", "old_text": "package main", "new_text": "package other",
	})
	if !result.IsError {
		t.Fatal("an edit against a file that changed since it was read was applied blind")
	}
	if !strings.Contains(result.Content, "changed since you read it") {
		t.Errorf("the model needs to be told what to do about it, got %q", result.Content)
	}

	// And the file is untouched by the refused edit.
	after, err := os.ReadFile(filepath.Join(w.Root(), "main.go"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(after), "package main") {
		t.Error("the refused edit was partially applied")
	}
}

// "Never read it" and "read it and it moved" need different words, because the fix is different.
func TestEditingWithoutReadingIsRefused(t *testing.T) {
	_, tools := toolset(t)

	result := call(t, tools["edit_file"], map[string]string{
		"path": "main.go", "old_text": "package main", "new_text": "package other",
	})
	if !result.IsError {
		t.Fatal("editing a file that was never read should be refused")
	}
	if !strings.Contains(result.Content, "read main.go before editing") {
		t.Errorf("got %q", result.Content)
	}
}

func TestASuccessfulEdit(t *testing.T) {
	w, tools := toolset(t)

	if r := call(t, tools["read_file"], map[string]string{"path": "main.go"}); r.IsError {
		t.Fatalf("read: %s", r.Content)
	}
	result := call(t, tools["edit_file"], map[string]string{
		"path": "main.go", "old_text": "package main", "new_text": "package changed",
	})
	if result.IsError {
		t.Fatalf("edit failed: %s", result.Content)
	}

	content, err := os.ReadFile(filepath.Join(w.Root(), "main.go"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(content), "package changed") {
		t.Errorf("the edit was not applied: %q", content)
	}

	// A second edit works without re reading, since the tool has just told the model exactly what
	// changed. Forcing a re read between every edit would triple the cost of a multi line change.
	second := call(t, tools["edit_file"], map[string]string{
		"path": "main.go", "old_text": "package changed", "new_text": "package again",
	})
	if second.IsError {
		t.Errorf("a second consecutive edit was refused: %s", second.Content)
	}
}

// Replacing the first is a guess about which one was meant, and replacing all is a different edit
// from the one asked for.
func TestAnAmbiguousEditIsRefused(t *testing.T) {
	w, tools := toolset(t)

	path := filepath.Join(w.Root(), "repeated.go")
	if err := os.WriteFile(path, []byte("x := 1\ny := 2\nx := 1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if r := call(t, tools["read_file"], map[string]string{"path": "repeated.go"}); r.IsError {
		t.Fatalf("read: %s", r.Content)
	}
	result := call(t, tools["edit_file"], map[string]string{
		"path": "repeated.go", "old_text": "x := 1", "new_text": "x := 99",
	})
	if !result.IsError {
		t.Fatal("an edit matching two places was applied to one of them")
	}
	if !strings.Contains(result.Content, "appears 2 times") {
		t.Errorf("the model needs to know why, got %q", result.Content)
	}
	if !strings.Contains(result.Content, "more surrounding context") {
		t.Errorf("the model needs to know how to fix it, got %q", result.Content)
	}
}

func TestAnEditThatMatchesNothing(t *testing.T) {
	_, tools := toolset(t)

	if r := call(t, tools["read_file"], map[string]string{"path": "main.go"}); r.IsError {
		t.Fatalf("read: %s", r.Content)
	}
	result := call(t, tools["edit_file"], map[string]string{
		"path": "main.go", "old_text": "this text is not there", "new_text": "x",
	})
	if !result.IsError {
		t.Fatal("an edit matching nothing should be refused")
	}
	if !strings.Contains(result.Content, "indentation") {
		t.Errorf("whitespace is the usual cause and the message should say so, got %q", result.Content)
	}
}

func TestWritingANewFile(t *testing.T) {
	w, tools := toolset(t)

	result := call(t, tools["write_file"], map[string]string{
		"path": "internal/core/new.go", "content": "package core\n",
	})
	if result.IsError {
		t.Fatalf("write failed: %s", result.Content)
	}

	content, err := os.ReadFile(filepath.Join(w.Root(), "internal", "core", "new.go"))
	if err != nil {
		t.Fatalf("the file was not created: %v", err)
	}
	if string(content) != "package core\n" {
		t.Errorf("content = %q", content)
	}
}

// Overwriting a file wholesale is the destructive case, so it gets the same rule as an edit.
func TestOverwritingAnUnreadFileIsRefused(t *testing.T) {
	w, tools := toolset(t)

	result := call(t, tools["write_file"], map[string]string{
		"path": "main.go", "content": "everything else is gone now",
	})
	if !result.IsError {
		t.Fatal("an existing file was replaced without ever having been read")
	}

	content, err := os.ReadFile(filepath.Join(w.Root(), "main.go"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(content), "package main") {
		t.Error("the file was overwritten anyway")
	}
}

// Every path a tool touches goes through the workspace, or confinement is one tool away from a bug
// that lets an agent write outside its worktree.
func TestEveryFileToolIsConfined(t *testing.T) {
	_, tools := toolset(t)

	cases := []struct {
		tool string
		args map[string]string
	}{
		{"read_file", map[string]string{"path": "../../../etc/passwd"}},
		{"write_file", map[string]string{"path": "../escaped.txt", "content": "no"}},
		{"edit_file", map[string]string{
			"path": "../escaped.txt", "old_text": "a", "new_text": "b",
		}},
	}

	for _, tc := range cases {
		result := call(t, tools[tc.tool], tc.args)
		if !result.IsError {
			t.Errorf("%s allowed a path outside the workspace", tc.tool)
		}
		if !strings.Contains(result.Content, "outside") {
			t.Errorf("%s: the refusal should say why, got %q", tc.tool, result.Content)
		}
	}
}

// filepath.Match has no ** at all, so the pattern every model reaches for first matches nothing,
// and a tool whose most obvious input silently returns nothing is one a model concludes the
// codebase is empty from.
func TestGlobSupportsTheDoubleStarModelsActuallyUse(t *testing.T) {
	w, tools := toolset(t)

	for _, path := range []string{"a.go", "internal/b.go", "internal/core/c.go"} {
		full := filepath.Join(w.Root(), path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte("package x\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	result := call(t, tools["glob"], map[string]string{"pattern": "**/*.go"})
	if result.IsError {
		t.Fatalf("glob failed: %s", result.Content)
	}
	for _, want := range []string{"a.go", "internal/b.go", "internal/core/c.go"} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("**/*.go did not find %s:\n%s", want, result.Content)
		}
	}

	// And a scoped pattern finds only what is under it.
	result = call(t, tools["glob"], map[string]string{"pattern": "internal/**/*.go"})
	if strings.Contains(result.Content, "a.go\n") && !strings.Contains(result.Content, "internal") {
		t.Errorf("internal/**/*.go matched something outside internal:\n%s", result.Content)
	}
}

// A model pointed at node_modules would get sixty thousand paths, which is expensive and useless in
// equal measure.
func TestGlobSkipsTheDirectoriesNobodyMeans(t *testing.T) {
	w, tools := toolset(t)

	noise := filepath.Join(w.Root(), "node_modules", "left-pad")
	if err := os.MkdirAll(noise, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(noise, "index.go"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result := call(t, tools["glob"], map[string]string{"pattern": "**/*.go"})
	if strings.Contains(result.Content, "node_modules") {
		t.Errorf("node_modules was searched:\n%s", result.Content)
	}
}

func TestGrepFindsALineAndSaysWhere(t *testing.T) {
	w, tools := toolset(t)

	if err := os.WriteFile(filepath.Join(w.Root(), "target.go"),
		[]byte("package x\n\nfunc distinctive() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result := call(t, tools["grep"], map[string]string{"query": "distinctive"})
	if result.IsError {
		t.Fatalf("grep failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "target.go:3") {
		t.Errorf("grep should say which file and line, got:\n%s", result.Content)
	}

	empty := call(t, tools["grep"], map[string]string{"query": "nothing matches this"})
	if empty.IsError {
		t.Error("finding nothing is a result, not a failure")
	}
	if !strings.Contains(empty.Content, "No matches") {
		t.Errorf("got %q", empty.Content)
	}
}

// A match inside a compiled object is never what somebody was looking for, and printing the line
// around it fills the context with bytes that are not text.
func TestGrepSkipsBinaryFiles(t *testing.T) {
	w, tools := toolset(t)

	binary := append([]byte("findme"), 0x00, 0x01, 0x02)
	if err := os.WriteFile(filepath.Join(w.Root(), "compiled.bin"), binary, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result := call(t, tools["grep"], map[string]string{"query": "findme"})
	if strings.Contains(result.Content, "compiled.bin") {
		t.Errorf("a binary file was searched:\n%s", result.Content)
	}
}

// A failure the model could recover from is information, not a fault. Returning a Go error would
// end the turn; returning a result lets the model try a different path.
func TestToolFailuresComeBackAsResultsNotErrors(t *testing.T) {
	_, tools := toolset(t)

	for name, args := range map[string]map[string]string{
		"read_file":  {"path": "does-not-exist.go"},
		"edit_file":  {"path": "does-not-exist.go", "old_text": "a", "new_text": "b"},
		"glob":       {"pattern": "["},
		"grep":       {"query": ""},
		"write_file": {"path": "", "content": "x"},
	} {
		result := call(t, tools[name], args)
		if !result.IsError {
			t.Errorf("%s reported success on input it could not handle", name)
		}
		if result.Content == "" {
			t.Errorf("%s failed without saying why", name)
		}
	}
}

// Every tool sends its schema to the provider and has it checked locally from the same declaration.
func TestEveryFileToolDeclaresAUsableSchema(t *testing.T) {
	w := testWorkspace(t)

	for _, tool := range FileTools(w) {
		if tool.Description() == "" {
			t.Errorf("%s has no description, so the model will not know when to use it", tool.Name())
		}
		if !tool.Kind().Valid() {
			t.Errorf("%s has kind %q, which no permission rule covers", tool.Name(), tool.Kind())
		}

		var schema map[string]any
		if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
			t.Errorf("%s has a schema that is not valid JSON: %v", tool.Name(), err)
			continue
		}
		if schema["type"] != "object" {
			t.Errorf("%s takes %v, and every provider in scope expects an object", tool.Name(), schema["type"])
		}
		if _, ok := schema["required"]; !ok {
			t.Errorf("%s declares nothing required, so a call with no arguments would reach it",
				tool.Name())
		}

		// And the schema has to agree with what the tool actually validates, which is the whole
		// reason there is one declaration rather than two.
		if err := core.ValidateToolInput(tool.Schema(), json.RawMessage(`{}`)); err == nil {
			t.Errorf("%s accepts an empty call despite declaring required arguments", tool.Name())
		}
	}
}
