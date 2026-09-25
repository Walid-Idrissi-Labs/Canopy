package chat

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// chromaBlock highlights a whole code block with a real lexer, so state that spans lines, a block
// comment, a multi-line string, a heredoc, is coloured correctly, and a few hundred languages are
// recognised rather than twelve. Each source line is then wrapped to width with its colours kept.
// It reports false when no lexer knows the language, and the caller falls back.
func chromaBlock(lang string, code []string, width int) ([]string, bool) {
	lexer := lexerFor(lang)
	if lexer == nil {
		return nil, false
	}
	source := strings.Join(code, "\n")
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, source)
	if err != nil {
		return nil, false
	}
	t := theme.Current()
	lines := make([]string, 0, len(code))
	var current strings.Builder
	for token := iterator(); token != chroma.EOF; token = iterator() {
		style := tokenStyle(token.Type, t)
		for i, part := range strings.Split(token.Value, "\n") {
			if i > 0 {
				lines = append(lines, current.String())
				current.Reset()
			}
			if part == "" {
				continue
			}
			part = expandTabs(part)
			if style == nil {
				current.WriteString(part)
			} else {
				current.WriteString(style.Render(part))
			}
		}
	}
	lines = append(lines, current.String())
	for len(lines) > len(code) && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	const indent = "  "
	const continuation = "↳ "
	contentWidth := width - lipgloss.Width(indent)
	if contentWidth < 1 {
		contentWidth = 1
	}
	var out []string
	for _, line := range lines {
		for j, fragment := range strings.Split(ansi.HardwrapWc(line, contentWidth, true), "\n") {
			prefix := indent
			if j > 0 {
				prefix = continuation
			}
			out = append(out, prefix+fragment)
		}
	}
	return out, true
}

func lexerFor(lang string) chroma.Lexer {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" {
		return nil
	}
	if l := lexers.Get(lang); l != nil {
		return l
	}
	return lexers.Match("file." + lang)
}

// tokenStyle maps a token to the theme's code colours, so a block matches the rest of the
// interface and the monochrome theme still renders it legibly.
func tokenStyle(tt chroma.TokenType, t theme.Theme) *lipgloss.Style {
	switch {
	case tt.InCategory(chroma.Comment):
		return &t.CodeComment
	case tt.InCategory(chroma.LiteralString):
		return &t.CodeString
	case tt.InCategory(chroma.LiteralNumber):
		return &t.CodeNumber
	case tt.InCategory(chroma.Keyword), tt == chroma.NameBuiltin, tt == chroma.NameBuiltinPseudo:
		return &t.CodeKeyword
	case tt == chroma.NameFunction, tt == chroma.NameFunctionMagic:
		return &t.CodeFunction
	case tt == chroma.NameClass, tt == chroma.KeywordType, tt == chroma.NameTag:
		return &t.CodeType
	default:
		return nil
	}
}
