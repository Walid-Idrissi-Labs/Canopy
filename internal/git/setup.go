package git

// Bringing a fresh worktree to a state where the project actually runs.
//
// A new worktree holds the committed files and nothing else. No `.env`, no `node_modules`, no
// virtualenv, no build cache. An agent spawned into one lands in a tree where the test suite fails
// for reasons that have nothing to do with its work, and A6 then ranks it on that. A false red is
// as damaging as a false green and more expensive, because somebody goes and chases it.
//
// Two mechanisms, and they are deliberately not equals:
//
//  1. **A setup command.** `npm ci`, `go mod download`, `uv sync`. This is the one that should do
//     the work. It is reproducible, it is already written down in the project's own README, and it
//     copies nothing out of anybody's checkout.
//  2. **A short allow list of files to copy.** For what cannot be rebuilt because it is secret. A
//     `.env` is the entire reason this exists.
//
// Everything about the second is arranged to keep it small and visible. Every copy is confirmed,
// paths git does not ignore are confirmed by a separate question, and nothing here ever reads a
// copied file into a field that is displayed, logged or returned.
//
// What this does not do, because it is the thing people assume it does: a worktree is not an
// environment. It does not isolate a database, a Redis, a queue, a port or an OAuth callback. Two
// agents running the same suite against the same development database still interfere with each
// other, and no amount of file copying changes that. Canopy templates values and says so; it does
// not promise isolation it cannot deliver.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
)

// LargeCopy is the size above which a copy is worth mentioning out loud before it is confirmed.
//
// Not a limit. Somebody who wants to copy two gigabytes may. But answering yes to `node_modules`
// without being told it is four hundred megabytes is not really an answer, and a laptop filling up
// because of a question that was asked badly is a bug in the question.
const LargeCopy = 10 << 20

// DefaultSetupTimeout bounds a setup command that was given no timeout of its own.
//
// Ten minutes, because a cold `npm ci` or a module download on a hotel connection genuinely takes
// minutes, and killing a legitimate install is a worse failure than waiting for one.
const DefaultSetupTimeout = 10 * time.Minute

// Environment describes how to make a fresh worktree runnable.
//
// The zero value is valid and does nothing, which is the right default. Most repositories need no
// preparation at all, and one that needs none should not be waiting on a command to say so.
type Environment struct {
	// Setup runs in the new worktree, through a shell, once the copies are in place.
	Setup string

	// SetupTimeout bounds it. Zero means DefaultSetupTimeout.
	SetupTimeout time.Duration

	// Copy lists paths, relative to the repository root, that may be copied from the primary
	// checkout.
	//
	// An allow list of literal paths rather than patterns. `.env` is a decision somebody made once
	// and can read back at a glance; `*.env` or `config/**` is a decision whose real scope they
	// find out about later, which is the wrong way round for the one feature here that moves
	// secrets.
	Copy []string
}

// CopyRequest is one allow list entry, measured, ready to be asked about.
type CopyRequest struct {
	// Path is relative to the repository root, cleaned.
	Path string

	// Ignored says git ignores this path. False means git tracks it, or would.
	Ignored bool

	// Dir, Bytes and Files describe what would actually move, so the question can be asked with the
	// size in it rather than after it.
	Dir   bool
	Bytes int64
	Files int
}

// Large reports whether the size belongs in the question.
func (r CopyRequest) Large() bool { return r.Bytes >= LargeCopy }

// Confirm is how a person is asked before anything leaves their checkout.
//
// Two callbacks rather than one with a flag, so the distinction is structural rather than a
// convention a caller can forget. They are genuinely different questions:
//
//   - **Ignored** is the ordinary case and what the allow list is for. The file is not in any
//     commit, so the worktree does not have it and cannot get it any other way.
//   - **Tracked** is not. The file is already in the worktree at its committed version, so copying
//     over it replaces committed content with whatever happens to be sitting in the user's tree.
//     The agent then works against a state that exists in no commit anywhere, and every diff it
//     produces is measured from the wrong baseline.
//
// A nil callback means no. A caller with nowhere to ask copies nothing rather than defaulting to
// yes, and in particular a caller that only wired up the ignored question cannot accidentally
// answer the tracked one with it.
type Confirm struct {
	Ignored func(CopyRequest) bool
	Tracked func(CopyRequest) bool
}

// Skip is a path that was not copied, and why, in words somebody can act on.
//
// Skips are reported rather than returned as errors because most of them are normal. An allow list
// is written once and applies to every worktree forever, so it naturally names files that are not
// there yet, and a missing `.env` should not fail the preparation of a worktree that does not need
// one.
type Skip struct {
	Path   string
	Reason string
}

// Prepared is what happened, in enough detail to put on a screen.
type Prepared struct {
	// Copied names what was copied. Paths only. The contents of a copied file are the one thing in
	// this package that must never reach a field somebody might print.
	Copied []string

	// Skipped is what was not, and why.
	Skipped []Skip

	// Ran says a setup command was configured and started. It distinguishes "there is no setup
	// command" from "the setup command did nothing", which are identical in an exit code and mean
	// very different things when a worktree turns out to be broken.
	Ran      bool
	ExitCode int
	Output   string
	Duration time.Duration
	TimedOut bool
}

