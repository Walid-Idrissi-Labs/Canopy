// Package clipboard puts text on the system clipboard.
//
// Its own package because it is the one place the interface talks to the machine outside the
// terminal, and because how it does so is a ranking worth stating once: a native tool first —
// pbcopy, wl-copy, xclip, xsel — because those reach the real clipboard on every terminal that has
// one, and OSC 52 through the terminal itself when none exists, which is what covers SSH sessions
// and the terminals that support it. Neither path is guaranteed on every machine, which is why the
// two are stacked rather than chosen.
package clipboard

import (
	"os/exec"
	"strings"

	"github.com/muesli/termenv"
)

// tools are the native clipboard writers, most specific platform first. Each entry is the command
// and its arguments; text arrives on stdin, which is the convention all four share.
var tools = [][]string{
	{"pbcopy"},
	{"wl-copy"},
	{"xclip", "-selection", "clipboard"},
	{"xsel", "--clipboard", "--input"},
}

// Write puts text on the system clipboard.
//
// It returns an error only when nothing could plausibly have worked. The OSC 52 fallback cannot
// report failure — the terminal either honours the sequence or ignores it — so a nil return means
// "written, as far as anyone can tell", which is the honest most this operation offers.
func Write(text string) error {
	for _, tool := range tools {
		path, err := exec.LookPath(tool[0])
		if err != nil {
			continue
		}
		cmd := exec.Command(path, tool[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}

	// No native tool, so ask the terminal itself. Invisible on terminals that do not support it,
	// which is why the native tools are tried first rather than trusted last.
	termenv.Copy(text)
	return nil
}
