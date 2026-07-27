package verify

import (
	"testing"
)

// A7-03. The pain this preempts exists only because agents run in parallel: merge the first one and
// the second stops merging cleanly, by which point the second has spent an hour on it.
func TestOverlapNamesTheFileAndEveryAgentInvolved(t *testing.T) {
	verifier, _, subjects := harness(t, "one", "two", "three")

	writeFile(t, subjects["one"].Dir, "auth.go", "package auth\n\n// one\n")
	writeFile(t, subjects["two"].Dir, "auth.go", "package auth\n\n// two\n")
	writeFile(t, subjects["two"].Dir, "alone.go", "package main\n")
	writeFile(t, subjects["three"].Dir, "elsewhere.go", "package main\n")

	overlaps, err := verifier.Overlaps()
	if err != nil {
		t.Fatalf("Overlaps: %v", err)
	}
	if len(overlaps) != 1 {
		t.Fatalf("%d overlaps, want the one shared file: %+v", len(overlaps), overlaps)
	}
	if overlaps[0].Path != "auth.go" {
		t.Errorf("the overlap is on %q", overlaps[0].Path)
	}
	if len(overlaps[0].Agents) != 2 {
		t.Fatalf("the overlap names %v, want both agents", overlaps[0].Agents)
	}
	if overlaps[0].Agents[0] != "one" || overlaps[0].Agents[1] != "two" {
		t.Errorf("the agents are %v, want a stable order", overlaps[0].Agents)
	}
	if overlaps[0].Contested() {
		t.Error("two edits were reported as contested, which is reserved for a delete against an edit")
	}
}

func TestNoOverlapIsTheCommonCase(t *testing.T) {
	verifier, _, subjects := harness(t, "one", "two")

	writeFile(t, subjects["one"].Dir, "one.go", "package main\n")
	writeFile(t, subjects["two"].Dir, "two.go", "package main\n")

	overlaps, err := verifier.Overlaps()
	if err != nil {
		t.Fatalf("Overlaps: %v", err)
	}
	if len(overlaps) != 0 {
		t.Errorf("two agents on separate files reported %+v", overlaps)
	}
}

// A delete against an edit is the case most likely to actually conflict, and a plain file list
// would show it as an ordinary overlap.
func TestADeleteAgainstAnEditIsMarked(t *testing.T) {
	verifier, _, subjects := harness(t, "keeper", "remover")

	writeFile(t, subjects["keeper"].Dir, "main.go", "package main\n\n// still here\n")
	run(t, subjects["remover"].Dir, "git", "rm", "main.go")

	overlaps, err := verifier.Overlaps()
	if err != nil {
		t.Fatalf("Overlaps: %v", err)
	}
	if len(overlaps) != 1 {
		t.Fatalf("%d overlaps: %+v", len(overlaps), overlaps)
	}
	if !overlaps[0].Contested() {
		t.Errorf("a delete against an edit is not marked: %+v", overlaps[0])
	}
	if len(overlaps[0].Deleted) != 1 || overlaps[0].Deleted[0] != "remover" {
		t.Errorf("the deleting agent is %v", overlaps[0].Deleted)
	}
}

// A rename is a change to both names. Without the old one, an agent renaming a file and another
// editing it under its old name never show as overlapping, which is how the edit gets lost.
func TestARenameOverlapsWithAnEditToTheOldName(t *testing.T) {
	verifier, _, subjects := harness(t, "renamer", "editor")

	run(t, subjects["renamer"].Dir, "git", "mv", "main.go", "app.go")
	writeFile(t, subjects["editor"].Dir, "main.go", "package main\n\n// edited in place\n")

	overlaps, err := verifier.Overlaps()
	if err != nil {
		t.Fatalf("Overlaps: %v", err)
	}

	var found bool
	for _, overlap := range overlaps {
		if overlap.Path == "main.go" && len(overlap.Agents) == 2 {
			found = true
		}
	}
	if !found {
		t.Errorf("a rename and an edit to the same file did not overlap: %+v", overlaps)
	}
}

// The most contested file leads, because the list is read from the top and that is the one to look
// at first.
func TestTheMostTouchedFileLeads(t *testing.T) {
	verifier, _, subjects := harness(t, "one", "two", "three")

	for _, name := range []string{"one", "two", "three"} {
		writeFile(t, subjects[name].Dir, "hot.go", "package main\n\n// "+name+"\n")
	}
	writeFile(t, subjects["one"].Dir, "warm.go", "package main\n\n// one\n")
	writeFile(t, subjects["two"].Dir, "warm.go", "package main\n\n// two\n")

	overlaps, err := verifier.Overlaps()
	if err != nil {
		t.Fatalf("Overlaps: %v", err)
	}
	if len(overlaps) != 2 {
		t.Fatalf("%d overlaps: %+v", len(overlaps), overlaps)
	}
	if overlaps[0].Path != "hot.go" {
		t.Errorf("the list leads with %q, want the file three agents touched", overlaps[0].Path)
	}
}

// One agent whose worktree cannot be read must not hide the overlaps among the others. Failing the
// whole call would turn a removed worktree into a blank screen at the moment somebody is deciding
// what to merge.
func TestAnUnreadableAgentDoesNotHideTheRest(t *testing.T) {
	verifier, _, subjects := harness(t, "one", "two")

	writeFile(t, subjects["one"].Dir, "auth.go", "package auth\n\n// one\n")
	writeFile(t, subjects["two"].Dir, "auth.go", "package auth\n\n// two\n")

	broken := subjects["two"]
	broken.Dir = "/nowhere/at/all"
	verifier.Watch([]Subject{subjects["one"], broken, {Agent: "three", Dir: subjects["one"].Dir}})

	if _, err := verifier.Overlaps(); err != nil {
		t.Fatalf("Overlaps returned an error because one worktree is gone: %v", err)
	}
}
