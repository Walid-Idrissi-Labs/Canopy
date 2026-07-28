// Package session owns conversations and runs turns against providers.
//
// This is the thing the interface talks to. It holds the authoritative view of every session, runs
// a turn in the background, folds the stream into that view as it arrives, and publishes a
// notification per update. The interface never touches a provider and never holds conversation
// state of its own: it renders a snapshot, and re-renders when told something moved.
//
// That split is what makes streaming survivable. Tokens arrive faster than a terminal can usefully
// redraw, so notifications coalesce; they can coalesce safely only because they carry nothing, and
// the growing reply lives in the snapshot instead.
package session

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/agent"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/git"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/pricing"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/store"
)

// Resolver produces the client and pricing identity for a named credential.
//
// An interface rather than a concrete key store, so the engine can be driven in tests without a
// keychain, and so the engine never learns what a credential looks like. It asks for a name and
// gets something that can answer.
type Resolver interface {
	// Resolve returns the client for a credential name. An empty name means "the obvious one",
	// which is only obvious when exactly one credential is stored.
	Resolve(name, model string) (client core.ProviderClient, id pricing.ModelID, err error)
}

// Engine holds every session and runs their turns.
type Engine struct {
	mu       sync.Mutex
	sessions map[string]*core.Session
	order    []string
	cancels  map[string]context.CancelFunc

	resolver Resolver
	events   *store.Broker

	// tools is what agents in this engine may do, and trust is how much of it they may do without
	// asking. Both nil or zero means a conversation with no tools, which is what a chat with no
	// workspace is and is a legitimate thing to want.
	tools    *core.ToolRegistry
	trust    core.TrustLevel
	approver agent.Approver
	trail    *permission.Trail

	// sessionMode is the mode one conversation is in, set by whoever is watching it.
	//
	// The mode rather than the level, because two modes share a level: runway and cruise can both do
	// anything, and what separates them is what happens to a turn afterwards. Storing the level
	// alone would make them the same setting with two names.
	sessionMode map[string]core.Mode

	// verifying is the sessions whose last turn is terminal and not yet finished with.
	//
	// A turn in a mode that keeps the workspace green is not done when the model stops talking. The
	// checks still have to run and the workspace may still be put back, and until both have happened
	// the conversation is not free. Without this the sequence is: the turn goes terminal, the
	// interface sees nothing running and accepts the next message, that turn starts editing, and then
	// the first turn's gate comes back red and restores a checkpoint over work the second one did.
	// Losing somebody's work to a safety feature is the worst thing in this file.
	verifying map[string]bool

	// restoredMode is a mode read back from history and not yet applied, by session.
	//
	// Kept apart from sessionMode because a stored mode is a request rather than a fact: runway needs
	// a gate and cruise needs checkpoints, and history is attached before either exists. Applying it
	// at load would either refuse every mode or, worse, restore runway into a run with no gate, where
	// nothing checks the turns it promises to check. So it waits here and is resolved on first use,
	// by which time everything it depends on has been attached or has definitively not been.
	restoredMode map[string]string

	// storage is optional. An engine without one still works completely and forgets everything on
	// exit, which is what the tests want and what a first run before the config directory exists
	// gets. Persistence being optional rather than assumed is also what stops a storage failure
	// from taking the conversation down with it.
	storage *Storage

	// onStorageError is how a persistence failure reaches somebody. Losing the ability to save is
	// worth saying and is not worth ending a turn over: the answer on screen is still the answer.
	onStorageError func(error)

	// turns counts the turns in flight, so shutdown can wait for them to close out.
	//
	// Cancelling a turn is not the same as it being finished: the context comes down, the stream
	// unwinds, and only then does the turn record that it was interrupted and keep whatever text
	// had arrived. Quitting without waiting for that loses the partial that cancelling went to the
	// trouble of keeping, which is the whole point of cancelling rather than killing.
	turns sync.WaitGroup

	// writes counts the saves in flight, so shutdown can wait for them.
	//
	// Needed because a turn becomes visibly terminal a moment before it is on disk: the state is set
	// under the lock and the write happens after it is released, so that a disk write never blocks
	// the interface from reading. Anything that watches for a turn to finish and then shuts down
	// would otherwise close the database out from under that write.
	writes sync.WaitGroup

	// grants are the approvals in force per session.
	grants map[string]*permission.Grants

	// pending is the question each session is waiting on, at most one at a time.
	pending map[string]*Prompt

	// checkpoints captures the worktree before each turn, when there is a worktree to capture.
	checkpoints *git.Taker

	// gate is the check runway mode runs after every turn. Nil where there is nothing to check
	// with, which is what stops runway being offered rather than something that quietly downgrades
	// it. See gate.go.
	gate Gate

	// agents are the named workers, and agentOrder is the order they were created in.
	agents     map[string]*Agent
	agentOrder []string

	// isolation is how a worktree is made for an agent that wants one, and agentTools holds the
	// registries of the agents that have. Keyed by session ID rather than agent name, because a
	// turn knows which session it belongs to and nothing else.
	//
	// Both nil in the ordinary case. An agent is not a branch, and an engine that never isolates
	// anything is the normal way to run Canopy rather than a degraded one.
	isolation *Isolation

	// steering holds guidance typed while a turn was in flight, per session, waiting for the next
	// turn boundary. See steer.go: this is the queue that makes correcting an agent cheap rather
	// than costing whatever it was in the middle of.
	steering map[string][]string

	// budgets are the spending caps. See budget.go: the cap is checked before a request goes out,
	// which is what makes it a guardrail rather than a receipt.
	budgets    *budgets
	agentTools map[string]*core.ToolRegistry

	// projectID is the repository this run belongs to, and projects remembers that identity per
	// session across restarts. The history database is shared across repositories; without this,
	// "this project's cost history" is actually every conversation on the machine.
	projectID string
	projects  map[string]string

	nextID int
}

