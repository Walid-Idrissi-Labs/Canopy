package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/acpserver"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/agent"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	gitpkg "github.com/Walid-Idrissi-Labs/Canopy/internal/git"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
)

// runACP serves the project it is started in to an editor over the Agent Client Protocol: the
// editor writes requests to stdin and reads Canopy's answers and progress from stdout, so nothing
// else may write there. Whatever needs a person is asked in the editor.
func runACP(args []string, stdin io.Reader, out, errOut io.Writer) int {
	host, code := openHost("acp", args, errOut)
	if host == nil {
		return code
	}
	defer host.close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := host.hub.Serve(ctx, stdin, out); err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return exitFailed
	}
	return exitOK
}

// host is an engine set up for this project, served through an ACP hub.
type host struct {
	hub   *acpserver.Hub
	dir   string
	close func()
}

// openHost sets up the engine the way canopy run does, with the hub as the one that asks a person,
// or says why it could not and with what exit code.
func openHost(name string, args []string, errOut io.Writer) (*host, int) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(errOut)
	keyName := flags.String("key", "", "the named key to use; the default key when omitted")
	model := flags.String("model", "", "the model; the key's default when omitted")
	if err := flags.Parse(args); err != nil {
		return nil, exitUsage
	}

	keyStore, err := openKeyStore()
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return nil, exitUsage
	}
	dir, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return nil, exitUsage
	}
	resolver := session.NewKeyResolver(keyStore, version)
	resolver.Renews(signInSources())
	engine := session.New(resolver)
	var closers []func()
	closeAll := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
	closers = append(closers, engine.Close)
	if err := attachHistory(engine); err != nil {
		_, _ = fmt.Fprintf(errOut, "warning: history is not being saved: %v\n", err)
	}
	projectID := gitpkg.WorkspaceID(dir)
	engine.SetProjectID(projectID)
	// The client owns stdin, or there is no terminal at all, so there is no one to answer a trust
	// prompt: an untrusted repository's configuration is withheld, as for canopy run.
	project := gateProject(dir, loadProjectRaw(dir, errOut), strings.NewReader(""), errOut, false)
	engine.WithInstructions(projectInstructions(dir, project, errOut))
	enableLanguageServers(project)
	closers = append(closers, closeLanguageServers)
	engine.SetWebSearch(webSearchWanted())

	if *keyName == "" {
		*keyName = resolver.DefaultKeyName()
	}
	if *model == "" {
		*model = defaultModelFor(keyStore, *keyName)
	}
	adapter := &acpEngine{engine: engine, dir: dir, projectID: projectID, key: *keyName, model: *model,
		trust: projectTrust(project)}
	hub := acpserver.NewHub(adapter)

	registry, err := toolsFor(dir)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "warning: tools are not available: %v\n", err)
	} else {
		engine.WithTools(registry, projectTrust(project), agent.ApproverFunc(hub.Approve))
	}
	closers = append(closers, attachMCP(engine, dir, project))
	return &host{hub: hub, dir: dir, close: closeAll}, exitOK
}

// acpEngine is the engine as the ACP server sees it: each editor session is an agent of its own,
// working in the project canopy acp was started in.
type acpEngine struct {
	engine    *session.Engine
	dir       string
	projectID string
	key       string
	model     string
	trust     core.TrustLevel

	mu    sync.Mutex
	count int
}

func (a *acpEngine) NewSession(ctx context.Context, cwd string) (string, error) {
	// The tools are rooted in one workspace, so a session elsewhere would edit a directory the
	// editor did not ask about.
	if cwd != "" && !sameDir(cwd, a.dir) {
		return "", fmt.Errorf("canopy acp serves %s; start another one in %s for that project", a.dir, cwd)
	}
	added, err := a.engine.AddAgent(ctx, session.Agent{Name: a.nextName(), KeyName: a.key, Model: a.model,
		Dir: a.dir, Trust: a.trust})
	if err != nil {
		return "", err
	}
	mode, _ := core.ModeByName(core.ModeBuild)
	if err := a.engine.SetMode(added.SessionID, mode); err != nil {
		return "", err
	}
	return added.SessionID, nil
}

func (a *acpEngine) nextName() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.count++
	return fmt.Sprintf("client-%d", a.count)
}

// LoadSession resumes a saved conversation as an agent of its own, unless one is working on it
// already, and only if it belongs to this project.
func (a *acpEngine) LoadSession(ctx context.Context, sessionID, cwd string) error {
	if cwd != "" && !sameDir(cwd, a.dir) {
		return fmt.Errorf("canopy serves %s here, not %s", a.dir, cwd)
	}
	for _, running := range a.engine.Agents() {
		if running.SessionID == sessionID {
			return nil
		}
	}
	if owner := a.engine.ProjectOf(sessionID); owner != "" && owner != a.projectID {
		return fmt.Errorf("conversation %s belongs to a different project", session.Code(sessionID))
	}
	// Picked up on the key and model it was having the conversation with.
	key, model := a.key, a.model
	if saved, ok := a.engine.Session(sessionID); ok && saved.KeyName != "" {
		key, model = saved.KeyName, saved.Model
	}
	_, err := a.engine.AddAgent(ctx, session.Agent{Name: a.nextName(), SessionID: sessionID, KeyName: key,
		Model: model, Dir: a.dir, Trust: a.trust})
	return err
}

func (a *acpEngine) ListSessions() []acpserver.Summary {
	var out []acpserver.Summary
	for _, running := range a.engine.Agents() {
		s, ok := a.engine.Session(running.SessionID)
		if !ok {
			continue
		}
		busy := len(s.Turns) > 0 && !s.Turns[len(s.Turns)-1].State.Terminal()
		out = append(out, acpserver.Summary{ID: s.ID, Title: s.Title, Dir: running.Dir,
			Mode: a.engine.Mode(s.ID).Name, Running: busy})
	}
	return out
}

func (a *acpEngine) Send(sessionID, text string) (string, error) {
	return a.engine.Send(sessionID, text)
}

func (a *acpEngine) Session(sessionID string) (core.Session, bool) {
	return a.engine.Session(sessionID)
}

func (a *acpEngine) Cancel(sessionID string) { a.engine.Cancel(sessionID) }

func (a *acpEngine) Events(after uint64) <-chan core.Event { return a.engine.Events(after) }

func (a *acpEngine) SetMode(sessionID, name string) error {
	mode, ok := core.ModeByName(name)
	if !ok {
		return errors.New("unknown mode " + name + ": use " + strings.Join(core.ModeNames(), ", "))
	}
	if reason := a.engine.ModeUnusable(sessionID, mode); reason != nil {
		return fmt.Errorf("%s cannot be used here: %w", name, reason)
	}
	return a.engine.SetMode(sessionID, mode)
}

// sameDir reports whether two paths name one directory, links resolved.
func sameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}
