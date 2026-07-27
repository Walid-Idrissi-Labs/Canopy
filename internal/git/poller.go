package git

// Watching worktrees for change.
//
// Polling rather than filesystem notification, deliberately. An agent writing into a worktree
// produces hundreds of events a second across editors, formatters, build caches and the test runner
// itself, and a notification watcher spends its life debouncing them back into the answer a poll
// gives directly. Polling also degrades honestly: over a network mount or in a container where
// notification silently stops working, a poll keeps returning the right answer more slowly, whereas
// a dead watcher looks exactly like a worktree nobody is touching. That failure mode is a green
// result that never goes stale, which is the one outcome this product cannot have.
//
// D-07 fixes the interval at two seconds, with five as the worst acceptable case.

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// DefaultPollInterval is D-07's answer to how quickly an edit must invalidate a green result.
const DefaultPollInterval = 2 * time.Second

// maxConcurrentPolls bounds how many worktrees are examined at once.
//
// Every poll of a worktree forks git at least twice, so the unbounded version of this loop turns
// twenty agents into forty processes arriving together every two seconds. The cap is what keeps a
// background measurement from competing with the agents it is measuring, which is the acceptance
// criterion for this task.
func maxConcurrentPolls() int {
	n := runtime.NumCPU() / 2
	switch {
	case n < 1:
		return 1
	case n > 4:
		return 4
	default:
		return n
	}
}

// Change is one worktree moving from one revision to another.
//
// From is the zero key the first time a worktree is seen, which reads as unknown. That is accurate
// rather than a special case: before the first poll Canopy genuinely did not know what was in
// there, and any evidence claiming otherwise should have been treated as untrustworthy.
type Change struct {
	WorkspaceID string
	Path        string

	From, To core.RevisionKey

	// Reason explains an unknown To, and is empty otherwise. It goes straight into
	// WorkspaceSnapshot.RevisionError.
	Reason string
}

// Poller watches a set of worktrees and reports when their content changes.
//
// The watched set is replaced wholesale rather than added to and removed from, matching how
// discovery already works: a worktree that has gone away simply stops being in the list, and there
// is no reconciliation step to get wrong.
type Poller struct {
	repo     *Repo
	interval time.Duration

	// onChange is called once per changed worktree, from the poller's own goroutine. It must not
	// block for long, since the next poll waits behind it.
	onChange func(Change)

	mu      sync.Mutex
	watched []core.WorkspaceSnapshot
	seen    map[string]core.RevisionKey
	reasons map[string]string
}

// NewPoller returns a poller for a repository. An interval of zero means DefaultPollInterval.
func NewPoller(repo *Repo, interval time.Duration, onChange func(Change)) *Poller {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	if onChange == nil {
		onChange = func(Change) {}
	}
	return &Poller{
		repo:     repo,
		interval: interval,
		onChange: onChange,
		seen:     make(map[string]core.RevisionKey),
		reasons:  make(map[string]string),
	}
}

// Publishing returns an onChange function that turns changes into revision events.
//
// Separate from the poller so that a test can watch changes without a broker, and so the poller
// never has to know what a broker is. The event carries no revision, per the event contract: a
// consumer re-reads the snapshot, which is the only thing entitled to say what the revision now is.
func Publishing(publish func(core.Event) uint64) func(Change) {
	return func(change Change) {
		publish(core.Event{
			Kind:        core.EventRevisionChanged,
			WorkspaceID: change.WorkspaceID,
		})
	}
}

// Watch replaces the set of worktrees being polled.
//
// Worktrees that have dropped out have their cached content hashes released here rather than at
// some later tidy up, because that cache is the one part of this that grows with every agent a long
// session ever ran.
func (p *Poller) Watch(workspaces []core.WorkspaceSnapshot) {
	current := make(map[string]bool, len(workspaces))
	for _, w := range workspaces {
		current[w.ID] = true
	}

	p.mu.Lock()
	gone := make([]string, 0)
	for _, previous := range p.watched {
		if !current[previous.ID] {
			gone = append(gone, previous.Path)
			delete(p.seen, previous.ID)
			delete(p.reasons, previous.ID)
		}
	}
	p.watched = append([]core.WorkspaceSnapshot(nil), workspaces...)
	p.mu.Unlock()

	if revisions := p.repo.Revisions(); revisions != nil {
		for _, path := range gone {
			revisions.Forget(path)
		}
	}
}

// Revision returns the revision most recently observed for a workspace.
func (p *Poller) Revision(workspaceID string) (core.RevisionKey, string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key, ok := p.seen[workspaceID]
	return key, p.reasons[workspaceID], ok
}

// Run polls until the context is cancelled.
//
// A poll happens immediately rather than after the first interval, so a freshly started Canopy shows
// real revisions rather than a screen of unknowns for two seconds.
func (p *Poller) Run(ctx context.Context) {
	p.Poll(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.Poll(ctx)
		}
	}
}

// Poll examines every watched worktree once and returns what changed.
//
// Exported because it is also the forced refresh: after a checkpoint, or after a tool writes a
// file, waiting up to two seconds to notice a change Canopy caused itself would be silly.
func (p *Poller) Poll(ctx context.Context) []Change {
	// Checked up front as well as inside the workers. A select between an available slot and a
	// cancelled context picks between them at random, so without this an already cancelled poll runs
	// most of the way through and records observations nobody made.
	if ctx.Err() != nil {
		return nil
	}

	p.mu.Lock()
	watched := append([]core.WorkspaceSnapshot(nil), p.watched...)
	p.mu.Unlock()

	type observation struct {
		workspace core.WorkspaceSnapshot
		key       core.RevisionKey
		reason    string
	}
	observations := make([]observation, len(watched))

	var wg sync.WaitGroup
	slots := make(chan struct{}, maxConcurrentPolls())

	for i, workspace := range watched {
		wg.Add(1)
		go func() {
			defer wg.Done()

			if ctx.Err() != nil {
				return
			}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()

			key, reason := p.repo.Revision(ctx, workspace.Path)
			observations[i] = observation{workspace: workspace, key: key, reason: reason}
		}()
	}
	wg.Wait()

	// Comparison and callbacks happen here, in one goroutine and after every worktree has been read.
	// Doing it inside the workers would mean the callback runs concurrently with itself, which turns
	// a simple "tell the store" into something that needs its own locking.
	var changes []Change
	for _, seen := range observations {
		if seen.workspace.ID == "" {
			// The context was cancelled before this slot was filled. Nothing was observed, so there
			// is nothing to report, and in particular no reason to record an unknown revision that
			// was never actually looked at.
			continue
		}

		p.mu.Lock()
		previous, existed := p.seen[seen.workspace.ID]
		previousReason := p.reasons[seen.workspace.ID]
		p.seen[seen.workspace.ID] = seen.key
		p.reasons[seen.workspace.ID] = seen.reason
		p.mu.Unlock()

		// Equal is false for two unknown revisions, by design, so an unreadable worktree would
		// otherwise report a change on every single tick. The reason comparison is what actually
		// decides it in that case: same unknown, same explanation, nothing new to say.
		unchanged := existed && (previous.Equal(seen.key) ||
			(!previous.Known() && !seen.key.Known() && previousReason == seen.reason))
		if unchanged {
			continue
		}

		change := Change{
			WorkspaceID: seen.workspace.ID,
			Path:        seen.workspace.Path,
			From:        previous,
			To:          seen.key,
			Reason:      seen.reason,
		}
		changes = append(changes, change)
		p.onChange(change)
	}
	return changes
}