// New builds an engine that forgets everything when it exits.
func New(resolver Resolver) *Engine {
	return &Engine{
		sessions:    map[string]*core.Session{},
		cancels:     map[string]context.CancelFunc{},
		resolver:    resolver,
		events:      store.NewBroker(),
		budgets:     newBudgets(),
		projects:    make(map[string]string),
		sessionMode: make(map[string]core.Mode),
		verifying:   make(map[string]bool),
	}
}

// modeUnusableLocked reports why a mode cannot be entered here, or nil if it can.
//
// One function rather than the checks written out at each site, because there are now three: setting
// a mode, restoring one from history, and carrying one across a fork. Three copies of a safety rule
// is three places for it to be updated twice.
func (e *Engine) modeUnusableLocked(sessionID string, mode core.Mode) error {
	// An engine with no tools attached has no configured level and so no ceiling to enforce. Valid
	// is what tells the two apart: an unset level is not a restrictive one, and treating it as
	// restrictive would refuse every mode on a conversation that has no tools to govern anyway.
	if ceiling := e.configuredTrustLocked(sessionID); ceiling.Valid() && !ceiling.AtLeast(mode.Trust) {
		return fmt.Errorf("this agent is %s, so it cannot be put in %s mode, which needs %s",
			ceiling, mode.Name, mode.Trust)
	}
	if mode.NeedsUndo && e.checkpoints == nil {
		return fmt.Errorf(
			"%s needs to be able to put the workspace back, which needs a git repository", mode.Name)
	}
	if mode.KeepsGreen && e.gate == nil {
		return fmt.Errorf(
			"%s needs to be able to check the workspace, which needs a configured test", mode.Name)
	}
	return nil
}

// modeLocked is the mode a conversation is in, resolving anything read back from history on the way.
//
// The one place both Mode and trustForLocked go through, so what the box shows and what the
// permission layer enforces cannot come from two different answers.
func (e *Engine) modeLocked(sessionID string) core.Mode {
	if mode, ok := e.sessionMode[sessionID]; ok && mode.Name != "" {
		return mode
	}

	if name, ok := e.restoredMode[sessionID]; ok {
		// Consumed either way. A stored mode that cannot be honoured here should be reported once and
		// then stop being asked about, rather than failing the same check on every tool call.
		delete(e.restoredMode, sessionID)

		if mode, known := core.ModeByName(name); known {
			if e.modeUnusableLocked(sessionID, mode) == nil {
				e.sessionMode[sessionID] = mode
				return mode
			}
			// Stored but not usable here: runway reopened where nothing can check the workspace, or a
			// mode above what this agent is configured for.
			//
			// Down to build, never to the level the stored mode happens to share with a weaker one.
			// Runway and cruise are both broad, so resolving by level would turn runway, which reverts
			// a turn that ends red, into cruise, which keeps it. Silently becoming the more dangerous
			// mode below is the worst way a safety setting can fail. Build is at or below every mode
			// that can reach here, because plan is usable everywhere and so never does.
			return e.fallBackLocked(sessionID, core.ModeBuild)
		}

		// A name from a version that had a mode this one does not. Nothing can be restored and
		// nothing can be assumed, so the narrowest mode there is, which is visible in the box the
		// moment somebody looks and is one keystroke to leave.
		return e.fallBackLocked(sessionID, core.ModePlan)
	}

	return core.ModeForTrust(e.configuredTrustLocked(sessionID))
}

// fallBackLocked settles a conversation into the named mode, or into plan where even that is too
// much for how the agent is configured.
func (e *Engine) fallBackLocked(sessionID, name string) core.Mode {
	if mode, ok := core.ModeByName(name); ok && e.modeUnusableLocked(sessionID, mode) == nil {
		e.sessionMode[sessionID] = mode
		return mode
	}
	plan, _ := core.ModeByName(core.ModePlan)
	e.sessionMode[sessionID] = plan
	return plan
}

// Trust is how much the agent in one conversation may do without asking.
func (e *Engine) Trust(sessionID string) core.TrustLevel {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.trustForLocked(sessionID)
}

// Mode is what the agent in one conversation is doing.
//
// Falls back to the mode its trust level corresponds to, so an agent configured read-only reports
// that it is planning whether or not anybody used the word. A conversation always has a mode; the
// map only records the ones somebody chose.
func (e *Engine) Mode(sessionID string) core.Mode {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.modeLocked(sessionID)
}

