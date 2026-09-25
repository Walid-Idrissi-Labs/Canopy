package theme

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// reload forgets what was loaded and reads themes again, a person's own from dir.
func reload(t *testing.T, dir string) {
	t.Helper()
	t.Setenv(ThemesDirEnv, dir)
	Reload()
	t.Cleanup(func() { loadOnce, shipped, personal, problems = sync.Once{}, nil, nil, nil })
}

func TestEveryShippedThemeLoads(t *testing.T) {
	reload(t, t.TempDir())
	if got := Problems(); len(got) != 0 {
		t.Fatalf("shipped themes that do not load: %v", got)
	}
	for _, name := range []string{"canopy", "mono", "catppuccin", "dracula", "gruvbox", "nord", "solarized", "tokyonight"} {
		if _, ok := ByName(name); !ok {
			t.Errorf("%s is not among the themes: %v", name, Names())
		}
	}
}

const minimal = `{"name": "mine", "text": "#111111", "muted": "#222222", "accent": "#333333",
 "success": "#444444", "danger": "#555555", "warning": "#666666", "info": "#777777",
 "border": "#888888", "highlight": "#999999", "flame": "#aaaaaa", "flameCore": "#bbbbbb",
 "smoke": "#cccccc", "smokeFaint": "#dddddd", "codeKeyword": "#eeeeee", "codeString": "#ffffff",
 "codeComment": "#010101", "codeNumber": {"light": "#020202", "dark": "#030303"}}`

func TestAThemeFileIsCheckedColourByColour(t *testing.T) {
	p, err := Parse([]byte(minimal))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "mine" || p.Text != (Adaptive{Light: "#111111", Dark: "#111111"}) ||
		p.CodeNumber != (Adaptive{Light: "#020202", Dark: "#030303"}) || p.CodeFunction != nil {
		t.Fatalf("parsed %+v", p)
	}
	for name, data := range map[string]string{
		"a colour missing":    strings.Replace(minimal, `"danger": "#555555", `, "", 1),
		"a colour misspelled": strings.Replace(minimal, `"#555555"`, `"red"`, 1),
		"a short hex":         strings.Replace(minimal, `"#555555"`, `"#555"`, 1),
		"an unknown key":      strings.Replace(minimal, `"name": "mine"`, `"name": "mine", "script": "x"`, 1),
		"a name with spaces":  strings.Replace(minimal, `"mine"`, `"my theme"`, 1),
		"a name in capitals":  strings.Replace(minimal, `"mine"`, `"Mine"`, 1),
		"not JSON":            "name: mine",
	} {
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	_, err = Parse([]byte(strings.Replace(minimal, `"danger": "#555555", `, "", 1)))
	if err == nil || !strings.Contains(err.Error(), "danger is missing") {
		t.Errorf("a missing colour was not named: %v", err)
	}
}

// A person's themes add to the list. One that does not parse, or that takes a name already used,
// is skipped and said, and the rest still load.
func TestAPersonsOwnThemesLoadBesideTheShippedOnes(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("mine.json", minimal)
	write("broken.json", `{"name": "broken"}`)
	write("nord.json", strings.Replace(minimal, `"mine"`, `"nord"`, 1))
	write("notes.txt", "not a theme")
	reload(t, dir)

	if _, ok := ByName("mine"); !ok {
		t.Fatalf("a person's theme is not among %v", Names())
	}
	if nord, _ := ByName("nord"); nord.About == "" {
		t.Fatal("a person's theme replaced the shipped nord")
	}
	got := strings.Join(Problems(), "\n")
	if !strings.Contains(got, "broken.json") || !strings.Contains(got, "nord is already a theme") ||
		strings.Contains(got, "notes.txt") {
		t.Fatalf("problems:\n%s", got)
	}
}

// Contrast, as WCAG measures it, against the background each palette was made for, in both its
// light and dark forms. Text has to be comfortable to read; an outcome has to be told apart at a
// glance; quiet text and comments may be quieter, but never gone.
func TestEveryPaletteCanBeRead(t *testing.T) {
	reload(t, t.TempDir())
	for _, p := range All() {
		for _, dark := range []bool{false, true} {
			bg := pick(p.Background, dark)
			for _, c := range []struct {
				role string
				fg   color.Color
				min  float64
			}{
				{"text", p.Text, 4.5}, {"danger", p.Danger, 3}, {"success", p.Success, 3},
				{"warning", p.Warning, 3}, {"accent", p.Accent, 3}, {"info", p.Info, 3},
				{"muted", p.Muted, 3}, {"code comment", p.CodeComment, 2.5},
			} {
				if got := contrast(pick(c.fg.(Adaptive), dark), bg); got < c.min {
					t.Errorf("%s (dark %v): %s is %.2f:1 against %s, want %.1f", p.Name, dark, c.role, got, bg, c.min)
				}
			}
		}
	}
}

func pick(a Adaptive, dark bool) string {
	if dark {
		return a.Dark
	}
	return a.Light
}

func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(hex string) float64 {
	var r, g, b int
	_, _ = fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b)
	channel := func(v int) float64 {
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(r) + 0.7152*channel(g) + 0.0722*channel(b)
}

// The rule the package exists for: no colour is made anywhere else. A hex colour or a colour
// constructor outside this package is a colour no theme can change.
func TestNoColourIsMadeOutsideTheTheme(t *testing.T) {
	raw := regexp.MustCompile(`lipgloss\.Color\(|color\.RGBA\{|lipgloss\.ANSIColor\(|"#[0-9a-fA-F]{6}"`)
	for _, root := range []string{"..", filepath.Join("..", "..", "..", "cmd")} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && filepath.Base(path) == "theme" {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(data), "\n") {
				if raw.MatchString(line) {
					t.Errorf("%s:%d makes a colour outside the theme: %s", path, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
