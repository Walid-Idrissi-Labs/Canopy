package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	execpkg "github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
	gitpkg "github.com/Walid-Idrissi-Labs/Canopy/internal/git"
	reportpkg "github.com/Walid-Idrissi-Labs/Canopy/internal/report"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/verify"
)

// The command that turns a run into something somebody else can read.
//
// `internal/report` has rendered a run honestly since A8-08 and nothing ever called it, which meant
// the deliverable, "one command producing a markdown summary", did not exist: the honesty rules were
// tested and unreachable. This is the missing half.
//
// The checks are run here rather than read from wherever they were last left, and that is the whole
// design. A report is pasted into a pull request and read by somebody who cannot see the screen it
// came from, usually quickly and usually trusting it, so evidence gathered at some earlier point and
// no longer describing the code is the exact failure the rendering already refuses to commit. Asking
// git what changed and asking the runner to check it are one command because they have to describe
// the same revision.

// reportAgent is the name the workspace is watched under.
//
// The same name the ordinary run gives its first agent, so a report taken from a directory describes
// the thing somebody was actually working in rather than inventing a second identity for it.
const reportAgent = "main"

// runReport writes a markdown summary of the current workspace.
func runReport(ctx context.Context, out io.Writer) error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("finding the working directory: %w", err)
	}

	repo, err := gitpkg.OpenRepo(dir)
	if err != nil {
		// Refused rather than rendered empty. A report of a directory with no history would say no
		// files changed, which is true and reads as "this run did nothing".
		return fmt.Errorf(
			"a report describes changes to a repository, and %s is not one", dir)
	}

	project := loadProject(dir)
	base := project.Base
	if base == "" {
		base = defaultBranch(ctx, repo)
	}

	tests := testsFor(project)
	verifier := verify.New(repo, base, tests, nil)
	snapshot, err := repo.Describe(ctx, core.WorkspaceSnapshot{
		ID: gitpkg.WorkspaceID(dir), Name: reportAgent, Path: dir,
	})
	if err != nil {
		return fmt.Errorf("reading the workspace: %w", err)
	}
	verifier.Watch([]verify.Subject{{
		Agent: reportAgent, WorkspaceID: snapshot.ID, Dir: dir, Branch: snapshot.Branch,
	}})

	// One scan before anything is run, because the verifier learns what revision it is looking at
	// from the poller and from nowhere else. A running Canopy has a poller going round every couple
	// of seconds; a command that exits does not, and without this the report comes out saying the
	// revision could not be determined, the tests unknown, and nothing changed. All three would be
	// wrong, and the third is the one somebody would believe.
	poller := gitpkg.NewPoller(repo, gitpkg.DefaultPollInterval, func(change gitpkg.Change) {
		verifier.Observe(ctx, change)
	})
	poller.Watch([]core.WorkspaceSnapshot{{ID: snapshot.ID, Name: reportAgent, Path: dir}})
	poller.Poll(ctx)

	// Whatever happens, nothing this command started outlives it. A suite that hangs, a settle that
	// times out and an error on the way past all leave test processes running otherwise, and a
	// command that exits while its own `go test` keeps going is the orphan problem the runner exists
	// to prevent, reintroduced one level up.
	defer verifier.Runner().CancelAll()

	run := reportpkg.Run{Agent: reportAgent, Branch: snapshot.Branch, Base: base}
	if err := gather(ctx, verifier, poller, len(tests) > 0, &run); err != nil {
		return err
	}
	addSpend(dir, &run)

	_, err = io.WriteString(out, reportpkg.Markdown(run))
	return err
}

func testsFor(project config.Project) []execpkg.Test {
	tests := make([]execpkg.Test, 0, len(project.Tests))
	for _, test := range project.Tests {
		tests = append(tests, execpkg.Test{
			Name:     test.Name,
			Command:  test.Command,
			Required: test.Required,
			Timeout:  test.TestTimeout(),
		})
	}
	return tests
}

