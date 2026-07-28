package main

// Wiring the verification engine into a running Canopy.
//
// Everything here is optional and every failure is a degradation rather than a refusal. Canopy runs
// in directories that are not repositories, in repositories with no configuration, and in projects
// whose test command is wrong. None of those is a reason to refuse to open a conversation, and all
// of them are reasons to say plainly that there is no evidence rather than to show a green tick.

import (
	"context"
	"fmt"
	"os"
	"time"

	"net/url"
	"sync"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	gitpkg "github.com/Walid-Idrissi-Labs/Canopy/internal/git"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/hooks"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/pricing"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/verify"
)

// verification is everything the review screen needs, kept together so it can be shut down as one.
type verification struct {
	verifier *verify.Verifier
	poller   *gitpkg.Poller
	stop     context.CancelFunc

	// store is what the worktree monitor reads. Fed from the same verifier as the review screen, so
	// the two cannot show different states of the same worktree.
	store *worktrees

	// runner decides which hooks fire and runs them. Nil when the project declares none, which is
	// the ordinary case and is why every use of it is guarded.
	runner *hooks.Runner

	// failures are the hooks that did not work, kept so they can be said out loud on the way out.
	//
	// The acceptance criterion for hooks is that a failing one is visible and never silently
	// swallowed, and the whole point of a hook is that somebody has stopped watching: a failure
	// nobody is told about means they stopped watching something that stopped working. There is
	// nowhere on screen for this yet, because a Canopy event is deliberately a thin notification
	// that a consumer answers by re-reading a snapshot, and a hook failure has no snapshot to
	// re-read. Reported on exit is late, and late is a great deal better than never.
	failuresMu sync.Mutex
	failures   []string
}

// reviewInsights joins the two exact contracts needed by A8-07: the verifier owns current test
// evidence, and the session engine owns exact provider cost plus persisted project history.
type reviewInsights struct {
	*verify.Verifier
	engine    *session.Engine
	projectID string
	mu        sync.Mutex
	recorded  map[string]session.OutcomeSample
}

type modelUsageState int

const (
	noModelUsage modelUsageState = iota
	singleModelUsage
	mixedModelUsage
)

func (r *reviewInsights) CostOutcomes() (tui.CostOutcomeHistory, error) {
	ranking := r.Rank()
	statuses := make(map[string]session.AgentStatus)
	for _, status := range r.engine.AgentStatuses() {
		statuses[status.Agent.Name] = status
	}

	for _, placement := range ranking.Ranked {
		status, ok := statuses[placement.Agent]
		if !ok {
			continue
		}
		conversation, ok := r.engine.Session(status.Agent.SessionID)
		if !ok {
			continue
		}
		model, usage, state := sessionModelUsage(conversation)
		switch state {
		case mixedModelUsage:
			continue
		case noModelUsage:
			continue
		}
		sample := session.OutcomeSample{
			ProjectID: r.projectID,
			SessionID: status.Agent.SessionID,
			Revision:  placement.Revision.String(),
			Agent:     placement.Agent,
			Model:     model,
			CostUSD:   usage.CostUSD,
			CostKnown: usage.CostKnown,
			Passing:   placement.Passing,
			Required:  placement.Required,
		}
		if err := r.recordOutcome(sample); err != nil {
			return tui.CostOutcomeHistory{}, err
		}
	}

	samples, err := r.engine.OutcomeHistory(r.projectID)
	if err != nil {
		return tui.CostOutcomeHistory{}, err
	}
	out := tui.CostOutcomeHistory{CurrentUnranked: len(ranking.Unranked)}
	for _, placement := range ranking.Ranked {
		status, ok := statuses[placement.Agent]
		if !ok {
			continue
		}
		conversation, ok := r.engine.Session(status.Agent.SessionID)
		if !ok {
			continue
		}
		_, _, state := sessionModelUsage(conversation)
		switch state {
		case mixedModelUsage:
			out.CurrentMixedModel++
		case noModelUsage:
			out.CurrentNoUsage++
		}
	}
	for _, sample := range samples {
		out.Samples = append(out.Samples, tui.CostOutcome{
			Model: sample.Model, CostUSD: sample.CostUSD, CostKnown: sample.CostKnown,
			Passing: sample.Passing, Required: sample.Required,
		})
	}
	return out, nil
}

