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
	flags := flag.NewFlagSet("acp", flag.ContinueOnError)
	flags.SetOutput(errOut)
	keyName := flags.String("key", "", "the named key to use; the default key when omitted")
	model := flags.String("model", "", "the model; the key's default when omitted")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}

	keyStore, err := openKeyStore()
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return exitUsage
	}
	resolver := session.NewKeyResolver(keyStore, version)
	resolver.Renews(signInSources())
	engine := session.New(resolver)
	defer engine.Close()
	if err := attachHistory(engine); err != nil {
		_, _ = fmt.Fprintf(errOut, "warning: history is not being saved: %v\n", err)
	}

	dir, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return exitUsage
	}
	engine.SetProjectID(gitpkg.WorkspaceID(dir))
	// The editor owns stdin, so there is no one to answer a trust prompt on it: an untrusted
	// repository's configuration is withheld, as for canopy run.
	project := gateProject(dir, loadProjectRaw(dir, errOut), strings.NewReader(""), errOut, false)
	engine.WithInstructions(projectInstructions(dir, project, errOut))
	enableLanguageServers(project)
	defer closeLanguageServers()
	engine.SetWebSearch(webSearchWanted())

	if *keyName == "" {
		*keyName = resolver.DefaultKeyName()
	}
	if *model == "" {
		*model = defaultModelFor(keyStore, *keyName)
	}
	adapter := &acpEngine{engine: engine, dir: dir, key: *keyName, model: *model, trust: projectTrust(project)}
	server := acpserver.New(adapter, out)

	registry, err := toolsFor(dir)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "warning: tools are not available: %v\n", err)
	} else {
		engine.WithTools(registry, projectTrust(project), agent.ApproverFunc(server.Approve))
	}
	stopServers := attachMCP(engine, dir, project)
	defer stopServers()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Serve(ctx, stdin); err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return exitFailed
	}
	return exitOK
}

// acpEngine is the engine as the ACP server sees it: each editor session is an agent of its own,
// working in the project canopy acp was started in.
type acpEngine struct {
	engine *session.Engine
	dir    string
	key    string
	model  string
	trust  core.TrustLevel

	mu    sync.Mutex
	count int
}

func (a *acpEngine) NewSession(ctx context.Context, cwd string) (string, error) {
	// The tools are rooted in one workspace, so a session elsewhere would edit a directory the
	// editor did not ask about.
	if cwd != "" && !sameDir(cwd, a.dir) {
		return "", fmt.Errorf("canopy acp serves %s; start another one in %s for that project", a.dir, cwd)
	}
	a.mu.Lock()
	a.count++
	name := fmt.Sprintf("editor-%d", a.count)
	a.mu.Unlock()
	added, err := a.engine.AddAgent(ctx, session.Agent{Name: name, KeyName: a.key, Model: a.model,
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
