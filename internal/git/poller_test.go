package git

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// collector records changes from the poller's goroutine and hands them back safely.
type collector struct {
	mu      sync.Mutex
	changes []Change
	notify  chan struct{}
}

func newCollector() *collector {
	return &collector{notify: make(chan struct{}, 64)}
}

func (c *collector) record(change Change) {
	c.mu.Lock()
	c.changes = append(c.changes, change)
	c.mu.Unlock()

	select {
	case c.notify <- struct{}{}:
	default:
	}
}

func (c *collector) all() []Change {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Change(nil), c.changes...)
}

// waitFor blocks until n changes have arrived or the deadline passes.
func (c *collector) waitFor(t *testing.T, n int, within time.Duration) []Change {
	t.Helper()

	deadline := time.After(within)
	for {
		if got := c.all(); len(got) >= n {
			return got
		}
		select {
		case <-c.notify:
		case <-deadline:
			t.Fatalf("only %d changes arrived within %s, want %d", len(c.all()), within, n)
			return nil
		}
	}
}

func watching(t *testing.T) (*Repo, []core.WorkspaceSnapshot) {
	t.Helper()

	dir := repo_(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}

	found, err := r.Worktrees(context.Background())
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	return r, found
}

// D-07: an edit invalidates a green result within one poll interval. The interval is shortened here
// so the test does not take two seconds, but the mechanism under test is the same one.
func TestAnEditIsSeenWithinOnePollInterval(t *testing.T) {
	repo, worktrees := watching(t)
	seen := newCollector()

	poller := NewPoller(repo, 20*time.Millisecond, seen.record)
	poller.Watch(worktrees)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go poller.Run(ctx)

	// The first change is the baseline, which arrives on the immediate poll rather than after an
	// interval, so a freshly started Canopy is not a screen of unknowns.
	seen.waitFor(t, 1, 2*time.Second)

	write(t, worktrees[0].Path, "tracked.go", "package main\n// edited\n")
	changes := seen.waitFor(t, 2, 2*time.Second)

	edit := changes[len(changes)-1]
	if edit.WorkspaceID != worktrees[0].ID {
		t.Errorf("the change names workspace %q, want %q", edit.WorkspaceID, worktrees[0].ID)
	}
	if edit.From.Equal(edit.To) {
		t.Error("the change reports the same revision on both sides, which is not a change")
	}
	if !edit.To.Known() {
		t.Errorf("the new revision is unknown: %s", edit.Reason)
	}
}

// The event carries no revision, only the subject. Anything else would be a second source of truth
// competing with the snapshot, and the two would eventually disagree.
func TestTheEmittedEventNamesTheWorkspaceAndNothingElse(t *testing.T) {
	repo, worktrees := watching(t)

	var published []core.Event
	poller := NewPoller(repo, time.Hour, Publishing(func(ev core.Event) uint64 {
		published = append(published, ev)
		return uint64(len(published))
	}))
	poller.Watch(worktrees[:1])
	poller.Poll(context.Background())

	if len(published) != 1 {
		t.Fatalf("%d events published, want one for the first observation", len(published))
	}
	event := published[0]
	if event.Kind != core.EventRevisionChanged {
		t.Errorf("the event is %q, want %q", event.Kind, core.EventRevisionChanged)
	}
	if event.WorkspaceID != worktrees[0].ID {
		t.Errorf("the event names %q, want %q", event.WorkspaceID, worktrees[0].ID)
	}
	if event.TestName != "" || event.RunID != "" || event.SessionID != "" {
		t.Errorf("the event carries fields that do not apply to it: %+v", event)
	}
}

// A worktree nobody has touched must not produce a change, or the dashboard flickers and everything
// downstream re-renders every two seconds for nothing.
func TestAnUntouchedWorktreeIsSilentAfterTheFirstLook(t *testing.T) {
	repo, worktrees := watching(t)
	seen := newCollector()

	poller := NewPoller(repo, time.Hour, seen.record)
	poller.Watch(worktrees)

	ctx := context.Background()
	poller.Poll(ctx)
	baseline := len(seen.all())

	for range 5 {
		poller.Poll(ctx)
	}

	if got := len(seen.all()); got != baseline {
		t.Errorf("%d changes after five idle polls, want the %d from the first look", got, baseline)
	}
}

