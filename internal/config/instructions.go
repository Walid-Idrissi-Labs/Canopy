package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MaxInstructionBytes bounds what project instructions may add to every request. Instructions sit
// in the system prompt of every request of every agent in the project, so an unbounded file is a
// standing charge on all of them that nobody sees.
const MaxInstructionBytes = 48 * 1024

// ErrInstructionsTooLarge is returned rather than truncating: half a file of rules is a different
// set of rules, and quietly sending it would be worse than sending none.
var ErrInstructionsTooLarge = errors.New("project instructions are larger than Canopy sends")

// InstructionSource is one file or field that contributed instructions.
type InstructionSource struct {
	// Name is how the source is shown: a path relative to the project, or "canopy.json".
	Name  string
	Bytes int
}

// Instructions is what a project asks every agent in it to follow, in precedence order: the user's
// own file first, then the repository's AGENTS.md, CLAUDE.md, .canopy/instructions.md and the
// canopy.json field. Later sources are more specific, and the model is told that later ones win
// where they conflict.
type Instructions struct {
	Text    string
	Sources []InstructionSource
}

// instructionFiles are read from the project root, in precedence order.
var instructionFiles = []string{"AGENTS.md", "CLAUDE.md", filepath.Join(".canopy", "instructions.md")}

// LoadInstructions gathers the instructions for a project directory.
func LoadInstructions(dir string, project Project) (Instructions, error) {
	var parts []string
	var out Instructions
	add := func(name, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		parts = append(parts, fmt.Sprintf("<instructions source=%q>\n%s\n</instructions>", name, text))
		out.Sources = append(out.Sources, InstructionSource{Name: name, Bytes: len(text)})
	}

	if base, err := os.UserConfigDir(); err == nil {
		if data, err := os.ReadFile(filepath.Join(base, "canopy", "instructions.md")); err == nil {
			add("your instructions.md", string(data))
		}
	}
	for _, name := range instructionFiles {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			add(filepath.ToSlash(name), string(data))
		}
	}
	add("canopy.json", project.Instructions)

	out.Text = strings.Join(parts, "\n\n")
	if len(out.Text) > MaxInstructionBytes {
		return Instructions{Sources: out.Sources}, fmt.Errorf("%w: %d bytes from %s, the limit is %d",
			ErrInstructionsTooLarge, len(out.Text), describeSources(out.Sources), MaxInstructionBytes)
	}
	return out, nil
}

func describeSources(sources []InstructionSource) string {
	names := make([]string, 0, len(sources))
	for _, s := range sources {
		names = append(names, s.Name)
	}
	return strings.Join(names, ", ")
}

// InstructionFiles lists the repository instruction files present in dir, for the trust prompt.
func InstructionFiles(dir string) []string {
	var found []string
	for _, name := range instructionFiles {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			found = append(found, filepath.ToSlash(name))
		}
	}
	return found
}
