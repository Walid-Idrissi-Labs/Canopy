package mcp

// A writer that keeps the beginning and stops.
//
// Used for a server's stderr, which is unbounded by nature: a server stuck in a retry loop prints
// the same line forever, and keeping all of it to show three lines of it later is a memory leak
// with a diagnostic excuse. The beginning is the part worth keeping, because the first thing a
// failing program prints is usually why.
//
// internal/exec has one of these too. Not shared, because unifying them means editing that package
// while A9-01 is being swept through it, and two small buffers cost less than that merge. Worth
// collapsing into one afterwards.

import "sync"

type boundedBuffer struct {
	mu      sync.Mutex
	limit   int
	content []byte
	dropped int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	room := b.limit - len(b.content)
	if room <= 0 {
		b.dropped += len(p)
		// The full length is reported as written. Telling the process its output failed would make
		// a well behaved one treat a broken pipe as an error and exit, which is not what happened.
		return len(p), nil
	}
	if len(p) > room {
		b.content = append(b.content, p[:room]...)
		b.dropped += len(p) - room
		return len(p), nil
	}

	b.content = append(b.content, p...)
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.content)
}