func (r *reviewInsights) recordOutcome(sample session.OutcomeSample) error {
	key := sample.SessionID + "|" + sample.Revision
	r.mu.Lock()
	if previous, ok := r.recorded[key]; ok && previous == sample {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()

	if err := r.engine.RecordOutcome(sample); err != nil {
		return err
	}
	r.mu.Lock()
	if r.recorded == nil {
		r.recorded = make(map[string]session.OutcomeSample)
	}
	r.recorded[key] = sample
	r.mu.Unlock()
	return nil
}

func sessionModelUsage(conversation core.Session) (string, core.Usage, modelUsageState) {
	var models = make(map[string]bool)
	unknownModel := false
	for _, turn := range conversation.Turns {
		if !turn.State.Terminal() {
			continue
		}
		model := turn.Model
		if model == "" {
			unknownModel = true
			continue
		}
		models[model] = true
	}
	if unknownModel || len(models) == 0 {
		return "", core.Usage{}, noModelUsage
	}
	if len(models) > 1 {
		return "", core.Usage{}, mixedModelUsage
	}
	for model := range models {
		return model, conversation.Usage(), singleModelUsage
	}
	panic("unreachable")
}

func (v *verification) Close() {
	if v == nil {
		return
	}
	v.stop()
	v.verifier.Runner().CancelAll()
	if v.runner != nil {
		// Waited on rather than abandoned. A hook is a process Canopy started, and leaving one
		// running after the program that started it has gone is the same failure the test runner is
		// held to.
		v.runner.Wait()
	}
}

// observeHooks tells the runner where every watched agent stands, and lets it decide what that
// newly satisfies.
//
// A snapshot per agent rather than a delta, because the poller produces snapshots and because
// working out what changed is the runner's job. It holds the state that makes "again" answerable,
// and splitting that decision across two packages is how a hook ends up firing twice.
func (v *verification) observeHooks(ctx context.Context, engine *session.Engine) {
	if v == nil || v.runner == nil {
		return
	}

	states := make(map[string]core.AgentState)
	for _, status := range engine.AgentStatuses() {
		states[status.Agent.Name] = status.State
	}

	for _, agent := range engine.Agents() {
		snapshot, ok := v.verifier.Snapshot(agent.Name)
		if !ok {
			continue
		}
		rollup := core.RollUp(snapshot)
		v.runner.Observe(ctx, hooks.Observation{
			Subject: agent.Name,
			// The derived state rather than the recorded one, so a result that has been overtaken
			// reads as stale here exactly as it does on screen. This is the whole of the rule that
			// nothing fires on evidence which no longer describes the code.
			Revision: snapshot.Revision,
			Tests:    rollup.Tests,
			Green:    rollup.Green,
			Agent:    states[agent.Name],
		})
	}
}

// recordHook is where a hook's outcome lands.
//
// Only the failures are kept. A hook that worked is what somebody configured it to do, and a list
// of those is a log nobody reads; a hook that did not is the thing they need to be told.
func (v *verification) recordHook(report hooks.Report) {
	if !report.Failed() {
		return
	}
	v.failuresMu.Lock()
	defer v.failuresMu.Unlock()
	v.failures = append(v.failures, report.Summary())
}

// HookFailures is everything that went wrong, for the caller to say on the way out.
func (v *verification) HookFailures() []string {
	if v == nil {
		return nil
	}
	v.failuresMu.Lock()
	defer v.failuresMu.Unlock()
	return append([]string(nil), v.failures...)
}

// startVerification brings up the poller and the verifier for a repository.
//
// Returns nil where there is nothing to verify, which the review screen renders as an explanation.
// A nil here is a normal outcome and not a swallowed error: the errors that matter are reported to
// stderr by the caller and the program carries on.
func startVerification(
	ctx context.Context, engine *session.Engine, dir string, project config.Project,
) (*verification, error) {
	repo, err := gitpkg.OpenRepo(dir)
	if err != nil {
		return nil, nil
	}

	base := project.Base
	if base == "" {
		base = defaultBranch(ctx, repo)
	}

	verifier := verify.New(repo, base, testsFor(project), nil)

	// The poller feeds the verifier and nothing else, so the two are wired directly rather than
	// through the event bus. Going through the bus would mean the verifier learning a revision
	// changed and then asking git what it changed to, which is two reads of the same fact.
	monitor := newWorktrees(dir, verifier)
	poller := gitpkg.NewPoller(repo, gitpkg.DefaultPollInterval, func(change gitpkg.Change) {
		verifier.Observe(context.Background(), change)
		// Told after the verifier has taken the change, never before. The dashboard reads the
		// verifier when it is woken, so waking it first would have it read the state from before
		// the change it was being told about.
		monitor.changed(change.WorkspaceID)
	})

	watchCtx, stop := context.WithCancel(ctx)
	v := &verification{verifier: verifier, poller: poller, stop: stop, store: monitor}

	// Hooks fire on what the verifier concludes, which is why they are built here rather than beside
	// the agent loop. Nothing was calling Observe at all before this: the package decided correctly
	// which events had happened and was never asked, so every hook anybody configured was a command
	// that validated at load and then never ran.
	if configured := project.Runnable(); len(configured) > 0 {
		v.runner = hooks.New(configured, dir, hooks.Shell, v.recordHook)
	}

	v.follow(engine)

	// Agents come and go while Canopy runs, so the watched set is refreshed rather than fixed at
	// startup. On its own interval rather than on an engine callback, because the engine has no hook
	// for "the agent list changed" and inventing one to serve a background poller would put a
	// verification concern inside the session package.
	go func() {
		ticker := time.NewTicker(gitpkg.DefaultPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
				v.follow(engine)
				// On the tick rather than on a revision changing, because half the vocabulary is
				// about the agent rather than about the code: an agent going idle or getting blocked
				// moves nothing in git, and a hook keyed to a poll of the worktree would never see
				// it. The runner decides what is new, which is the whole reason it holds that state
				// rather than the caller.
				v.observeHooks(watchCtx, engine)
			}
		}
	}()

	go poller.Run(watchCtx)
	return v, nil
}

