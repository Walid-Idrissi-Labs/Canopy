package acp

import (
	"bufio"
	"strings"
	"testing"
)

func TestAFinalFrameDoesNotNeedANewline(t *testing.T) {
	t.Parallel()

	c := newConn(strings.NewReader(`{"jsonrpc":"2.0","method":"session/update"}`), &strings.Builder{})
	m, err := c.read()
	if err != nil {
		t.Fatalf("read final frame: %v", err)
	}
	if m.Method != methodSessionUpdate {
		t.Fatalf("method = %q, want %q", m.Method, methodSessionUpdate)
	}
}

func TestAJSONRPCFrameCannotGrowWithoutBound(t *testing.T) {
	t.Parallel()

	r := bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", maxFrameBytes+1)), 64*1024)
	frame, err := readFrame(r)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("readFrame error = %v, want size-limit error", err)
	}
	if frame != nil {
		t.Fatalf("oversized frame returned %d bytes", len(frame))
	}
}
