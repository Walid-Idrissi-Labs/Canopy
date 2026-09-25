package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tools"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	execpkg "github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
	gitpkg "github.com/Walid-Idrissi-Labs/Canopy/internal/git"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/gitsafe"
)

// runLand brings an agent's branch into the branch you are on, only if the result passes.
//
// The merge happens in a scratch worktree, never in your checkout. The project's tests run on the
// merged result, which is what will exist afterwards, not on the agent's branch alone: two branches
// that each pass can fail together. Your branch moves only if every required test passed and it
// still points where it did when the merge was made, so work that arrived in the meantime is never
// overwritten. The agent's branch is kept either way.
func runLand(args []string, stdin io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet("land", flag.ContinueOnError)
	flags.SetOutput(out)
	pr := flags.Bool("pr", false, "push the agent's branch and open a pull request instead of merging locally")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: canopy land [-pr] <agent-branch>")
	}
	branch := flags.Arg(0)
	ctx := context.Background()

	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	git := func(in string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = in
		cmd.Env = gitsafe.InheritedFor(in)
		b, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(b)), err
	}
	top, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("this directory is not in a git repository")
	}
	if _, err := git(top, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		return fmt.Errorf("there is no branch called %q", branch)
	}

	if *pr {
		if out, err := git(top, "push", "-u", "origin", branch); err != nil {
			return fmt.Errorf("pushing %s: %s", branch, out)
		}
		cmd := exec.CommandContext(ctx, "gh", "pr", "create", "--head", branch, "--fill")
		cmd.Dir = top
		cmd.Stdout, cmd.Stderr = out, out
		return cmd.Run()
	}

	dest, err := git(top, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return errors.New("your checkout is not on a branch, so there is nowhere to land")
	}
	if status, _ := git(top, "status", "--porcelain"); status != "" {
		return errors.New("your checkout has uncommitted changes; commit or put them aside first, " +
			"so landing cannot mix them into the result")
	}
	before, err := git(top, "rev-parse", "HEAD")
	if err != nil {
		return err
	}

	home, err := gitpkg.WorktreeHome(top)
	if err != nil {
		return err
	}
	scratch := filepath.Join(home, fmt.Sprintf("land-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	if msg, err := git(top, "worktree", "add", "--detach", scratch, before); err != nil {
		return fmt.Errorf("making a scratch worktree: %s", msg)
	}
	defer func() { _, _ = git(top, "worktree", "remove", "--force", scratch) }()

	_, _ = fmt.Fprintf(out, "merging %s into %s in a scratch worktree\n", branch, dest)
	if msg, err := git(scratch, "merge", "--no-ff", "--no-edit", branch); err != nil {
		conflicts, _ := git(scratch, "diff", "--name-only", "--diff-filter=U")
		_, _ = git(scratch, "merge", "--abort")
		if conflicts != "" {
			return fmt.Errorf("%s does not merge cleanly into %s; conflicting files:\n  %s",
				branch, dest, strings.ReplaceAll(conflicts, "\n", "\n  "))
		}
		return fmt.Errorf("merging: %s", msg)
	}
	merged, err := git(scratch, "rev-parse", "HEAD")
	if err != nil {
		return err
	}

	project := loadProjectFrom(top, stdin, out)
	tests := testsFor(project)
	if len(tests) == 0 {
		return errors.New("no tests are configured (or the repository is not trusted), so the merged " +
			"result cannot be checked; nothing was changed")
	}
	// Prepared the way an agent's worktree is, the allow-listed files copied in and the setup run,
	// or tests that need dependencies fail on the merge for want of them rather than on its merits.
	if project.Setup != "" || len(project.Copy) > 0 {
		repo, err := gitpkg.OpenRepo(top)
		if err != nil {
			return err
		}
		prepared, err := repo.Prepare(ctx, core.WorkspaceSnapshot{Path: scratch, Ownership: core.OwnershipManaged},
			gitpkg.Environment{Setup: project.Setup, SetupTimeout: project.SetupDuration(), Copy: project.Copy},
			// Only files git ignores, from the person's own checkout into a worktree about to be
			// deleted; overwriting a committed file is never confirmed here.
			gitpkg.Confirm{Ignored: func(gitpkg.CopyRequest) bool { return true }})
		if err != nil {
			return fmt.Errorf("preparing the scratch worktree: %w", err)
		}
		if !prepared.OK() {
			return fmt.Errorf("the project's setup failed in the scratch worktree, so nothing was changed: %s",
				prepared.Summary())
		}
	}
	failed := 0
	for i, test := range tests {
		outcome := execpkg.RunTest(ctx, test, execpkg.Target{Dir: scratch, Sandbox: tools.Confinement(scratch)}, fmt.Sprintf("land-%d", i))
		mark := "passed"
		if outcome.Run.State != core.TestPassing {
			mark = string(outcome.Run.State)
			if test.Required {
				failed++
			}
		}
		_, _ = fmt.Fprintf(out, "  %-12s %s\n", test.Name, mark)
		if outcome.Run.State != core.TestPassing && test.Required {
			tail := outcome.Output
			if len(tail) > 2000 {
				tail = "..." + tail[len(tail)-2000:]
			}
			_, _ = fmt.Fprintf(out, "%s\n", tail)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d required tests did not pass on the merged result; %s was not changed", failed, dest)
	}

	// Moved while the tests ran: somebody committed, or the checkout changed. Landing now would
	// replace what they did with a result that was never tested against it.
	now, _ := git(top, "rev-parse", "HEAD")
	status, _ := git(top, "status", "--porcelain")
	if now != before || status != "" {
		return fmt.Errorf("%s changed while the tests ran, so the tested result no longer applies; "+
			"nothing was changed, run land again", dest)
	}
	if msg, err := git(top, "merge", "--ff-only", merged); err != nil {
		return fmt.Errorf("moving %s: %s", dest, msg)
	}
	_, _ = fmt.Fprintf(out, "landed %s on %s at %s; the branch %s is kept\n", branch, dest, merged[:12], branch)
	return nil
}

// loadProjectFrom reads and gates the configuration of the repository at dir.
func loadProjectFrom(dir string, stdin io.Reader, out io.Writer) config.Project {
	project, _, err := config.Load(dir)
	if err != nil {
		_, _ = fmt.Fprintf(out, "warning: %v\n", err)
		return config.Project{}
	}
	f, ok := stdin.(*os.File)
	return gateProject(dir, project, stdin, out, ok && isTerminal(f))
}