// OK reports whether the worktree is ready to be worked in.
func (p Prepared) OK() bool { return !p.Ran || (p.ExitCode == 0 && !p.TimedOut) }

// Summary is one line for a list, and says the failure out loud when there is one.
//
// A failed setup has to be visible. The worktree exists either way and looks perfectly normal from
// the outside, so an agent dispatched into a tree where the install failed will start working and
// produce failures nobody can attribute. This is the sentence that stops that.
func (p Prepared) Summary() string {
	switch {
	case p.TimedOut:
		return fmt.Sprintf("setup timed out after %s, so this worktree is not ready",
			p.Duration.Round(time.Second))
	case p.Ran && p.ExitCode != 0:
		return fmt.Sprintf("setup exited %d, so this worktree is not ready", p.ExitCode)
	case p.Ran:
		return fmt.Sprintf("ready, setup took %s", p.Duration.Round(time.Second))
	case len(p.Copied) > 0:
		return fmt.Sprintf("ready, copied %d %s", len(p.Copied), plural(len(p.Copied), "file", "files"))
	default:
		return "ready"
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Prepare brings a worktree to a runnable state: the copies first, then the setup command.
//
// That order is load bearing. A setup command frequently reads the very files the allow list exists
// to provide, and a build that runs before `.env` arrives fails in a way that looks like a broken
// project rather than a race in the preparation.
//
// A failing setup command is reported in the result rather than returned as an error. The worktree
// still exists and still has the code in it, somebody may well want to go in and fix it by hand,
// and removing their worktree because a registry was briefly unreachable would be an overreaction.
// The error return is for the cases where preparation could not be attempted at all.
func (r *Repo) Prepare(
	ctx context.Context, workspace core.WorkspaceSnapshot, env Environment, confirm Confirm,
) (Prepared, error) {
	switch {
	case workspace.Path == "":
		return Prepared{}, errors.New("that worktree has no path")
	case workspace.Ownership == core.OwnershipPrimary:
		// Preparing the primary would copy files onto themselves and run an install command inside
		// somebody's own checkout, neither of which was asked for.
		return Prepared{}, errors.New(
			"the primary checkout is yours and is already set up, so it is never prepared")
	}

	root, err := r.run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return Prepared{}, fmt.Errorf("finding the repository root: %w", err)
	}
	// Resolved for the same reason Create resolves its result: on macOS a temporary directory is a
	// symlink, so the root git reports and the path a copy is written to would otherwise disagree.
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	}

	var prepared Prepared
	for _, path := range env.Copy {
		r.copyOne(ctx, root, workspace.Path, path, confirm, &prepared)
	}

	if strings.TrimSpace(env.Setup) == "" {
		return prepared, nil
	}

	timeout := env.SetupTimeout
	if timeout <= 0 {
		timeout = DefaultSetupTimeout
	}

	prepared.Ran = true
	// Through a shell, because a setup command is written the way somebody would type it and
	// frequently contains a pipe or a conditional.
	result, err := exec.Run(ctx, "/bin/sh", []string{"-c", env.Setup}, exec.Options{
		Dir:     workspace.Path,
		Env:     setupEnv(),
		Timeout: timeout,
	})
	if err != nil {
		return prepared, fmt.Errorf("running the setup command: %w", err)
	}
	if !result.Ran {
		return prepared, fmt.Errorf("the setup command could not be started: %s",
			strings.TrimSpace(result.Output))
	}

	prepared.ExitCode = result.ExitCode
	prepared.Output = result.Output
	prepared.Duration = result.Duration
	prepared.TimedOut = result.TimedOut
	return prepared, nil
}

// copyOne handles a single allow list entry, recording what it did rather than failing.
func (r *Repo) copyOne(
	ctx context.Context, root, worktree, path string, confirm Confirm, into *Prepared,
) {
	skip := func(reason string) {
		into.Skipped = append(into.Skipped, Skip{Path: strings.TrimSpace(path), Reason: reason})
	}

	clean, err := insideRepo(path)
	if err != nil {
		skip(err.Error())
		return
	}

	source := filepath.Join(root, clean)
	// Lstat rather than Stat, so a symlink is seen as one instead of as whatever it points at.
	info, err := os.Lstat(source)
	switch {
	case os.IsNotExist(err):
		skip("there is nothing at that path in the checkout")
		return
	case err != nil:
		skip("could not read it: " + err.Error())
		return
	case info.Mode()&os.ModeSymlink != 0:
		// Refused rather than followed. Materialising a symlink inside an isolated worktree is a
		// route back out of it that nobody chose, and the whole point of the isolated mode is that
		// the route does not exist.
		skip("it is a symlink, and copying one into an isolated worktree would point back out of it")
		return
	case !info.Mode().IsRegular() && !info.IsDir():
		skip("it is not a regular file or a directory")
		return
	}

	ignored, err := r.ignores(ctx, root, clean)
	if err != nil {
		// Unable to tell is treated as tracked, which is the stricter of the two questions.
		skip("could not tell whether git ignores it: " + err.Error())
		return
	}

	request := CopyRequest{Path: clean, Ignored: ignored, Dir: info.IsDir()}
	request.Bytes, request.Files = measure(source)

	ask, refusal := confirm.Ignored, "nothing confirmed the copy"
	if !ignored {
		ask = confirm.Tracked
		refusal = "git tracks this path, and nothing confirmed copying over the committed version"
	}
	if ask == nil {
		skip(refusal)
		return
	}
	if !ask(request) {
		skip("declined")
		return
	}

	if err := copyTree(source, filepath.Join(worktree, clean)); err != nil {
		skip("copying it failed: " + err.Error())
		return
	}
	into.Copied = append(into.Copied, clean)
}

