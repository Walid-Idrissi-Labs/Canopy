package chat_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// A column is not a rune. A CJK character occupies two of them, so a wrap that cuts a rune count
// against a cell budget emits lines up to twice the width it was given, and a line wider than the
// terminal wraps the whole frame rather than just itself.
//
// This is the case with no spaces in it, which is what forces the hard break path: an ordinary
// sentence in Japanese has no wrap points a space-based wrapper can use.
// Exercised through a fenced code block, which is the path that still hard-breaks a run of text with
// no wrap points in it. A comment or a string literal in a CJK language is ordinary source.
func TestFullWidthTextIsWrappedByColumnNotByRune(t *testing.T) {
	line := strings.Repeat("日本語", 40) // 120 runes, 240 columns
	reply := "```\n" + line + "\n```"

	for _, width := range []int{20, 40, 80} {
		for _, rendered := range chat.RenderMarkdown(reply, width) {
			if got := lipgloss.Width(rendered); got > width {
				t.Errorf("width %d: a wrapped line is %d columns wide", width, got)
			}
		}
	}
}

// Nothing may be lost on the way. Breaking a line is expected; dropping characters out of it is the
// failure this guards, and it is the one a cell-based cut is most likely to introduce.
func TestWrappingFullWidthTextLosesNothing(t *testing.T) {
	line := strings.Repeat("日本語", 20)
	reply := "```\n" + line + "\n```"

	var recovered strings.Builder
	for _, rendered := range chat.RenderMarkdown(reply, 30) {
		text := plain(rendered)
		if strings.HasPrefix(strings.TrimSpace(text), "```") {
			continue
		}
		text = strings.TrimPrefix(text, "  ")
		text = strings.TrimPrefix(text, "↳ ")
		recovered.WriteString(text)
	}
	if got := recovered.String(); got != line {
		t.Errorf("the wrapped line does not reconstruct the source:\ngot  %q\nwant %q", got, line)
	}
}

// The status row budgets its own height by counting the newlines in it, so a message long enough
// for the terminal to wrap on its own occupied more rows than the frame had reserved and pushed the
// footer off the bottom. The messages long enough to do it are the errors, which is to say exactly
// the moments when the screen coming apart is least welcome.
func TestALongErrorDoesNotPushTheFrameOffTheScreen(t *testing.T) {
	engine := &fakeEngine{
		session: core.Session{ID: "s1"},
		sendErr: errors.New(strings.Repeat("the provider said something at great length. ", 12)),
	}

	next, _ := run(model(engine), "hello")

	body := next.Body()
	for _, line := range strings.Split(body, "\n") {
		if got := lipgloss.Width(line); got > 96 {
			t.Errorf("a body line is %d columns wide, so the terminal wraps it: %q", got, plain(line))
		}
	}
	if !strings.Contains(plain(body), "at great length") {
		t.Errorf("the error was dropped rather than wrapped:\n%s", plain(body))
	}
}

// The argument on a tool call line is truncated to fit inside a bordered panel. Counted in runes, a
// path of full-width characters measures as shorter than it draws and pushes the border past the
// edge of the terminal.
func TestAFullWidthArgumentDoesNotOverflowTheCallLine(t *testing.T) {
	path := strings.Repeat("日本語", 30) + ".go"
	engine := &fakeEngine{session: withCall(
		"read_file", `{"path":"`+path+`"}`,
		core.ToolResult{Content: "ok", Duration: 0},
	)}

	m := model(engine)
	for _, line := range strings.Split(m.Body(), "\n") {
		if got := lipgloss.Width(line); got > 80 {
			t.Errorf("a rendered line is %d columns wide at width 80: %q", got, plain(line))
		}
	}
}