// SetMode changes it, for that conversation alone.
//
// Per conversation because the decision belongs to whoever is watching that one, and enforced rather
// than requested: the mode's level is what the permission layer decides against and what the tool
// list handed to the model is filtered by. That is the difference between plan mode and asking a
// model nicely to plan. An agent told to plan and choosing to edit a file anyway is stopped by the
// permission layer, which is the only kind of instruction worth relying on.
// A mode can lower what an agent may do and can never raise it above what its configuration allows.
// That is the one rule here worth stating as a rule: an agent started read-only is read-only because
// somebody decided it should be, and a keystroke in a chat window is not the place to overrule that.
// The refusal is returned rather than swallowed, because a key that silently does nothing reads as a
// key that is broken.
func (e *Engine) SetMode(sessionID string, mode core.Mode) error {
	e.mu.Lock()
	if err := e.modeUnusableLocked(sessionID, mode); err != nil {
		e.mu.Unlock()
		return err
	}
	if e.sessionMode == nil {
		e.sessionMode = make(map[string]core.Mode)
	}
	e.sessionMode[sessionID] = mode
	// Whatever history had to say is settled now, so it must not be resolved later over the top of a
	// decision somebody has just made.
	delete(e.restoredMode, sessionID)
	e.mu.Unlock()

	// Written after the lock is released, because persist takes it. Remembered by name rather than
	// by value: the prompt and the trust level belong to this build of Canopy and the name is the
	// part somebody chose, so an upgrade that improves a prompt reaches conversations that already
	// exist instead of leaving them on the old one forever.
	e.persist(func(s *Storage) error { return s.SaveSessionMode(sessionID, mode.Name) })
	return nil
}

// ProjectOf is the project a conversation belongs to, or empty if it was recorded without one.
//
// Empty is a real answer and not a missing one. Conversations from before history was scoped by
// project have no association, and so do any started outside a repository, and neither should be
// treated as belonging to whichever directory happens to be asking.
func (e *Engine) ProjectOf(sessionID string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.projects[sessionID]
}

// SetProjectID scopes new sessions and cost analysis to one project.
func (e *Engine) SetProjectID(projectID string) {
	e.mu.Lock()
	e.projectID = projectID
	e.mu.Unlock()
}

// WithStorage attaches persistence and loads whatever is already there.
//
// Loading at attach time rather than lazily, because the alternative is a session list that fills in
// after the interface has already drawn an empty one, which reads as history having been lost.
func (e *Engine) WithStorage(storage *Storage, onError func(error)) error {
	e.mu.Lock()
	e.storage = storage
	e.onStorageError = onError
	e.mu.Unlock()

	saved, err := storage.List()
	if err != nil {
		return err
	}

	// Oldest first, so the in memory order matches the order sessions were created and the numeric
	// part of a generated ID keeps meaning what it looks like it means.
	for i := len(saved) - 1; i >= 0; i-- {
		full, err := storage.Load(saved[i].ID)
		if err != nil {
			return err
		}
		e.mu.Lock()
		e.sessions[full.ID] = &full
		e.order = append(e.order, full.ID)
		e.nextID = max(e.nextID, idNumber(full.ID))
		e.mu.Unlock()
	}

	projects, err := storage.projectIDs()
	if err != nil {
		return err
	}
	modes, err := storage.sessionModes()
	if err != nil {
		return err
	}
	e.mu.Lock()
	for sessionID, projectID := range projects {
		e.projects[sessionID] = projectID
	}
	if len(modes) > 0 && e.restoredMode == nil {
		e.restoredMode = make(map[string]string, len(modes))
	}
	for sessionID, mode := range modes {
		e.restoredMode[sessionID] = mode
	}
	e.mu.Unlock()
	return nil
}

// idNumber reads the counter out of a generated session ID.
//
// Needed because the counter has to carry across restarts. Without it the first session of a new
// run would be called session-1 again and collide with the one already on disk, silently appending
// tonight's turns to a conversation from last week.
func idNumber(id string) int {
	rest, ok := strings.CutPrefix(id, "session-")
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 0
	}
	return n
}

// SetClock replaces the clock. Only useful in tests.
func (e *Engine) SetClock(now func() time.Time) { e.events.SetClock(now) }

// WithTools gives agents in this engine something to do besides talk.
//
// The approver is separate from the tools because who answers a permission prompt is a property of
// how Canopy is being run, not of what the agent can do. A terminal asks; a scheduled run has a
// policy; neither should be assumed by the thing holding the tool list.
func (e *Engine) WithTools(
	tools *core.ToolRegistry, trust core.TrustLevel, approver agent.Approver,
) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tools = tools
	e.trust = trust
	e.approver = approver
	if e.trail == nil {
		e.trail = permission.NewTrail()
	}
}

// WithCheckpoints captures the worktree before every turn, so any turn can be undone.
//
// Optional, because a conversation in a directory that is not a git repository is a legitimate thing
// and should not be refused for want of somewhere to store a snapshot.
func (e *Engine) WithCheckpoints(taker *git.Taker) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.checkpoints = taker
}

