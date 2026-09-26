package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
)

// captureStderr collects what is written to standard error until the returned function is called,
// which returns it as lines and puts standard error back. For the setup before the interface opens:
// what is said there is otherwise drawn over by the alternate screen before anybody reads it.
//
// It swaps the os.Stderr variable, so it is called before anything that writes from a goroutine of
// its own starts, and what already holds the old value (log, the bell, notifications) goes on
// writing to the terminal uncaptured.
func captureStderr() func() []string {
	reader, writer, err := os.Pipe()
	if err != nil {
		return func() []string { return nil }
	}
	saved := os.Stderr
	os.Stderr = writer
	var buf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Still shown as it happens, for a terminal that keeps it or a log that takes it.
		_, _ = io.Copy(io.MultiWriter(&buf, saved), reader)
	}()
	return func() []string {
		os.Stderr = saved
		_ = writer.Close()
		wg.Wait()
		_ = reader.Close()
		var lines []string
		for _, line := range strings.Split(buf.String(), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, line)
			}
		}
		return lines
	}
}