// follow points the poller and the verifier at whatever agents currently exist.
func (v *verification) follow(engine *session.Engine) {
	agents := engine.Agents()

	subjects := make([]verify.Subject, 0, len(agents))
	workspaces := make([]core.WorkspaceSnapshot, 0, len(agents))
	for _, agent := range agents {
		if agent.Dir == "" {
			continue
		}
		id := agent.WorkspaceID
		if id == "" {
			// A non isolated agent works in the repository itself and has no workspace of its own, so
			// one is derived from its directory. Derived rather than skipped: the ordinary run has
			// exactly one agent and it is not isolated, and a verification screen that only worked
			// for isolated agents would be useless in the common case.
			id = gitpkg.WorkspaceID(agent.Dir)
		}
		subjects = append(subjects, verify.Subject{
			Agent: agent.Name, WorkspaceID: id, Dir: agent.Dir, Branch: agent.Branch,
		})
		workspaces = append(workspaces, core.WorkspaceSnapshot{ID: id, Name: agent.Name, Path: agent.Dir})
	}

	v.verifier.Watch(subjects)
	v.poller.Watch(workspaces)

	watched := make([]watchedAgent, 0, len(workspaces))
	for _, agent := range agents {
		if agent.Dir != "" {
			watched = append(watched, watchedAgent{name: agent.Name, path: agent.Dir})
		}
	}
	v.store.follow(watched)
}

// defaultBranch works out what an agent's work should be measured against.
//
// The remote's own idea of its default first, then the local branches Canopy is most likely to
// find. A wrong answer here is not catastrophic and it is visible: every diff comes out the wrong
// size, which somebody notices immediately, as opposed to a wrong test result which they might not.
func defaultBranch(ctx context.Context, repo *gitpkg.Repo) string {
	for _, candidate := range []string{"main", "master", "trunk", "develop"} {
		if repo.HasBranch(ctx, candidate) {
			return candidate
		}
	}
	return "HEAD"
}

