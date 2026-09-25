package theme

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
)

// A theme beyond the two built in is a file of colours, the same shape whether it ships or a person
// writes it: a name, and each colour of the palette as one hex value or a light and a dark one.
// Nothing in a theme file is code, and nothing in it can reach further than the colours it names.

//go:embed themes/*.json
var shippedFiles embed.FS

// ThemesDirEnv overrides where a person's own themes are read from.
const ThemesDirEnv = "CANOPY_THEMES_DIR"

// maxThemeFile is the largest theme file read. A real one is about a kilobyte.
const maxThemeFile = 64 << 10

// roles are the keys a theme file may have, and whether each is required.
var roles = []struct {
	key      string
	required bool
	set      func(*Palette, Adaptive)
}{
	{"background", false, func(p *Palette, a Adaptive) { p.Background = a }},
	{"text", true, func(p *Palette, a Adaptive) { p.Text = a }},
	{"muted", true, func(p *Palette, a Adaptive) { p.Muted = a }},
	{"accent", true, func(p *Palette, a Adaptive) { p.Accent = a }},
	{"success", true, func(p *Palette, a Adaptive) { p.Success = a }},
	{"danger", true, func(p *Palette, a Adaptive) { p.Danger = a }},
	{"warning", true, func(p *Palette, a Adaptive) { p.Warning = a }},
	{"info", true, func(p *Palette, a Adaptive) { p.Info = a }},
	{"border", true, func(p *Palette, a Adaptive) { p.Border = a }},
	{"highlight", true, func(p *Palette, a Adaptive) { p.Highlight = a }},
	{"flame", true, func(p *Palette, a Adaptive) { p.Flame = a }},
	{"flameCore", true, func(p *Palette, a Adaptive) { p.FlameCore = a }},
	{"smoke", true, func(p *Palette, a Adaptive) { p.Smoke = a }},
	{"smokeFaint", true, func(p *Palette, a Adaptive) { p.SmokeFaint = a }},
	{"codeKeyword", true, func(p *Palette, a Adaptive) { p.CodeKeyword = a }},
	{"codeString", true, func(p *Palette, a Adaptive) { p.CodeString = a }},
	{"codeComment", true, func(p *Palette, a Adaptive) { p.CodeComment = a }},
	{"codeNumber", true, func(p *Palette, a Adaptive) { p.CodeNumber = a }},
	{"codeFunction", false, func(p *Palette, a Adaptive) { p.CodeFunction = a }},
	{"codeType", false, func(p *Palette, a Adaptive) { p.CodeType = a }},
}

var (
	hexColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	themeName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
)

// Parse reads a theme file. Every colour of the palette is required except the background and the
// two for function and type names, which fall back to the text colour, so a theme that forgets one
// is told which rather than drawing it in nothing. Keys are exact: a misspelt or repeated one is an
// error, not a colour quietly lost.
func Parse(data []byte) (Palette, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if key, ok := repeatedKey(data); ok {
		return Palette{}, fmt.Errorf("not a theme file: %q is given twice", key)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Palette{}, fmt.Errorf("not a theme file: %w", err)
	}
	var p Palette
	name, err := text(fields, "name")
	if err != nil {
		return Palette{}, err
	}
	if !themeName.MatchString(name) {
		return Palette{}, fmt.Errorf("the name %q is not lower case letters, digits and dashes", name)
	}
	p.Name = name
	if p.About, err = text(fields, "about"); err != nil {
		return Palette{}, err
	}
	// Shown in /theme, so held to what a line of text on screen can be.
	if strings.IndexFunc(p.About, isControl) >= 0 || len(p.About) > 300 {
		return Palette{}, errors.New("about is one line of text, up to 300 characters")
	}
	known := map[string]bool{"name": true, "about": true}
	var problems []string
	for _, role := range roles {
		known[role.key] = true
		raw, present := fields[role.key]
		if !present {
			if role.required {
				problems = append(problems, role.key+" is missing")
			}
			continue
		}
		shade, err := parseShade(raw)
		if err != nil {
			problems = append(problems, role.key+" "+err.Error())
			continue
		}
		role.set(&p, shade)
	}
	for key := range fields {
		if !known[key] {
			problems = append(problems, fmt.Sprintf("%q is not a colour a theme has", key))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return Palette{}, fmt.Errorf("theme %s: %s", name, strings.Join(problems, "; "))
	}
	return p, nil
}

func text(fields map[string]json.RawMessage, key string) (string, error) {
	raw, ok := fields[key]
	if !ok {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("%s is not a string", key)
	}
	return s, nil
}

// parseShade reads "#rrggbb", or {"light": "#rrggbb", "dark": "#rrggbb"} with both and nothing else.
func parseShade(raw json.RawMessage) (Adaptive, error) {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		if !hexColour.MatchString(one) {
			return Adaptive{}, errors.New("is not #rrggbb")
		}
		return Adaptive{Light: one, Dark: one}, nil
	}
	var both map[string]string
	if err := json.Unmarshal(raw, &both); err != nil {
		return Adaptive{}, errors.New(`is not "#rrggbb" or {"light": "#rrggbb", "dark": "#rrggbb"}`)
	}
	light, dark := both["light"], both["dark"]
	if len(both) != 2 || !hexColour.MatchString(light) || !hexColour.MatchString(dark) {
		return Adaptive{}, errors.New(`needs "light" and "dark", each #rrggbb, and nothing else`)
	}
	return Adaptive{Light: light, Dark: dark}, nil
}

