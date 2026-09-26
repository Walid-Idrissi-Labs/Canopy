package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/trust"
)

// maxMentionFiles bounds the list an @ mention searches, for a repository with a vendored world in
// it.
const maxMentionFiles = 20000

// projectFiles lists the files an @ mention can name: git's own list, tracked and untracked but
// never ignored. Read in the background, starting now, and again when it is more than a few seconds
// old, so the interface never waits on git: until the first read lands the list is empty.
func projectFiles(dir string) func() []string {
	var mu sync.Mutex
	var files []string
	var read time.Time
	reading := false
	refresh := func() {
		out, err := exec.Command("git", "-C", dir, "ls-files", "--cached", "--others", "--exclude-standard", "-z").Output()
		var fresh []string
		if err == nil {
			for _, name := range strings.Split(string(out), "\x00") {
				if name != "" && len(fresh) < maxMentionFiles {
					fresh = append(fresh, filepath.ToSlash(name))
				}
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if err == nil {
			files = fresh
		}
		read, reading = time.Now(), false
	}
	start := func() {
		if !reading {
			reading = true
			go refresh()
		}
	}
	mu.Lock()
	start()
	mu.Unlock()
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(read) > 10*time.Second {
			start()
		}
		return files
	}
}

// noteWritten runs between writing a note and checking what the repository has become; a test uses
// it to change the repository in that moment.
var noteWritten = func() {}

// noteRead runs between reading AGENTS.md and writing the note, for the same kind of test.
var noteRead = func() {}

// rememberIn keeps a "# note" in the project's AGENTS.md, which is read into every conversation
// started from then on.
//
// AGENTS.md is part of what a person trusts in a repository, so writing to it changes what they
// trusted. The note is theirs, so trust follows it, but only when AGENTS.md is still what they
// trusted: had an agent edited it since, following would quietly bless that edit too, and instead
// the next start asks again.
func rememberIn(dir string, project config.Project) func(note string) (string, error) {
	return func(note string) (string, error) {
		path := filepath.Join(dir, "AGENTS.md")
		line := "- " + strings.ReplaceAll(note, "\n", " ") + "\n"

		// Opened without following a link, and checked as opened: a regular file with one name, so
		// the note cannot be steered into a file somewhere else.
		// Appended, so an append made by anything else between reading and writing is kept, and
		// then shows up as a difference from what was expected below.
		file, err := openNoFollow(path)
		if err != nil {
			return "", errors.New("AGENTS.md could not be opened as a plain file here, so nothing was written")
		}
		defer func() { _ = file.Close() }()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || !singleLink(info) {
			return "", errors.New("AGENTS.md is not a plain file with one name, so nothing was written")
		}
		before, err := io.ReadAll(file)
		if err != nil {
			return "", err
		}
		if len(before) > 0 && before[len(before)-1] != '\n' {
			line = "\n" + line
		}

		// What the repository will be after the note, worked out before writing it. Trust follows the
		// note only if the repository was trusted before it and is, afterwards, exactly that: anything
		// else that changed meanwhile, an agent's edit to any instruction file among them, is left for
		// the next start to ask about.
		// Every instruction file read once, so the check of what was trusted and the expectation of
		// what will be are made from the same reading; AGENTS.md as it was opened.
		snapshot := map[string][]byte{}
		for _, rel := range config.InstructionFiles(dir) {
			if data, err := os.ReadFile(filepath.Join(dir, rel)); err == nil {
				snapshot[rel] = data
			}
		}
		snapshot["AGENTS.md"] = before
		store, storeErr := trust.Open()
		existed := len(before) > 0 || info.Size() > 0
		trusted := storeErr == nil && project.Trusted && existed &&
			store.Trusted(trust.DescribeWith(dir, project, snapshot))
		snapshot["AGENTS.md"] = append(append([]byte(nil), before...), line...)
		expected := trust.DescribeWith(dir, project, snapshot)

		noteRead()
		if _, err := file.WriteString(line); err != nil {
			return "", err
		}
		noteWritten()
		if trusted {
			if after := trust.Describe(dir, project); after.Fingerprint() == expected.Fingerprint() {
				_ = store.Grant(after)
				return "AGENTS.md", nil
			}
		}
		if !project.Trusted {
			return "AGENTS.md", nil
		}
		return "AGENTS.md (the repository changed besides, so the next start asks to trust it again)", nil
	}
}
