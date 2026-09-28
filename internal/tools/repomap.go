package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// repoMapTool outlines the repository: its source files and what each declares, the files the rest
// of the code refers to most coming first, within a size budget. Finding one's way around by
// listing directories and reading whole files costs many times more.
type repoMapTool struct{ w *Workspace }

// RepoMapTool builds the repository outline tool for a workspace.
func RepoMapTool(w *Workspace) core.Tool { return &repoMapTool{w: w} }

func (t *repoMapTool) Name() string        { return "repo_map" }
func (t *repoMapTool) Kind() core.ToolKind { return core.ToolRead }

func (t *repoMapTool) Description() string {
	return "An outline of the repository: source files with the functions, types and classes each " +
		"declares, the files most referenced by the rest of the code first. Much cheaper than listing " +
		"and reading files to find your way around; read what you need afterwards. path narrows it " +
		"to a directory."
}

func (t *repoMapTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "A directory to outline instead of the whole repository."},
			"max_tokens": {"type": "integer", "description": "Roughly how long the outline may be, 500 to 8000; 2000 when omitted."}
		}
	}`)
}

const (
	repoMapMaxFiles     = 5000
	repoMapMaxFileBytes = 512 * 1024
	repoMapMaxSymbols   = 15
	// repoMapMaxBytes bounds what one outline reads in all, so a huge tree costs a bounded pass.
	repoMapMaxBytes = 64 << 20
)

// mapFile is one source file, what it declares, and the compound names it mentions.
type mapFile struct {
	rel      string
	symbols  []string
	names    []string
	mentions map[string]struct{}
	refs     int
}

func (t *repoMapTool) Run(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Path      string `json:"path"`
		MaxTokens int    `json:"max_tokens"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return failure("could not read the arguments: %v", err), nil
		}
	}
	switch {
	case args.MaxTokens == 0:
		args.MaxTokens = 2000
	case args.MaxTokens < 500:
		args.MaxTokens = 500
	case args.MaxTokens > 8000:
		args.MaxTokens = 8000
	}
	root := t.w.Root()
	if args.Path != "" {
		resolved, err := t.w.Resolve(args.Path)
		if err != nil {
			return pathFailure(err), nil
		}
		root = resolved
	}

	var files []*mapFile
	var read int64
	truncated := false
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // deliberate: keep walking past unreadable directories
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := entry.Name()
		if entry.IsDir() {
			if path != root && (shouldSkipDir(name) || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		extract := extractorFor(name)
		if extract == nil || strings.HasPrefix(name, ".") || !entry.Type().IsRegular() {
			return nil
		}
		if len(files) >= repoMapMaxFiles || read >= repoMapMaxBytes {
			truncated = true
			return filepath.SkipAll
		}
		info, err := entry.Info()
		if err != nil || info.Size() > repoMapMaxFileBytes {
			return nil //nolint:nilerr // an unreadable or huge file is left out, not fatal
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil //nolint:nilerr // as above
		}
		read += int64(len(data))
		// The compound names it mentions are kept for ranking, not its bytes.
		f := &mapFile{rel: t.w.Relative(path), mentions: map[string]struct{}{}}
		f.symbols, f.names = extract(name, data)
		for _, word := range identifier.FindAll(data, -1) {
			if w := string(word); compound(w) {
				f.mentions[w] = struct{}{}
			}
		}
		files = append(files, f)
		return nil
	})
	if err != nil {
		return failure("outlining stopped: %v", err), nil
	}
	if len(files) == 0 {
		return core.ToolResult{Content: "no source files here"}, nil
	}

	rankByReferences(files)
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].refs != files[j].refs {
			return files[i].refs > files[j].refs
		}
		return files[i].rel < files[j].rel
	})

	listed := files[:0:0]
	for _, f := range files {
		if !testFile(f.rel) {
			listed = append(listed, f)
		}
	}
	files = listed

	// Most of the budget outlines the files that matter most; the rest names further files, so the
	// shape of the repository is there even where the detail is not.
	budget := args.MaxTokens * 4
	var b strings.Builder
	shown := 0
	for _, f := range files {
		var entry strings.Builder
		entry.WriteString(f.rel)
		if f.refs > 0 {
			fmt.Fprintf(&entry, "  (used by %d files)", f.refs)
		}
		entry.WriteString("\n")
		for i, s := range f.symbols {
			if i == repoMapMaxSymbols {
				fmt.Fprintf(&entry, "  ... and %d more\n", len(f.symbols)-i)
				break
			}
			entry.WriteString("  " + s + "\n")
		}
		if b.Len()+entry.Len() > budget*7/10 && shown > 0 {
			break
		}
		b.WriteString(entry.String())
		shown++
	}
	if shown < len(files) {
		b.WriteString("\nalso:\n")
		for _, f := range files[shown:] {
			if b.Len()+len(f.rel)+1 > budget {
				break
			}
			b.WriteString(f.rel + "\n")
			shown++
		}
	}
	if rest := len(files) - shown; rest > 0 || truncated {
		fmt.Fprintf(&b, "\n%d more files not shown; pass path to outline one directory, or raise max_tokens.\n", rest)
	}
	return core.ToolResult{Content: strings.TrimRight(b.String(), "\n")}, nil
}

