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

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	execpkg "github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
	gitpkg "github.com/Walid-Idrissi-Labs/Canopy/internal/git"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/verify"
)

// verification is everything the review screen needs, kept together so it can be shut down as one.
type verification struct {
	verifier *verify.Verifier
	poller   *gitpkg.Poller
	stop     context.CancelFunc
}

func (v *verification) Close() {
	if v == nil {
		return
	}
	v.stop()
	v.verifier.Runner().CancelAll()
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

	tests := make([]execpkg.Test, 0, len(project.Tests))
	for _, test := range project.Tests {
		tests = append(tests, execpkg.Test{
			Name:     test.Name,
			Command:  test.Command,
			Required: test.Required,
			Timeout:  test.TestTimeout(),
		})
	}

	verifier := verify.New(repo, base, tests, nil)

	// The poller feeds the verifier and nothing else, so the two are wired directly rather than
	// through the event bus. Going through the bus would mean the verifier learning a revision
	// changed and then asking git what it changed to, which is two reads of the same fact.
	poller := gitpkg.NewPoller(repo, gitpkg.DefaultPollInterval, func(change gitpkg.Change) {
		verifier.Observe(context.Background(), change)
	})

	watchCtx, stop := context.WithCancel(ctx)
	v := &verification{verifier: verifier, poller: poller, stop: stop}
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