// checkpointBefore captures the worktree, returning an empty string when there is nowhere to.
//
// A failure to checkpoint does not stop the turn. Refusing to answer because a snapshot could not be
// taken would be a tool that stops working in a directory it cannot fully manage, and the turn is
// still what the user asked for. It is reported instead, since somebody who thinks they can undo and
// cannot is worse off than somebody who knows they cannot.
func (e *Engine) checkpointBefore(ctx context.Context, sessionID, turnID string) string {
	e.mu.Lock()
	taker, report := e.checkpoints, e.onStorageError
	e.mu.Unlock()

	if taker == nil {
		return ""
	}
	checkpoint, err := taker.Take(ctx, turnID, "before "+sessionID)
	if err != nil {
		if report != nil {
			report(fmt.Errorf("could not checkpoint before this turn, so it cannot be undone: %w", err))
		}
		return ""
	}
	return checkpoint.Commit
}

// Undo restores the worktree to how it was before a turn ran.
//
// The conversation is left alone. Reverting the files and deleting the messages would destroy the
// record of what was tried, which is the thing somebody undoing wants to look at afterwards to work
// out what to ask for instead.
func (e *Engine) Undo(ctx context.Context, sessionID, turnID string) error {
	e.mu.Lock()
	taker := e.checkpoints
	session, ok := e.sessions[sessionID]
	var commit string
	if ok {
		for _, turn := range session.Turns {
			if turn.ID == turnID {
				commit = turn.Checkpoint
				break
			}
		}
	}
	e.mu.Unlock()

	switch {
	case !ok:
		return fmt.Errorf("no session %q", sessionID)
	case taker == nil:
		return errors.New("this directory is not a git repository, so nothing was checkpointed")
	case commit == "":
		return fmt.Errorf("turn %s has no checkpoint, so there is nothing to restore", turnID)
	}

	if err := taker.Restore(ctx, git.Checkpoint{Commit: commit}); err != nil {
		return err
	}
	e.events.Publish(core.Event{Kind: core.EventSessionUpdated, SessionID: sessionID})
	return nil
}

// Trail is the audit record of every tool call these agents have made.
func (e *Engine) Trail() *permission.Trail {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.trail == nil {
		e.trail = permission.NewTrail()
	}
	return e.trail
}

// Events returns a channel of notifications. See core.SnapshotStore for the contract.
func (e *Engine) Events(afterSequence uint64) <-chan core.Event {
	return e.events.Subscribe(afterSequence)
}

// Close stops every running turn, finishes writing, and shuts the event stream down.
//
// It closes the storage it was given, because WithStorage hands ownership over. Two owners of one
// database handle is how a file gets closed while something else is still writing to it, which is
// exactly the bug the write counter here exists to prevent.
func (e *Engine) Close() {
	e.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(e.cancels))
	for _, cancel := range e.cancels {
		cancels = append(cancels, cancel)
	}
	e.cancels = map[string]context.CancelFunc{}
	storage := e.storage
	e.storage = nil
	e.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}

	// In this order: the turns settle first, then the writes those turns produce.
	e.turns.Wait()
	e.writes.Wait()

	if storage != nil {
		if err := storage.Close(); err != nil && e.onStorageError != nil {
			e.onStorageError(err)
		}
	}
	e.events.Close()
}

// Sessions returns every session, oldest first.
//
// A copy, deeply enough that a caller holding the result cannot be affected by a turn that arrives
// afterwards. A shared backing array is the classic way an immutable snapshot quietly stops being
// one, and here it would mean the interface rendering half of one update and half of the next.
func (e *Engine) Sessions() []core.Session {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sessionsLocked()
}

func (e *Engine) sessionsLocked() []core.Session {
	out := make([]core.Session, 0, len(e.order))
	for _, id := range e.order {
		out = append(out, copySession(*e.sessions[id]))
	}
	return out
}

// Session returns one session by ID.
func (e *Engine) Session(id string) (core.Session, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.sessions[id]
	if !ok {
		return core.Session{}, false
	}
	return copySession(*s), true
}

func copySession(s core.Session) core.Session {
	s.Turns = append([]core.Turn(nil), s.Turns...)
	s.Compactions = append([]core.Compaction(nil), s.Compactions...)
	s.Forks = append([]core.ForkRef(nil), s.Forks...)
	return s
}

// Create starts a new session.
func (e *Engine) Create(keyName, model string) core.Session {
	e.mu.Lock()
	e.nextID++
	now := e.events.Now()
	s := &core.Session{
		ID:        fmt.Sprintf("session-%d", e.nextID),
		KeyName:   keyName,
		Model:     model,
		CreatedAt: now,
		UpdatedAt: now,
	}
	e.sessions[s.ID] = s
	e.order = append(e.order, s.ID)
	e.projects[s.ID] = e.projectID
	out := copySession(*s)
	e.mu.Unlock()

	e.persistSession(out)
	e.events.Publish(core.Event{Kind: core.EventSessionsChanged, SessionID: s.ID})
	return out
}