// rankByReferences scores each file by how many other files use a name it declares. Only names
// declared in exactly one file count, so Model, String or New, declared all over, say nothing. So
// must a name be a compound, camelCase, snake_case or with a digit: a single word such as server
// or Report turns up in the prose of comments everywhere and would rank whatever declares it.
func rankByReferences(files []*mapFile) {
	definedIn := map[string][]*mapFile{}
	for _, f := range files {
		for _, n := range f.names {
			if compound(n) {
				definedIn[n] = append(definedIn[n], f)
			}
		}
	}
	for _, f := range files {
		counted := map[*mapFile]bool{}
		for word := range f.mentions {
			defs := definedIn[word]
			if len(defs) != 1 || defs[0] == f || counted[defs[0]] {
				continue
			}
			counted[defs[0]] = true
			defs[0].refs++
		}
	}
}

// compound reports whether a name is more than one plain word.
func compound(name string) bool {
	if len(name) < 4 {
		return false
	}
	for i, r := range name {
		if r == '_' || (r >= '0' && r <= '9') || (i > 0 && r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}

// testFile reports whether a file is a test, which is read for references but not listed: tests
// say how code is used, not where it lives.
func testFile(name string) bool {
	base := strings.ToLower(filepath.Base(name))
	return strings.HasSuffix(base, "_test.go") || strings.HasPrefix(base, "test_") ||
		strings.HasSuffix(base, "_test.py") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.")
}

var identifier = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

type extractor func(name string, data []byte) (symbols, names []string)

func extractorFor(name string) extractor {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go":
		return goSymbols
	case ".py":
		return pattern(pythonDecl)
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs":
		return pattern(scriptDecl)
	case ".rs":
		return pattern(rustDecl)
	case ".java", ".kt", ".kts", ".cs", ".swift", ".scala":
		return pattern(classyDecl)
	case ".rb":
		return pattern(rubyDecl)
	}
	return nil
}

// goSymbols reads Go declarations with the real parser: functions and methods with their
// signatures, and types.
func goSymbols(name string, data []byte) ([]string, []string) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, data, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil
	}
	var symbols, names []string
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			var buf bytes.Buffer
			_ = printer.Fprint(&buf, fset, &ast.FuncDecl{Recv: d.Recv, Name: d.Name, Type: d.Type})
			symbols = append(symbols, clip(strings.Join(strings.Fields(buf.String()), " ")))
			// A method's name is shared by every type that has one, so only functions rank.
			if d.Recv == nil {
				names = append(names, d.Name.Name)
			}
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts := spec.(*ast.TypeSpec)
				kind := "type"
				switch ts.Type.(type) {
				case *ast.StructType:
					kind = "struct"
				case *ast.InterfaceType:
					kind = "interface"
				}
				symbols = append(symbols, "type "+ts.Name.Name+" "+kind)
				names = append(names, ts.Name.Name)
			}
		}
	}
	return symbols, names
}

var (
	pythonDecl = regexp.MustCompile(`(?m)^( {0,4})(?:async\s+)?(def|class)\s+([A-Za-z_]\w*)[^\n:]*`)
	scriptDecl = regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:default\s+)?(?:declare\s+)?(?:abstract\s+)?(?:async\s+)?` +
		`(function\*?|class|interface|type|enum|const)\s+([A-Za-z_$][\w$]*)[^\n{=]*`)
	rustDecl    = regexp.MustCompile(`(?m)^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?(fn|struct|enum|trait|impl|mod|type)\s+([A-Za-z_]\w*)[^\n{;]*`)
	classyDecl  = regexp.MustCompile(`(?m)^\s*(?:(?:public|private|protected|internal|static|final|abstract|open|data|sealed|override|suspend)\s+)*(class|interface|struct|enum|record|object|fun|func|def)\s+([A-Za-z_]\w*)[^\n{=]*`)
	rubyDecl    = regexp.MustCompile(`(?m)^\s*(class|module|def)\s+([A-Za-z_][\w.]*[?!]?)[^\n]*`)
	scriptConst = regexp.MustCompile(`^\s*(?:export\s+)?const\s`)
)

// pattern builds an extractor from a declaration regular expression whose last group is the name.
// A top-level const in a script is kept only when exported, since most are local values.
func pattern(re *regexp.Regexp) extractor {
	return func(_ string, data []byte) ([]string, []string) {
		var symbols, names []string
		for _, m := range re.FindAllSubmatch(data, -1) {
			line := string(m[0])
			if scriptConst.MatchString(line) && !strings.Contains(line, "export") {
				continue
			}
			symbols = append(symbols, clip(strings.Join(strings.Fields(line), " ")))
			names = append(names, string(m[len(m)-1]))
		}
		return symbols, names
	}
}

func clip(s string) string {
	if len(s) > 140 {
		return s[:137] + "..."
	}
	return s
}