// repeatedKey reports the first key given twice in any one object. A decoder keeps the last and
// says nothing, so a theme with two "text" entries would be drawn with whichever its author did not
// look at last.
func repeatedKey(data []byte) (string, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var walk func() (string, bool, error)
	walk = func() (string, bool, error) {
		token, err := decoder.Token()
		if err != nil {
			return "", false, err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return "", false, nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return "", false, err
				}
				key, _ := keyToken.(string)
				if seen[key] {
					return key, true, nil
				}
				seen[key] = true
				if key, found, err := walk(); found || err != nil {
					return key, found, err
				}
			}
		case '[':
			for decoder.More() {
				if key, found, err := walk(); found || err != nil {
					return key, found, err
				}
			}
		}
		_, err = decoder.Token()
		return "", false, err
	}
	key, found, _ := walk()
	return key, found
}

var (
	loadMu   sync.Mutex
	loaded   bool
	shipped  []Palette
	personal []Palette
	problems []string
)

// Reload reads the theme files again, so one written or fixed since the program started can be
// used without restarting it.
func Reload() {
	loadMu.Lock()
	loaded = false
	loadMu.Unlock()
	load()
}

// load reads the shipped themes and a person's own, if they have not been read. The reading happens
// outside the lock, so a slow disk holds up nothing but the caller. A shipped theme that does not
// parse is a bug the tests catch; a person's that does not is skipped and said, rather than
// stopping the program over a colour.
func load() {
	loadMu.Lock()
	done := loaded
	loadMu.Unlock()
	if done {
		return
	}
	var found, own []Palette
	var said []string
	entries, _ := shippedFiles.ReadDir("themes")
	for _, entry := range entries {
		data, err := shippedFiles.ReadFile("themes/" + entry.Name())
		if err != nil {
			continue
		}
		if p, err := Parse(data); err == nil {
			found = append(found, p)
		} else {
			said = append(said, err.Error())
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })

	if dir := userThemesDir(); dir != "" {
		files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		sort.Strings(files)
		taken := map[string]bool{Default.Name: true, Monochrome.Name: true}
		for _, p := range found {
			taken[p.Name] = true
		}
		for _, path := range files {
			// Named as Go would quote it, so a file name cannot put a terminal control on screen.
			name := fmt.Sprintf("%q", filepath.Base(path))
			data, err := readThemeFile(path)
			if err != nil {
				said = append(said, name+": "+err.Error())
				continue
			}
			p, err := Parse(data)
			if err != nil {
				said = append(said, name+": "+err.Error())
				continue
			}
			// A person's theme adds to the list; it does not quietly replace one everybody knows by
			// that name.
			if taken[p.Name] {
				said = append(said, name+": "+p.Name+" is already a theme")
				continue
			}
			taken[p.Name] = true
			own = append(own, p)
		}
	}

	loadMu.Lock()
	defer loadMu.Unlock()
	shipped, personal, problems, loaded = found, own, said, true
}

// readThemeFile reads a regular file of reasonable size. A pipe, a device or a link to one is
// refused without being read, since reading one can wait forever or never end.
func readThemeFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	if opened, err := file.Stat(); err != nil || !opened.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxThemeFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxThemeFile {
		return nil, errors.New("larger than a theme file is")
	}
	return data, nil
}

// userThemesDir is where a person's own themes are read from.
func userThemesDir() string {
	if dir := strings.TrimSpace(os.Getenv(ThemesDirEnv)); dir != "" {
		return dir
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "canopy", "themes")
}

// Problems are the theme files that could not be used, and why. Each is made safe to print: an
// operating system's error names the file in full, whatever it is called.
func Problems() []string {
	load()
	loadMu.Lock()
	defer loadMu.Unlock()
	out := make([]string, 0, len(problems))
	for _, problem := range problems {
		out = append(out, strings.Map(func(r rune) rune {
			if isControl(r) {
				return -1
			}
			return r
		}, problem))
	}
	return out
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) }