// gather runs the checks and reads what they found, along with what changed.
//
// A repository with no test configured is not an error here. It produces the zero roll-up, which the
// rendering already reports as "nothing is configured to check this project", and that sentence is
// the one honest answer: it is neither a pass nor a failure, and it is the state most easily
// mistaken for a clean run.
func gather(
	ctx context.Context, verifier *verify.Verifier, poller *gitpkg.Poller,
	checked bool, run *reportpkg.Run,
) error {
	if checked {
		if err := verifier.Verify(ctx, run.Agent); err != nil {
			// Said out loud rather than left to surface as an absence of evidence, which would read
			// exactly like a project with nothing configured. A runner that could not start is a
			// different fact from a project that declares no tests.
			return fmt.Errorf("running the checks: %w", err)
		}
		if err := settle(ctx, verifier, run.Agent); err != nil {
			return err
		}
	}

	// Looked at again after the suite finishes, so every part of the report describes the same
	// worktree. Without this the whole thing is built from the scan taken before the tests ran, and a
	// file edited while they were running produces a report that is confidently wrong in both
	// directions at once: the results are still attributed to the revision they were started
	// against, so nothing looks stale and the verdict reads **Verified**, while the cached diff still
	// says nothing changed. A reader is told the work is finished and verified and that there is no
	// work.
	//
	// Nothing is papered over here. If the revision moved, the runs no longer describe the worktree
	// and the roll-up says so on its own, which is the honest answer and the one the acceptance
	// criterion asks for.
	poller.Poll(ctx)

	if rollup, ok := verifier.Rollup(run.Agent); ok {
		run.Rollup = rollup
	}

	run.Diff = verifier.Diff(run.Agent)
	changes, err := verifier.Changes(run.Agent)
	if err != nil {
		return fmt.Errorf("reading what changed: %w", err)
	}
	run.Files = reportpkg.Sorted(changes)
	return nil
}

// settle waits for the checks to stop moving.
//
// Reading the roll-up while a suite is still running reports whatever happened to fail first as the
// verdict, which is the same reason the green gate waits. Bounded by the same generous timeout, and
// a timeout is reported rather than rendered: a report of a run whose tests never finished would
// claim a verification state the evidence does not support, which is the one thing this must not do.
func settle(ctx context.Context, verifier *verify.Verifier, agent string) error {
	ctx, cancel := context.WithTimeout(ctx, gateTimeout)
	defer cancel()

	for {
		snapshot, ok := verifier.Snapshot(agent)
		if !ok {
			return fmt.Errorf("the workspace %s is not being watched", agent)
		}
		if settled(snapshot) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the checks did not finish within %s", gateTimeout)
		case <-time.After(gatePoll):
		}
	}
}

// addSpend fills in what the run cost, from the most recent conversation in this project.
//
// Best effort, and silent when there is nothing to find. History is optional everywhere else in
// Canopy and a report of a repository somebody has never opened Canopy on is still worth having: it
// comes out with no cost figure, which the rendering reports as "not known" rather than as free.
func addSpend(dir string, run *reportpkg.Run) {
	engine := session.New(nil)
	defer engine.Close()

	if err := attachHistory(engine); err != nil {
		return
	}

	latest, ok := latestSession(engine, gitpkg.WorkspaceID(dir))
	if !ok {
		return
	}
	run.Usage = latest.Usage()
	run.Turns = len(latest.Turns)
}

// latestSession is the most recently updated conversation belonging to a project.
//
// Scoped to the project for the same reason picking one up is: the history database is shared across
// every directory Canopy is opened in, and the most recent conversation on the machine is very
// often one about something else entirely. Reporting its cost against this repository's diff would
// be a number that looks like an answer and is not.
func latestSession(engine *session.Engine, projectID string) (core.Session, bool) {
	var newest core.Session
	found := false
	for _, candidate := range engine.Sessions() {
		if projectID != "" && engine.ProjectOf(candidate.ID) != projectID {
			continue
		}
		if !found || candidate.UpdatedAt.After(newest.UpdatedAt) {
			newest, found = candidate, true
		}
	}
	return newest, found
}
