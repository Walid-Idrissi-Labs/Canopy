package copilot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Reading this package's own source, which two of its promises are only checkable that way.
//
// "Nothing listens on a port" and "Canopy does not impersonate another editor" are properties of the
// code rather than of any value it computes, so the only way to hold them is to look. A comment
// saying so is a comment a later edit walks past.

type goFile struct {
	name string
	body string
}

func goFilesIn(t *testing.T, dir string) []goFile {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	files := make([]goFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		files = append(files, goFile{name: entry.Name(), body: string(body)})
	}
	if len(files) == 0 {
		t.Fatalf("no Go files found in %s, so this test proves nothing", dir)
	}
	return files
}

// Everything in this repository is plain ASCII, and a device code, a login and a vendor's error
// message all pass through here from places that are not.
func TestThisPackageIsPlainASCII(t *testing.T) {
	for _, file := range goFilesIn(t, ".") {
		for i, r := range file.body {
			if r > 127 {
				t.Errorf("%s carries a non-ASCII rune at byte %d", file.name, i)
				break
			}
		}
	}
}
