package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The outline lists what each file declares, the file the rest of the code uses most first, and
// leaves out hidden and dependency directories.
func TestTheRepoMapOutlinesAndRanks(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"core/store.go":             "package core\n\ntype Store struct{}\n\nfunc (s *Store) Load(key string) ([]byte, error) { return nil, nil }\n\nfunc NewStore() *Store { return &Store{} }\n",
		"cmd/a.go":                  "package main\n\nfunc useA() { core.NewStore().Load(\"a\") }\n",
		"cmd/b.go":                  "package main\n\nfunc useB() { _ = core.NewStore() }\n",
		"web/app.ts":                "export function renderPage(x: number): string { return '' }\nconst local = 1\nexport class Widget {}\n",
		"tools/slug.py":             "class Slugger:\n    def slugify(self, text):\n        return text\n",
		"node_modules/dep/index.js": "export function hidden() {}\n",
		".secret/keys.go":           "package keys\n\nfunc Leak() {}\n",
	})
	w, err := OpenWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := RepoMapTool(w).Run(context.Background(), nil)
	if err != nil || result.IsError {
		t.Fatalf("%v %s", err, result.Content)
	}
	out := result.Content
	for _, want := range []string{
		"func (s *Store) Load(key string) ([]byte, error)", "type Store struct", "func NewStore() *Store",
		"export function renderPage(x: number): string", "export class Widget", "class Slugger", "def slugify(self, text)",
		"used by 2 files",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the outline lacks %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"hidden", "Leak", "const local"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("the outline has %q:\n%s", unwanted, out)
		}
	}
	if !strings.HasPrefix(out, "core/store.go") {
		t.Errorf("the most used file is not first:\n%s", out)
	}
}

// The budget holds: a large tree is cut off with a note saying how to see the rest.
func TestTheRepoMapKeepsToItsBudget(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 300; i++ {
		files[filepath.Join("pkg", strings.Repeat("x", 3)+string(rune('a'+i%26))+strings.Repeat("y", i%7), "f"+string(rune('a'+i/26))+".go")] =
			"package p\n\nfunc SomethingLongEnoughToCount" + string(rune('A'+i%26)) + "() {}\n"
	}
	w, err := OpenWorkspace(writeTree(t, files))
	if err != nil {
		t.Fatal(err)
	}
	result, _ := RepoMapTool(w).Run(context.Background(), []byte(`{"max_tokens":500}`))
	if len(result.Content) > 500*4+200 || !strings.Contains(result.Content, "more files not shown") {
		t.Fatalf("%d bytes for a 500-token budget:\n%s", len(result.Content), result.Content)
	}
}
