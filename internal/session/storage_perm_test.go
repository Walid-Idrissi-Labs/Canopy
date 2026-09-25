package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTheHistoryDatabaseIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	s, err := OpenStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("history.db is %v; transcripts can hold secrets an agent read and must be owner only", perm)
	}
}
