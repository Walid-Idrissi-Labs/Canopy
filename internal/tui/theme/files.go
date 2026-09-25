package theme

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// A theme beyond the two built in is a file of colours, the same shape whether it ships or a person
// writes it: a name, and each colour of the palette as one hex value or a light and a dark one.
// Nothing in a theme file is code, and nothing in it can reach further than the colours it names.

//go:embed themes/*.json
var shippedFiles embed.FS

// ThemesDirEnv overrides where a person's own themes are read from.
const ThemesDirEnv = "CANOPY_THEMES_DIR"

// shade is one colour in a theme file: "#rrggbb", or {"light": "#rrggbb", "dark": "#rrggbb"}.
type shade Adaptive

func (s *shade) UnmarshalJSON(data []byte) error {
	var one string
	if json.Unmarshal(data, &one) == nil {
		s.Light, s.Dark = one, one
		return nil
	}
	var both struct{ Light, Dark string }
	if err := json.Unmarshal(data, &both); err != nil {
		return errors.New(`a colour is "#rrggbb" or {"light": "#rrggbb", "dark": "#rrggbb"}`)
	}
	s.Light, s.Dark = both.Light, both.Dark
	return nil
}

type themeFile struct {
	Name       string `json:"name"`
	About      string `json:"about"`
	Background *shade `json:"background"`

	Text      *shade `json:"text"`
	Muted     *shade `json:"muted"`
	Accent    *shade `json:"accent"`
	Success   *shade `json:"success"`
	Danger    *shade `json:"danger"`
	Warning   *shade `json:"warning"`
	Info      *shade `json:"info"`
	Border    *shade `json:"border"`
	Highlight *shade `json:"highlight"`

	Flame      *shade `json:"flame"`
	FlameCore  *shade `json:"flameCore"`
	Smoke      *shade `json:"smoke"`
	SmokeFaint *shade `json:"smokeFaint"`

	CodeKeyword  *shade `json:"codeKeyword"`
	CodeString   *shade `json:"codeString"`
	CodeComment  *shade `json:"codeComment"`
	CodeNumber   *shade `json:"codeNumber"`
	CodeFunction *shade `json:"codeFunction"`
	CodeType     *shade `json:"codeType"`
}

var (
	hexColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	themeName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
)

// Parse reads a theme file. Every colour of the palette is required except the two for function
// and type names, which fall back to the text colour, so a theme that forgets one is told which
// rather than drawing it in nothing.
func Parse(data []byte) (Palette, error) {
	var file themeFile
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return Palette{}, fmt.Errorf("not a theme file: %w", err)
	}
	if !themeName.MatchString(file.Name) {
		return Palette{}, fmt.Errorf("the name %q is not lower case letters, digits and dashes", file.Name)
	}
	p := Palette{Name: file.Name, About: file.About}
	var problems []string
	take := func(field string, s *shade, required bool) color.Color {
		if s == nil {
			if required {
				problems = append(problems, field+" is missing")
			}
			return nil
		}
		if !hexColour.MatchString(s.Light) || !hexColour.MatchString(s.Dark) {
			problems = append(problems, field+" is not #rrggbb")
			return nil
		}
		return Adaptive(*s)
	}
	if bg := take("background", file.Background, false); bg != nil {
		p.Background = bg.(Adaptive)
	}
	p.Text = take("text", file.Text, true)
	p.Muted = take("muted", file.Muted, true)
	p.Accent = take("accent", file.Accent, true)
	p.Success = take("success", file.Success, true)
	p.Danger = take("danger", file.Danger, true)
	p.Warning = take("warning", file.Warning, true)
	p.Info = take("info", file.Info, true)
	p.Border = take("border", file.Border, true)
	p.Highlight = take("highlight", file.Highlight, true)
	p.Flame = take("flame", file.Flame, true)
	p.FlameCore = take("flameCore", file.FlameCore, true)
	p.Smoke = take("smoke", file.Smoke, true)
	p.SmokeFaint = take("smokeFaint", file.SmokeFaint, true)
	p.CodeKeyword = take("codeKeyword", file.CodeKeyword, true)
	p.CodeString = take("codeString", file.CodeString, true)
	p.CodeComment = take("codeComment", file.CodeComment, true)
	p.CodeNumber = take("codeNumber", file.CodeNumber, true)
	p.CodeFunction = take("codeFunction", file.CodeFunction, false)
	p.CodeType = take("codeType", file.CodeType, false)
	if len(problems) > 0 {
		return Palette{}, fmt.Errorf("theme %s: %s", file.Name, strings.Join(problems, "; "))
	}
	return p, nil
}

var (
	loadMu   sync.Mutex
	loadOnce sync.Once
	shipped  []Palette
	personal []Palette
	problems []string
)

// Reload reads the theme files again, so one written or fixed since the program started can be
// used without restarting it.
func Reload() {
	loadMu.Lock()
	loadOnce, shipped, personal, problems = sync.Once{}, nil, nil, nil
	loadMu.Unlock()
	load()
}

// load reads the shipped themes and a person's own, once. A shipped theme that does not parse is a
// bug the tests catch; a person's that does not is skipped and said, rather than stopping the
// program over a colour.
func load() {
	loadMu.Lock()
	defer loadMu.Unlock()
	loadOnce.Do(func() {
		entries, _ := shippedFiles.ReadDir("themes")
		for _, entry := range entries {
			data, err := shippedFiles.ReadFile("themes/" + entry.Name())
			if err != nil {
				continue
			}
			if p, err := Parse(data); err == nil {
				shipped = append(shipped, p)
			} else {
				problems = append(problems, err.Error())
			}
		}
		sort.Slice(shipped, func(i, j int) bool { return shipped[i].Name < shipped[j].Name })

		dir := userThemesDir()
		if dir == "" {
			return
		}
		files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		sort.Strings(files)
		taken := map[string]bool{Default.Name: true, Monochrome.Name: true}
		for _, p := range shipped {
			taken[p.Name] = true
		}
		for _, path := range files {
			info, err := os.Stat(path)
			if err != nil || info.Size() > 64<<10 {
				problems = append(problems, filepath.Base(path)+": not read")
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				problems = append(problems, filepath.Base(path)+": "+err.Error())
				continue
			}
			p, err := Parse(data)
			if err != nil {
				problems = append(problems, filepath.Base(path)+": "+err.Error())
				continue
			}
			// A person's theme adds to the list; it does not quietly replace one everybody knows by
			// that name.
			if taken[p.Name] {
				problems = append(problems, filepath.Base(path)+": "+p.Name+" is already a theme")
				continue
			}
			taken[p.Name] = true
			personal = append(personal, p)
		}
	})
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

// Problems are the theme files that could not be used, and why.
func Problems() []string {
	load()
	loadMu.Lock()
	defer loadMu.Unlock()
	return append([]string(nil), problems...)
}
