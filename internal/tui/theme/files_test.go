package theme

import (
	"errors"
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// reload forgets what was loaded and reads themes again, a person's own from dir.
func reload(t *testing.T, dir string) {
	t.Helper()
	t.Setenv(ThemesDirEnv, dir)
	Reload()
	t.Cleanup(func() {
		loadMu.Lock()
		loaded = false
		loadMu.Unlock()
	})
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
		"a bad dark half":     strings.Replace(minimal, `{"light": "#020202", "dark": "#030303"}`, `{"light": "#020202", "dark": "red"}`, 1),
		"a half missing":      strings.Replace(minimal, `{"light": "#020202", "dark": "#030303"}`, `{"light": "#020202"}`, 1),
		"a stray half":        strings.Replace(minimal, `"dark": "#030303"}`, `"dark": "#030303", "dim": "#040404"}`, 1),
		"a key given twice":   strings.Replace(minimal, `"text": "#111111"`, `"text": "#111111", "text": "#121212"`, 1),
		"a key in capitals":   strings.Replace(minimal, `"text": "#111111"`, `"TEXT": "#111111"`, 1),
		"something after it":  minimal + ` {"name": "second"}`,
		"an about not text":   strings.Replace(minimal, `"name": "mine"`, `"name": "mine", "about": 5`, 1),
		"an about with an escape": strings.Replace(minimal, `"name": "mine"`,
			`"name": "mine", "about": "nice\u001b]52;c;aGk=\u0007"`, 1),
	} {
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := Parse(append([]byte("\xef\xbb\xbf"), minimal...)); err != nil {
		t.Errorf("a file saved with a byte order mark was refused: %v", err)
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
	write("plain.json", strings.Replace(minimal, `"mine"`, `"mono"`, 1))
	write("another.json", strings.Replace(minimal, `"mine"`, `"another"`, 1))
	write("notes.txt", "not a theme")
	write("huge.json", minimal+strings.Repeat(" ", maxThemeFile))
	reload(t, dir)

	if _, ok := ByName("mine"); !ok {
		t.Fatalf("a person's theme is not among %v", Names())
	}
	// In the order of their file names, so the list is the same every time.
	names := Names()
	if strings.Join(names[len(names)-2:], ",") != "another,mine" {
		t.Fatalf("a person's themes are listed as %v", names)
	}
	if mono, _ := ByName("mono"); mono.Name != "mono" || mono.Text == (Adaptive{Light: "#111111", Dark: "#111111"}) {
		t.Fatal("a person's theme replaced mono")
	}
	if nord, _ := ByName("nord"); nord.About == "" {
		t.Fatal("a person's theme replaced the shipped nord")
	}
	got := strings.Join(Problems(), "\n")
	if !strings.Contains(got, "broken.json") || !strings.Contains(got, "nord is already a theme") ||
		!strings.Contains(got, "mono is already a theme") || !strings.Contains(got, "huge.json") ||
		strings.Contains(got, "notes.txt") {
		t.Fatalf("problems:\n%s", got)
	}
}

// A pipe or a device among the theme files is refused without being read: reading one can wait
// forever, or never end, and the theme list is read while the interface waits.
func TestAPipeOrADeviceIsNotRead(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe.json"), 0o600); err != nil {
		t.Skip("no FIFOs here:", err)
	}
	if err := os.Symlink("/dev/zero", filepath.Join(dir, "zero.json")); err != nil {
		t.Fatal(err)
	}
	done := make(chan []string, 1)
	go func() {
		reload(t, dir)
		done <- Problems()
	}()
	select {
	case got := <-done:
		joined := strings.Join(got, "\n")
		if !strings.Contains(joined, "pipe.json") || !strings.Contains(joined, "zero.json") {
			t.Fatalf("problems:\n%s", joined)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading the theme files hung on a pipe or a device")
	}
}

// A file name reaches the screen quoted, so it cannot carry a terminal control there.
func TestAFileNameIsQuotedInProblems(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x\x1b[2J.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// And a name that reaches the list only through the operating system's own error.
	if err := os.Symlink(filepath.Join(dir, "gone"), filepath.Join(dir, "y\x1b]52;c;aGk=\x07.json")); err != nil {
		t.Fatal(err)
	}
	reload(t, dir)
	for _, problem := range Problems() {
		if strings.Contains(problem, "\x1b") {
			t.Fatalf("a control character reached a problem: %q", problem)
		}
	}
}

// Contrast, as WCAG measures it, against the background each palette was made for, in both its
// light and dark forms. Text has to be comfortable to read; an outcome has to be told apart at a
// glance; quiet text and code may be quieter, and a border fainter still, but none may vanish.
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
				{"code keyword", p.CodeKeyword, 2.5}, {"code string", p.CodeString, 2.5},
				{"code number", p.CodeNumber, 2.5}, {"code function", p.CodeFunction, 2.5},
				{"code type", p.CodeType, 2.5},
				// A border only has to be there, and a faint one is the point; it must not vanish.
				{"border", p.Border, 1.2},
			} {
				if c.fg == nil {
					continue
				}
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

// The rule the package exists for: no colour is made anywhere else. A hex colour, a colour
// constructor, an ANSI colour index or a raw colour escape outside this package is a colour no theme
// can change. Every package is scanned, since a colour does not have to be made in the interface to
// end up on it.
func TestNoColourIsMadeOutsideTheTheme(t *testing.T) {
	raw := regexp.MustCompile(strings.Join([]string{
		`\b[A-Za-z_]+\.Color\("`, // lipgloss.Color("..."), under any import name
		`color\.(N?RGBA(64)?|Gray(16)?|CMYK|Alpha(16)?)\{`,
		`ansi\.(IndexedColor|BasicColor|TrueColor|RGBColor)`,
		`\.(Darken|Lighten|Complementary|Alpha|ANSIColor|RGBColor|LightDark)\(`, // under any import name
		`\b(lipgloss|ansi)\.(Black|Red|Green|Yellow|Blue|Magenta|Cyan|White|Bright[A-Z]\w*)\b`,
		"`#[0-9a-fA-F]{3}([0-9a-fA-F]{3})?`",
		`"#[0-9a-fA-F]{3}([0-9a-fA-F]{3})?"`,
		`\\(x1b|033|u001b)\[[0-9;]*m`, // a colour escape written out
	}, "|"))
	here, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{filepath.Join("..", ".."), filepath.Join("..", "..", "..", "cmd")} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			// Another package's test can remove its own scratch directory mid-walk.
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if d.IsDir() {
				if abs, _ := filepath.Abs(path); abs == here {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
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
