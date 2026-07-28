package tui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// No package outside internal/tui/theme may construct a colour.
//
// That rule is the first thing the theme package says, and it was broken twice: internal/tui/styles.go
// and internal/tui/keys/model.go each declared their own adaptive colours, duplicating the default
// palette by value. Both looked correct, because the values matched. Both meant that selecting a
// theme changed some screens and not others, which is worse than having one theme.
//
// Neither was found by looking at the interface, because a screen that ignores the theme looks
// exactly like it always has. The first was found by reading, and the second only because the first
// prompted a grep. A third would be found the same way or not at all, which is what this replaces.
//
// The check is textual rather than a type check on purpose. A colour can be constructed in more ways
// than an analysis of this size can follow, and the thing worth forbidding is the shape: a hex
// string appearing anywhere except the one file that is allowed to hold them.
func TestOnlyTheThemePackageConstructsColours(t *testing.T) {
	// Where colour is allowed to be spelled out. Everything else asks the theme for it.
	allowed := map[string]bool{
		filepath.Join("theme", "theme.go"): true,
	}

	forbidden := []string{"AdaptiveColor{", "lipgloss.Color(", "termenv.RGBColor"}

	root := "."
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		switch {
		case err != nil:
			return err
		case info.IsDir(), !strings.HasSuffix(path, ".go"), strings.HasSuffix(path, "_test.go"):
			return nil
		}

		relative := strings.TrimPrefix(path, "./")
		if allowed[relative] {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, shape := range forbidden {
			if strings.Contains(string(content), shape) {
				t.Errorf("%s constructs a colour with %s.\n"+
					"Every colour belongs in internal/tui/theme, named for what it means rather than "+
					"what it looks like, or this file will stop following the selected theme and "+
					"nobody will notice, because it will keep looking the way it always did.",
					relative, shape)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the interface tree: %v", err)
	}
}