// loadProject reads the committed configuration, reporting a broken one rather than ignoring it.
func loadProject(dir string) config.Project {
	project, found, err := config.Load(dir)
	if err != nil {
		// Loud, and then carry on with nothing configured. A config file that fails to load and is
		// silently replaced by defaults is how somebody ends up with a green project whose tests
		// never ran.
		fmt.Fprintf(os.Stderr, "warning: %v\nwarning: continuing with nothing configured\n", err)
		return config.Project{}
	}
	if !found {
		return config.Project{}
	}
	return project
}

// loadCommands resolves the user-level catalog with this project's definitions.
//
// A broken global file degrades only the global layer. Project commands still work and the warning
// names the exact file, which is more useful than making every repository refuse to start because
// one optional convenience file has a typo.
func loadCommands(project []config.Command) config.CommandSet {
	global, _, err := config.LoadGlobalCommands()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: global slash commands are not available: %v\n", err)
	}
	return config.ResolveCommands(global, project)
}

// profiles lists the credentials an agent can be started on.
//
// Built here rather than in the engine because the engine has never known what a credential is: it
// is handed a resolver and asks it for a client. Keeping that boundary means the dispatch tool can
// be tested with a fake that has no keychain in it.
type profiles struct {
	store  *keys.Store
	engine *session.Engine
}

func (p profiles) Profiles() []session.Profile {
	stored, err := p.store.List()
	if err != nil {
		return nil
	}

	out := make([]session.Profile, 0, len(stored))
	for _, meta := range stored {
		id := pricing.ModelID{
			Provider: meta.Ref.Provider,
			Model:    defaultModelFor(p.store, meta.Ref.Name),
			Host:     hostOf(meta.BaseURL),
		}
		_, priced := pricing.Apply(id, core.Usage{OutputTokens: 1})

		out = append(out, session.Profile{
			Name:  meta.Ref.Name,
			Model: defaultModelFor(p.store, meta.Ref.Name),
			// Apply returns a reason when it could not price the request, so an empty reason is the
			// only thing that means a rate is actually known. Asking the table twice, once for the
			// rate and once for the reason, would be two places to disagree.
			Priced: priced == "",
		})
	}
	return out
}

func (p profiles) Estimate(task string, count int) session.Estimate {
	return p.engine.Estimate(task, count)
}

func (p profiles) Concurrency() (int, int) { return p.engine.Concurrency() }

func (p profiles) Spawn(ctx context.Context, request session.Dispatch) ([]session.Agent, error) {
	return p.engine.Spawn(ctx, request)
}

// hostOf is the host part of a base URL, which is what the pricing table keys an endpoint on.
func hostOf(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	return parsed.Host
}

// attachDispatch lets one agent start others from the conversation.
//
// Registered on the orchestrating agent only, and after its session exists, because the
// confirmation has to be asked of the person watching that session. Spawned agents deliberately do
// not get these tools: an agent that can spawn agents that can spawn agents is A8-01, which has its
// own design and its own limits, and inheriting it by accident here would mean a fan out could
// multiply without anybody having agreed to it.
func attachDispatch(engine *session.Engine, store *keys.Store, registry *core.ToolRegistry, sessionID string) error {
	source := profiles{store: store, engine: engine}

	confirm := func(c session.Confirmation) bool {
		// Routed through the same approver the tool calls use, so there is one place a person answers
		// questions rather than two that behave differently. The question text carries the count, the
		// profile, the task, the estimate and the warnings, because every one of those is a thing
		// that could be wrong and this is the last moment to catch it.
		question := c.Question() + "\n" + c.Estimate.Summary()
		for _, warning := range c.Warnings {
			question += "\n" + warning
		}
		return engine.Approve(context.Background(), permission.Request{
			SessionID: sessionID,
			AgentID:   sessionID,
			Tool:      "spawn_agents",
			Kind:      core.ToolExecute,
			Command:   question,
		}, permission.Decision{
			Outcome: permission.Ask,
			Reason:  "starting agents spends money and creates worktrees",
		})
	}

	for _, tool := range session.DispatchTools(source, confirm) {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	return nil
}