// Sessions and turns are written at two moments and no others: when a turn starts, so the question
// survives a crash, and when it reaches a terminal state, so the answer does. Nothing is written per
// token. That would turn one streamed reply into thousands of transactions to buy a guarantee
// nobody asked for, which is that the last few words of a reply still arriving when the process
// died should also be kept.

// persist runs one write against whatever storage is attached, counting it so shutdown can wait.
//
// The counter is taken under the same lock that reads the storage handle. Taking it afterwards
// would leave a window where Close sees no writes outstanding and closes the database a moment
// before this one starts.
func (e *Engine) persist(write func(*Storage) error) {
	e.mu.Lock()
	storage, report := e.storage, e.onStorageError
	if storage != nil {
		e.writes.Add(1)
	}
	e.mu.Unlock()

	if storage == nil {
		return
	}
	defer e.writes.Done()

	if err := write(storage); err != nil && report != nil {
		report(err)
	}
}

func (e *Engine) persistSession(session core.Session) {
	e.mu.Lock()
	projectID := e.projects[session.ID]
	e.mu.Unlock()
	e.persist(func(s *Storage) error { return s.SaveSessionForProject(session, projectID) })
}

func (e *Engine) persistTurn(sessionID string, ordinal int, turn core.Turn) {
	e.persist(func(s *Storage) error { return s.SaveTurn(sessionID, ordinal, turn) })
}

// ErrBusy is returned when a session already has a turn in flight.
//
// Its own error because the caller's response is different: a second message while the first is
// still streaming is a person typing ahead, not a failure, and the interface queues or refuses
// rather than showing them something that reads as broken.
var ErrBusy = errors.New("this session is already working on a turn")

// Send asks a question and runs the turn in the background.
//
// Returns as soon as the turn is registered, because a terminal that blocked until the answer
// arrived could not draw the answer arriving. Everything after this point reaches the caller
// through the snapshot and the event stream.
func (e *Engine) Send(sessionID, prompt string) (turnID string, err error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", errors.New("an empty message has nothing to answer")
	}

	e.mu.Lock()
	s, ok := e.sessions[sessionID]
	if !ok {
		e.mu.Unlock()
		return "", fmt.Errorf("no session %q", sessionID)
	}
	if _, running := s.Active(); running {
		e.mu.Unlock()
		return "", ErrBusy
	}
	// Terminal is not the same as finished. A turn whose mode keeps the workspace green is still
	// being checked after its last word, and may still be rolled back, so the conversation stays
	// closed until that has settled. Wrapped rather than returned bare, so callers matching on
	// ErrBusy keep working while a person reads why this one is different from the ordinary wait.
	if e.verifying[sessionID] {
		e.mu.Unlock()
		return "", fmt.Errorf(
			"%w: the workspace is still being checked after the last turn, which may still be undone",
			ErrBusy)
	}
	e.mu.Unlock()

	// Before the turn is registered, not after the reply comes back. A cap checked afterwards is a
	// receipt: it says what was spent and it did not stop the spending. Checked here, a paused agent
	// has not made the request at all.
	if err := e.checkBudget(sessionID); err != nil {
		return "", err
	}
	e.mu.Lock()
	s = e.sessions[sessionID]
	if s == nil {
		e.mu.Unlock()
		return "", fmt.Errorf("no session %q", sessionID)
	}

	now := e.events.Now()
	turnID = fmt.Sprintf("%s-turn-%d", sessionID, len(s.Turns)+1)
	s.Turns = append(s.Turns, core.Turn{
		ID:        turnID,
		State:     core.TurnPending,
		Request:   core.Message{Role: core.RoleUser, Text: prompt},
		Model:     s.Model,
		StartedAt: now,
	})
	s.UpdatedAt = now

	// The title is the first thing said, which is what a person recognises a session by in a list.
	// Set once and never rewritten, so a session does not rename itself out from under someone.
	if s.Title == "" {
		s.Title = summarise(prompt)
	}

	history := s.History()
	keyName, model := s.KeyName, s.Model

	ctx, cancel := context.WithCancel(context.Background())
	e.cancels[sessionID] = cancel
	started := s.Turns[len(s.Turns)-1]
	ordinal := len(s.Turns) - 1
	saved := copySession(*s)
	e.mu.Unlock()

	// Taken before the turn runs and before it is persisted, so the snapshot describes the worktree
	// as it was when the question was asked rather than part way through the answer.
	if commit := e.checkpointBefore(context.Background(), sessionID, turnID); commit != "" {
		e.update(sessionID, turnID, func(t *core.Turn) { t.Checkpoint = commit })
		started.Checkpoint = commit
		saved = e.snapshot(sessionID)
	}

	e.persistSession(saved)
	e.persistTurn(sessionID, ordinal, started)
	e.publishTurn(sessionID, turnID, false)

	e.turns.Add(1)
	go e.run(ctx, sessionID, turnID, keyName, model, history)
	return turnID, nil
}