// An unreadable worktree stays unknown, and must not report a fresh change every tick just because
// two unknown revisions never compare equal. That rule is right and this is where it needs handling
// rather than weakening.
func TestARepeatedlyUnknownRevisionIsReportedOnce(t *testing.T) {
	dir := repo_(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	// A fingerprint limit nothing can satisfy, so every poll comes back unknown for the same reason.
	r.revisions = NewRevisions(1)
	write(t, dir, "big.txt", "more than one byte")

	seen := newCollector()
	poller := NewPoller(r, time.Hour, seen.record)
	poller.Watch([]core.WorkspaceSnapshot{{ID: "w1", Path: dir}})

	ctx := context.Background()
	poller.Poll(ctx)
	poller.Poll(ctx)
	poller.Poll(ctx)

	changes := seen.all()
	if len(changes) != 1 {
		t.Fatalf("%d changes for an unchanging unknown revision, want one", len(changes))
	}
	if changes[0].Reason == "" {
		t.Error("the change reports an unknown revision with no reason, so the dashboard has nothing to show")
	}
}

// Dropping a worktree has to release its cached content hashes, or a session that runs fifty agents
// keeps the hash of every file each of them ever wrote.
func TestUnwatchingAWorktreeReleasesWhatWasCachedForIt(t *testing.T) {
	repo, worktrees := watching(t)

	poller := NewPoller(repo, time.Hour, nil)
	poller.Watch(worktrees)
	write(t, worktrees[0].Path, "scratch.txt", "hello")
	poller.Poll(context.Background())

	revisions := repo.Revisions()
	revisions.mu.Lock()
	held := len(revisions.cached)
	revisions.mu.Unlock()
	if held == 0 {
		t.Fatal("nothing was cached, so this test is not testing anything")
	}

	poller.Watch(nil)

	revisions.mu.Lock()
	defer revisions.mu.Unlock()
	if len(revisions.cached) != 0 {
		t.Errorf("%d cached hashes survived unwatching every worktree", len(revisions.cached))
	}
	if _, _, known := poller.Revision(worktrees[0].ID); known {
		t.Error("the poller still remembers a revision for a worktree it no longer watches")
	}
}

// Many worktrees, one poll, bounded work. The assertion is on concurrency rather than on wall clock,
// because a timing threshold on a shared CI runner is a flake generator. What matters is that the
// number of git processes alive at once stays bounded no matter how long the list is.
func TestPollingManyWorktreesStaysBounded(t *testing.T) {
	repo, worktrees := watching(t)

	watched := make([]core.WorkspaceSnapshot, 0, 24)
	for i := range 24 {
		watched = append(watched, core.WorkspaceSnapshot{
			ID:   string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Path: worktrees[0].Path,
		})
	}

	var live, peak int
	var mu sync.Mutex
	poller := NewPoller(repo, time.Hour, func(Change) {})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()
	started := make(chan struct{}, len(watched))
	poller.revision = func(context.Context, string) (core.RevisionKey, string) {
		mu.Lock()
		live++
		if live > peak {
			peak = live
		}
		mu.Unlock()
		started <- struct{}{}
		<-release
		mu.Lock()
		live--
		mu.Unlock()
		return core.RevisionKey{HeadSHA: "abc"}, ""
	}

	poller.Watch(watched)
	done := make(chan []Change, 1)
	go func() { done <- poller.Poll(context.Background()) }()

	for range maxConcurrentPolls() {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("the poller never filled its bounded worker slots")
		}
	}
	select {
	case <-started:
		t.Fatalf("more than %d revision reads started before a slot was released", maxConcurrentPolls())
	case <-time.After(50 * time.Millisecond):
	}
	releaseAll()
	changes := <-done

	if len(changes) != len(watched) {
		t.Errorf("%d changes for %d worktrees on the first look", len(changes), len(watched))
	}
	if peak != maxConcurrentPolls() {
		t.Errorf("revision reads peaked at %d, want the configured bound of %d", peak, maxConcurrentPolls())
	}
}

// The watched set is refreshed concurrently with polling in the running application. A slow
// observation of an agent that has just ended must not land after Watch removed it and resurrect
// its revision or emit a change for a row that no longer exists.
func TestChangingTheWatchSetDiscardsAnInFlightObservation(t *testing.T) {
	repo, worktrees := watching(t)
	poller := NewPoller(repo, time.Hour, nil)
	poller.Watch(worktrees[:1])

	started := make(chan struct{})
	release := make(chan struct{})
	poller.revision = func(context.Context, string) (core.RevisionKey, string) {
		close(started)
		<-release
		return core.RevisionKey{HeadSHA: "old-workspace"}, ""
	}

	done := make(chan []Change, 1)
	go func() { done <- poller.Poll(context.Background()) }()
	<-started
	poller.Watch(nil)
	close(release)

	if changes := <-done; len(changes) != 0 {
		t.Errorf("an observation of a removed workspace was reported: %+v", changes)
	}
	if _, _, ok := poller.Revision(worktrees[0].ID); ok {
		t.Error("an in-flight observation resurrected a workspace after Watch removed it")
	}
}

// Cancelling mid poll must not record an observation nobody made. Recording an unknown revision for
// a worktree that was never examined would turn a shutdown into a wave of false staleness on the
// next start.
func TestACancelledPollRecordsNothing(t *testing.T) {
	repo, worktrees := watching(t)
	seen := newCollector()

	poller := NewPoller(repo, time.Hour, seen.record)
	poller.Watch(worktrees)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	poller.Poll(ctx)

	if got := seen.all(); len(got) != 0 {
		t.Errorf("%d changes were reported from a cancelled poll: %+v", len(got), got)
	}
}