// insideRepo refuses an allow list entry that does not name something inside the repository.
//
// The allow list is configuration, so it is written by a person rather than chosen by a model, but
// it is still the input that decides which files get copied out of a checkout. A `../` in it would
// reach into a sibling project, and an absolute path would reach anywhere at all.
func insideRepo(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	switch {
	case trimmed == "":
		return "", errors.New("it is an empty path")
	case filepath.IsAbs(trimmed):
		return "", errors.New("it is an absolute path, and the allow list is relative to the repository")
	}

	clean := filepath.Clean(trimmed)
	switch {
	case clean == "..", strings.HasPrefix(clean, ".."+string(filepath.Separator)):
		return "", errors.New("it points outside the repository")
	case clean == ".":
		return "", errors.New("it names the whole repository, which is not a file to copy")
	}
	return clean, nil
}

// ignores asks git whether a path is ignored, without treating "no" as a failure.
//
// `git check-ignore` reports its answer in the exit code: zero for ignored, one for not. The repo's
// own run helper turns every non zero exit into an error, which would make every tracked file look
// like a broken git invocation, so this one goes to exec directly.
func (r *Repo) ignores(ctx context.Context, root, path string) (bool, error) {
	result, err := exec.Run(ctx, "git", []string{"check-ignore", "-q", "--", path}, exec.Options{
		Dir:     root,
		Env:     environ(),
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return false, err
	}
	if !result.Ran {
		return false, errors.New("git could not be run")
	}

	switch result.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("git check-ignore exited %d: %s",
			result.ExitCode, strings.TrimSpace(result.Output))
	}
}

// measure totals what a copy would move, so the confirmation can say how much.
//
// Walk errors are ignored on purpose. This produces a number for a question, not a guarantee, and
// an unreadable file deep in a tree should not stop somebody being told the tree is large.
func measure(path string) (bytes int64, files int) {
	_ = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if info, statErr := entry.Info(); statErr == nil && info.Mode().IsRegular() {
			bytes += info.Size()
			files++
		}
		return nil
	})
	return bytes, files
}

// copyTree copies a file or a directory into the worktree.
func copyTree(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(source, target, info.Mode())
	}

	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}

		dest := filepath.Join(target, rel)
		switch {
		case entry.IsDir():
			// The owner write bit is forced on, or a read only directory in the source produces a
			// directory nothing can be written into and the walk fails on its own output.
			return os.MkdirAll(dest, info.Mode().Perm()|0o700)
		case info.Mode()&os.ModeSymlink != 0, !info.Mode().IsRegular():
			// Same reasoning as the top level refusal, applied to everything inside a directory.
			return nil
		default:
			return copyFile(path, dest, info.Mode())
		}
	})
}

// copyFile copies one file and preserves its mode.
//
// The mode matters more than it looks. A `.env` is routinely 0600, and copying it out at 0644 hands
// an isolated agent's credentials to every account on the machine. Chmod is explicit rather than
// left to the open flags, because those only apply when the file is created and the tracked file
// case writes over something that already exists.
func copyFile(source, target string, mode fs.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(target, mode.Perm())
}

// setupEnv is the user's environment, minus the git variables that would redirect the command.
//
// The full environment rather than the sanitised one the git calls use. A setup command is the
// project's own build tooling: it needs PATH, HOME, proxy settings, a version manager's shims and
// whatever else somebody has arranged in their shell, and stripping that would break exactly the
// installs this exists to run.
//
// What is removed is the handful of GIT_ variables that would point the command at a repository
// other than the one it is running in. Canopy sets some of those for its own git calls, and a setup
// command that inherited them would quietly operate on the primary checkout while appearing to run
// in the worktree, which is the failure here that would be hardest to explain afterwards.
func setupEnv() []string {
	var out []string
	for _, entry := range os.Environ() {
		switch name, _, _ := strings.Cut(entry, "="); name {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY":
			continue
		default:
			out = append(out, entry)
		}
	}
	return out
}
