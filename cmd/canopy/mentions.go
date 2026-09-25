package main

import (
	"errors"
	"fmt"
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
// never ignored, read again when it is more than a few seconds old.
func projectFiles(dir string) func() []string {
	var mu sync.Mutex
	var files []string
	var read time.Time
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		if files != nil && time.Since(read) < 10*time.Second {
			return files
		}
		out, err := exec.Command("git", "-C", dir, "ls-files", "--cached", "--others", "--exclude-standard", "-z").Output()
		if err != nil {
			return files
		}
		files = files[:0:0]
		for _, name := range strings.Split(string(out), "\x00") {
			if name != "" && len(files) < maxMentionFiles {
				files = append(files, filepath.ToSlash(name))
			}
		}
		read = time.Now()
		return files
	}
}

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
		if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
			return "", errors.New("AGENTS.md is not a regular file, so nothing was written to it")
		}
		store, storeErr := trust.Open()
		stillTrusted := storeErr == nil && project.Trusted && store.Trusted(trust.Describe(dir, project))

		file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
		if err != nil {
			return "", err
		}
		prefix := ""
		if info, err := file.Stat(); err == nil && info.Size() > 0 {
			if last, err := lastByte(path); err == nil && last != '\n' {
				prefix = "\n"
			}
		}
		_, err = fmt.Fprintf(file, "%s- %s\n", prefix, strings.ReplaceAll(note, "\n", " "))
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return "", err
		}
		if stillTrusted {
			_ = store.Grant(trust.Describe(dir, project))
			return "AGENTS.md", nil
		}
		return "AGENTS.md (which changed since this repository was trusted, so the next start asks)", nil
	}
}

func lastByte(path string) (byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return '\n', err
	}
	buf := make([]byte, 1)
	_, err = file.ReadAt(buf, info.Size()-1)
	return buf[0], err
}