// Cancel stops the turn in flight, keeping whatever has arrived.
func (e *Engine) Cancel(sessionID string) {
	e.mu.Lock()
	cancel := e.cancels[sessionID]
	e.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// run drives one turn from request to terminal state.
//
// Every exit path ends in finish, which is the only place a turn becomes terminal. One exit means
// one place where the end time gets set, the event gets marked final and the cancel gets released,
// which is the difference between a turn that always closes out and one that closes out on the
// paths somebody remembered.
func (e *Engine) run(
	ctx context.Context, sessionID, turnID, keyName, model string, history []core.Message,
) {
	defer func() {
		e.mu.Lock()
		delete(e.cancels, sessionID)
		e.mu.Unlock()

		// Guidance queued while this turn ran is delivered here, after the cancel is released and
		// before the wait group is marked done. Released first because delivering starts a new turn
		// and would otherwise find this session still registered as busy. Done last so a Close that
		// is waiting on turns waits for the steered turn as well, rather than shutting down between
		// the two and losing it.
		e.deliverSteering(sessionID)
		e.turns.Done()
	}()

	client, id, err := e.resolver.Resolve(keyName, model)
	if err != nil {
		e.finish(sessionID, turnID, failureState(ctx), err, core.Usage{}, "")
		return
	}

	e.update(sessionID, turnID, func(t *core.Turn) {
		t.State = core.TurnStreaming
		t.Provider = client.Name()
	})

	e.mu.Lock()
	tools, trust := e.toolsForLocked(sessionID)
	approver, trail := e.approver, e.trail
	e.mu.Unlock()

	loop := &agent.Loop{
		Client: client,
		Tools:  tools,
		Trust:  trust,
		// Asked again before every tool call, so changing mode while a reply is arriving takes hold
		// on the next thing the model tries rather than on the next thing the user says. The level
		// above is the one this turn started on and is the fallback.
		LiveTrust: func() core.TrustLevel { return e.Trust(sessionID) },
		Grants:    e.grantsFor(sessionID),
		Trail:     trail,
		Approver:  approver,
		AgentID:   sessionID,
		SessionID: sessionID,
	}

	// The mode's own prompt, sent as the system prompt. Without it the level is enforced and never
	// explained, so a planning agent tries to edit, is refused, tries again, and spends the turn
	// thrashing against a boundary nobody told it about. Read at the top of the turn rather than per
	// call, because a system prompt that changed mid conversation would rewrite what the model
	// believes it was told earlier.
	request := core.Request{Model: model, Messages: history, System: e.Mode(sessionID).Prompt}

	outcome, err := loop.Run(ctx, request,
		&turnObserver{engine: e, sessionID: sessionID, turnID: turnID})
	if err != nil {
		// failureState rather than a flat TurnFailed: a provider can take several seconds to send
		// its first byte, and somebody who presses escape in that window has stopped the turn
		// rather than hit a fault. Reporting it as failed would put an error on screen for
		// something the user did on purpose.
		e.finish(sessionID, turnID, failureState(ctx), err, core.Usage{}, client.Name())
		return
	}

	usage, _ := pricing.Apply(id, outcome.Usage)
	state := core.TurnStateFromStopReason(outcome.Stop)

	// A turn stopped by a step or token bound is a failure with a specific explanation, not a
	// generic one. "It went in circles" is something a user can act on; "the turn failed" is not.
	var reason error
	if outcome.LimitHit != "" {
		state = core.TurnFailed
		reason = errors.New(outcome.LimitHit)
	}
	// A stop reason that maps to failure but carries no explanation leaves the user with "the turn
	// failed without an explanation", which is true and useless. Naming the reason at least says
	// which vocabulary to go and look it up in, and that string comes from the provider adapter
	// rather than from here, so it is the provider's own word for what happened.
	if state == core.TurnFailed && reason == nil {
		reason = fmt.Errorf("the provider ended the turn with %q, which is not a way a turn "+
			"finishes successfully", outcome.Stop)
	}

	// Claimed before the turn is marked terminal, which is the whole point of doing it here rather
	// than inside keepGreen. finish is what makes the turn terminal, and a terminal turn is what the
	// interface reads as "you may send the next message". Claiming afterwards leaves a window where
	// a second turn can be accepted and start editing while the first one's checks are still running,
	// and the first one's rollback then restores over work the second one did.
	held := state == core.TurnComplete && e.holdConversation(sessionID)

	e.finish(sessionID, turnID, state, reason, usage, client.Name())

	// Only a turn that finished on its own terms is worth checking. A cancelled or failed turn has
	// nothing to keep, and running the project's tests over the wreckage of one somebody interrupted
	// would spend a minute to conclude what the interruption already said.
	//
	// Deliberately after finish, with a fresh context. The turn's own context is cancelled the moment
	// it is terminal, and the checks are about what that turn left behind rather than part of it.
	if held {
		e.keepGreen(context.WithoutCancel(ctx), sessionID, turnID)
	}
}

// holdConversation closes a conversation to new messages while its last turn is being checked.
//
// Returns whether it engaged, so the caller knows whether there is anything to release. False for
// every conversation whose mode does not promise to keep the workspace green, which is most of them:
// nothing is going to be checked, nothing can be rolled back, and holding the conversation shut
// would be a wait with no purpose.
func (e *Engine) holdConversation(sessionID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.gateAppliesLocked(sessionID) {
		return false
	}
	if e.verifying == nil {
		e.verifying = map[string]bool{}
	}
	e.verifying[sessionID] = true
	return true
}

// releaseConversation reopens it, whatever the checks concluded.
func (e *Engine) releaseConversation(sessionID string) {
	e.mu.Lock()
	delete(e.verifying, sessionID)
	e.mu.Unlock()
}

// gateAppliesLocked reports whether this conversation's turns have to be checked after they end.
//
// One definition, used by the hold above and by the check itself. Two copies of this would be two
// chances for the conversation to be held shut by something that then never runs.
func (e *Engine) gateAppliesLocked(sessionID string) bool {
	return e.gate != nil && e.modeLocked(sessionID).KeepsGreen
}

// snapshot returns a copy of a session, or the zero value if it has gone.
func (e *Engine) snapshot(sessionID string) core.Session {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s, ok := e.sessions[sessionID]; ok {
		return copySession(*s)
	}
	return core.Session{}
}

// grantsFor returns the approvals in force for a session, creating them on first use.
//
// Per session and never persisted, because an approval that outlives the conversation it was given
// in is one nobody remembers granting.
func (e *Engine) grantsFor(sessionID string) *permission.Grants {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.grants == nil {
		e.grants = map[string]*permission.Grants{}
	}
	if existing, ok := e.grants[sessionID]; ok {
		return existing
	}
	fresh := permission.NewGrants()
	e.grants[sessionID] = fresh
	return fresh
}

// turnObserver folds the loop's running commentary into the session snapshot.
//
// This is the only thing that connects a turn in progress to what is on screen, and every method
// has to be cheap: they run on the loop's goroutine, between tokens.
type turnObserver struct {
	engine    *Engine
	sessionID string
	turnID    string
}

func (o *turnObserver) Text(chunk string) {
	o.engine.update(o.sessionID, o.turnID, func(t *core.Turn) { t.Text += chunk })
}

func (o *turnObserver) Thinking(chunk string) {
	o.engine.update(o.sessionID, o.turnID, func(t *core.Turn) { t.Thinking += chunk })
}

func (o *turnObserver) ToolRequested(call core.ToolCall) {
	// Shown before permission is decided, because the gap between a tool being asked for and being
	// approved is exactly when somebody wants to see what is being proposed.
	o.engine.update(o.sessionID, o.turnID, func(t *core.Turn) {
		t.ToolCalls = append(t.ToolCalls, call)
		t.State = core.TurnAwaitingTools
	})
}

func (o *turnObserver) ToolFinished(_ core.ToolCall, result core.ToolResult) {
	o.engine.update(o.sessionID, o.turnID, func(t *core.Turn) {
		t.ToolResults = append(t.ToolResults, result)
		t.State = core.TurnStreaming
	})
	o.engine.refreshTasks(o.sessionID)
}

// refreshTasks copies the agent's task list onto the session.
//
// Pulled after every tool call rather than pushed by the tool that owns the list. A push would mean
// the task tool holding a reference to the session it was given to, which is backwards: tools are
// handed to sessions, not the other way round, and the tool that knew about its session is the one
// that would break the moment an agent moved into a worktree with its own registry.
//
// Reading the registry for this session rather than a single global one is what keeps two agents'
// lists apart. Sharing one would show each of them the other's plan, which is worse than showing
// neither.
func (e *Engine) refreshTasks(sessionID string) {
	e.mu.Lock()
	tools, _ := e.toolsForLocked(sessionID)
	if tools == nil {
		e.mu.Unlock()
		return
	}
	tasks := tools.Tasks()

	s, ok := e.sessions[sessionID]
	if !ok || core.TasksEqual(s.Tasks, tasks) {
		// Unchanged, which is the overwhelmingly common case: almost no tool call touches the task
		// list, and publishing anyway would redraw the whole screen on every file read.
		e.mu.Unlock()
		return
	}
	s.Tasks = tasks
	s.UpdatedAt = e.events.Now()
	out := copySession(*s)
	e.mu.Unlock()

	e.persistSession(out)
	e.events.Publish(core.Event{Kind: core.EventSessionsChanged, SessionID: sessionID})
}

func (o *turnObserver) StepFinished(usage core.Usage) {
	// A running total, so a turn that takes twenty steps shows what it has spent before it ends
	// rather than only afterwards.
	o.engine.update(o.sessionID, o.turnID, func(t *core.Turn) {
		t.Usage = t.Usage.Add(usage)
	})
}

// failureState decides whether something that went wrong was a fault or a person pressing escape.
//
// The context is the authority, not the error. Cancelling an in flight request surfaces as a
// transport failure at whatever layer happened to be waiting, and every one of those layers would
// otherwise have to recognise its own vendor's phrasing of "cancelled". Asking the context instead
// gives one answer that cannot drift.
func failureState(ctx context.Context) core.TurnState {
	if ctx.Err() != nil {
		return core.TurnInterrupted
	}
	return core.TurnFailed
}

// update applies a change to a turn and publishes a coalescable notification.
func (e *Engine) update(sessionID, turnID string, change func(*core.Turn)) {
	e.mu.Lock()
	turn := e.findLocked(sessionID, turnID)
	if turn == nil {
		e.mu.Unlock()
		return
	}
	change(turn)
	e.sessions[sessionID].UpdatedAt = e.events.Now()
	e.mu.Unlock()

	e.publishTurn(sessionID, turnID, false)
}

// finish closes a turn out.
//
// The only path to a terminal state, and the only place that publishes a final event. A final event
// may never be coalesced, so this is what guarantees the last thing anyone hears about a turn is how
// it ended rather than that it was streaming.
func (e *Engine) finish(
	sessionID, turnID string, state core.TurnState, err error, usage core.Usage, provider string,
) {
	e.mu.Lock()
	turn := e.findLocked(sessionID, turnID)
	if turn == nil || turn.State.Terminal() {
		// Already closed out. Publishing a second final event for the same turn would report a
		// finished turn as finishing again, which reads as a new answer arriving.
		e.mu.Unlock()
		return
	}

	now := e.events.Now()
	turn.State = state
	turn.EndedAt = now
	turn.Usage = usage
	if provider != "" {
		turn.Provider = provider
	}
	if err != nil {
		turn.Error = err.Error()
	}
	// Validate requires a reason on a failed turn, and a turn that failed with no error attached
	// would otherwise be an invalid state nobody could explain.
	if state == core.TurnFailed && turn.Error == "" {
		turn.Error = "the turn failed without an explanation"
	}
	session := e.sessions[sessionID]
	session.UpdatedAt = now
	finished, ordinal := *turn, indexOfLocked(session, turnID)
	saved := copySession(*session)
	e.mu.Unlock()

	// Recorded once, here, because finish is the only path to a terminal state. Counting the spend
	// anywhere else would mean a turn that ended on an unusual path was not billed against the cap,
	// and an unusual path is exactly where an agent burns tokens.
	e.recordSpend(sessionID, usage)

	e.persistTurn(sessionID, ordinal, finished)
	e.persistSession(saved)
	e.publishTurn(sessionID, turnID, true)
}

func indexOfLocked(session *core.Session, turnID string) int {
	for i := range session.Turns {
		if session.Turns[i].ID == turnID {
			return i
		}
	}
	return len(session.Turns) - 1
}

func (e *Engine) findLocked(sessionID, turnID string) *core.Turn {
	s, ok := e.sessions[sessionID]
	if !ok {
		return nil
	}
	for i := range s.Turns {
		if s.Turns[i].ID == turnID {
			return &s.Turns[i]
		}
	}
	return nil
}

func (e *Engine) publishTurn(sessionID, turnID string, final bool) {
	e.events.Publish(core.Event{
		Kind:      core.EventTurnUpdated,
		SessionID: sessionID,
		TurnID:    turnID,
		Final:     final,
	})
}

// summarise turns the first message into a session title.
func summarise(prompt string) string {
	const limit = 48

	title := strings.Join(strings.Fields(prompt), " ")
	if len(title) <= limit {
		return title
	}
	// Cut at a word boundary where there is one nearby, since a title chopped mid word looks like
	// a rendering fault rather than a deliberate truncation.
	cut := title[:limit]
	if space := strings.LastIndex(cut, " "); space > limit/2 {
		cut = cut[:space]
	}
	return cut + "..."
}

// Tools returns the engine's tool registry, and whether one is attached.
//
// Exposed so a caller can add tools after construction. The dispatch tools need a session id to
// route their confirmation to, and that does not exist until the first agent does.
func (e *Engine) Tools() (*core.ToolRegistry, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.tools, e.tools != nil
}

// UseCredential points a session at a different credential and model.
//
// Refused while a turn is running. Changing which key gets billed part way through an answer would
// mean the reply was paid for by one credential and attributed to another, and the transcript would
// be wrong about which model produced it.
//
// The model travels with the credential rather than being set separately, because every provider
// except Anthropic has no default anybody could guess, and a credential switched without one leaves
// a session that looks configured and fails on the next message.
func (e *Engine) UseCredential(sessionID, keyName, model string) error {
	if keyName == "" {
		return errors.New("a credential name is required")
	}

	e.mu.Lock()
	s, ok := e.sessions[sessionID]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("no session %q", sessionID)
	}
	if _, running := s.Active(); running {
		e.mu.Unlock()
		return errors.New("this session is mid answer, so wait for it to finish or stop it first")
	}
	s.KeyName, s.Model = keyName, model
	s.UpdatedAt = e.events.Now()
	saved := copySession(*s)

	// The agent record follows, so the agents view and anything that spawns from this session agree
	// with the session itself rather than showing what it used to run on.
	for i := range e.agents {
		if e.agents[i].SessionID == sessionID {
			e.agents[i].KeyName, e.agents[i].Model = keyName, model
		}
	}
	e.mu.Unlock()

	e.persistSession(saved)
	e.events.Publish(core.Event{Kind: core.EventSessionUpdated, SessionID: sessionID})
	return nil
}
