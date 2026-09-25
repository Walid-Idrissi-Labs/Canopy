package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
)

func memoryStore(t *testing.T) *keys.Store {
	t.Helper()
	store := keys.NewStore(keys.NewMemoryBackend(), filepath.Join(t.TempDir(), "keys.json"))
	original := openKeyStore
	openKeyStore = func() (*keys.Store, error) { return store, nil }
	t.Cleanup(func() { openKeyStore = original })
	return store
}

// init writes what the build files suggest, as a file config.Load reads back, and never over one.
func TestInitWritesTheDetectedTests(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	var out bytes.Buffer
	if err := runInit(nil, &out); err != nil {
		t.Fatal(err)
	}
	project, found, err := config.Load(dir)
	if err != nil || !found || len(project.Tests) != 3 || project.Tests[1].Command.Argv[1] != "test" {
		t.Fatalf("wrote %+v, %v, %v", project, found, err)
	}
	if !strings.Contains(out.String(), "canopy trust") {
		t.Fatalf("it did not say the file has to be trusted before it runs: %q", out.String())
	}
	if err := runInit(nil, &out); err == nil {
		t.Fatal("a second init overwrote canopy.json")
	}
}

func TestInitWritesNothingWhenNothingIsFound(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var out bytes.Buffer
	if err := runInit(nil, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "canopy.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a canopy.json with no tests in it was written")
	}
	out.Reset()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runInit([]string{"-print"}, &out); err != nil || !strings.Contains(out.String(), `"cargo"`) {
		t.Fatalf("print gave %q, %v", out.String(), err)
	}
	if _, err := os.Stat(filepath.Join(dir, "canopy.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("-print wrote the file")
	}
}

// import asks once, stores what the environment holds under the names shown, and stores nothing
// when the answer is anything but yes.
func TestImportAsksThenStores(t *testing.T) {
	store := memoryStore(t)
	t.Setenv("ANTHROPIC_API_KEY", canary)
	t.Setenv("OPENAI_API_KEY", "sk-openai-test-value")
	var out bytes.Buffer
	if err := runKeysImport(nil, strings.NewReader("n\n"), &out); err != nil {
		t.Fatal(err)
	}
	if listed, _ := store.List(); len(listed) != 0 {
		t.Fatalf("a no stored %d keys", len(listed))
	}
	if strings.Contains(out.String(), canary) {
		t.Fatal("the value of a key was printed")
	}
	out.Reset()
	if err := runKeysImport(nil, strings.NewReader("y\n"), &out); err != nil {
		t.Fatal(err)
	}
	listed, _ := store.List()
	if len(listed) != 2 {
		t.Fatalf("stored %+v", listed)
	}
	secret, err := store.Get(core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic})
	if err != nil || secret.Reveal() != canary {
		t.Fatal("the Anthropic key was not stored as claude")
	}
	for _, meta := range listed {
		if meta.Ref.Name == "openai" && (meta.BaseURL != "https://api.openai.com/v1" || meta.Model == "") {
			t.Fatalf("the OpenAI key cannot answer a message: %+v", meta)
		}
	}

	// Again: both are stored already, so nothing is asked and nothing is added.
	out.Reset()
	if err := runKeysImport(nil, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already stored") || !strings.Contains(out.String(), "Nothing to import") {
		t.Fatalf("a second import said %q", out.String())
	}
}

// A name already in use is left alone, not overwritten with a different key.
func TestImportLeavesATakenNameAlone(t *testing.T) {
	store := memoryStore(t)
	if _, err := store.Put(core.KeyMetadata{Ref: core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic}},
		core.NewSecret("sk-ant-someone-elses")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_API_KEY", canary)
	t.Setenv("OPENAI_API_KEY", "")
	var out bytes.Buffer
	if err := runKeysImport([]string{"-yes"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	secret, _ := store.Get(core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic})
	if secret.Reveal() == canary {
		t.Fatal("an existing key was replaced by the one in the environment")
	}
	if !strings.Contains(out.String(), "already exists") {
		t.Fatalf("said %q", out.String())
	}
}

func TestDoctorSaysWhatIsMissingAndWhatToDo(t *testing.T) {
	store := memoryStore(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := doctorEnv{
		getenv: func(name string) string {
			return map[string]string{"ANTHROPIC_API_KEY": "x", "TMUX": "/tmp/tmux"}[name]
		},
		lookPath: func(name string) (string, error) {
			if name == "gopls" {
				return "/bin/gopls", nil
			}
			return "", errors.New("not found")
		},
		gitLine:  func() (string, error) { return "git version 2.50.0", nil },
		gitTop:   func() (string, error) { return "", errors.New("not a repository") },
		dir:      dir,
		keyStore: openKeyStore,
		sandbox:  func() error { return errors.New("sandbox-exec is missing") },
		terminal: true,
	}
	said := func() string {
		var lines []string
		for _, c := range diagnose(env) {
			lines = append(lines, c.level+" "+c.what+": "+c.say)
		}
		return strings.Join(lines, "\n")
	}
	got := said()
	for _, want := range []string{
		"ok git: git version 2.50.0",
		"note repository: this directory is not in a git repository",
		"warn keys: none stored; `canopy keys import` stores ANTHROPIC_API_KEY",
		"warn sandbox: sandbox-exec is missing",
		"note canopy.json: none; `canopy init` writes one with the 3 tests",
		"ok language servers: gopls",
		"note claude route: claude not found",
		"note tmux:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if _, err := store.Put(core.KeyMetadata{Ref: core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic}},
		core.NewSecret(canary)); err != nil {
		t.Fatal(err)
	}
	env.gitLine = func() (string, error) { return "", errors.New("no git") }
	env.keyStore = func() (*keys.Store, error) { return store, nil }
	got = said()
	if !strings.Contains(got, "fail git:") || !strings.Contains(got, "ok keys: 1 stored") {
		t.Errorf("after the changes:\n%s", got)
	}
	env.keyStore = func() (*keys.Store, error) { return nil, errors.New("no keychain") }
	if got := said(); !strings.Contains(got, "fail key store: no keychain") || !strings.Contains(got, keys.BackendEnvVar+"=file") {
		t.Errorf("a missing keychain does not name the way out:\n%s", got)
	}
}

type unreachableBackend struct{ keys.Backend }

func (unreachableBackend) Name() string               { return "keychain" }
func (unreachableBackend) Get(string) (string, error) { return "", errors.New("no Secret Service") }

// What doctor does with each thing it can be told, and the exit code that follows.
func TestDoctorNamesEachProblem(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CANOPY_TRUST_FILE", filepath.Join(t.TempDir(), "trust.json"))
	if err := os.WriteFile(filepath.Join(dir, "canopy.json"),
		[]byte(`{"tests": [{"name": "unit", "command": {"argv": ["go", "test"]}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{"CANOPY_SANDBOX": "off", "CANOPY_CLAUDE_ACP": "/opt/bridge"}
	env := doctorEnv{
		getenv: func(name string) string { return vars[name] },
		lookPath: func(name string) (string, error) {
			if name == "/opt/bridge" {
				return name, nil
			}
			return "", errors.New("not found")
		},
		gitLine: func() (string, error) { return "git version 2.50.0", nil },
		gitTop:  func() (string, error) { return "/elsewhere", nil },
		dir:     dir,
		keyStore: func() (*keys.Store, error) {
			return keys.NewStore(unreachableBackend{keys.NewMemoryBackend()}, filepath.Join(t.TempDir(), "k.json")), nil
		},
		sandbox:  func() error { return nil },
		terminal: true,
	}
	checks := diagnose(env)
	var lines []string
	for _, c := range checks {
		lines = append(lines, c.level+" "+c.what+": "+c.say)
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{
		"fail key store: keychain cannot be reached: no Secret Service",
		"warn sandbox: available but switched off",
		"warn canopy.json: not trusted",
		"note repository: inside /elsewhere",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	var out bytes.Buffer
	if code := reportChecks(&out, checks); code != exitFailed {
		t.Errorf("a failed check exited %d", code)
	}
	if code := reportChecks(&out, []check{{"ok", "git", "fine"}, {"warn", "x", "y"}}); code != exitOK {
		t.Errorf("no failure exited %d", code)
	}
	if !strings.Contains(got, "ok claude bridge: /opt/bridge found") {
		t.Errorf("the bridge named by CANOPY_CLAUDE_ACP was not found:\n%s", got)
	}
}

// Only y or yes stores; a key pointed at another endpoint is left alone; one value in two variables
// is stored once.
func TestImportIsCarefulAboutWhatItStores(t *testing.T) {
	store := memoryStore(t)
	t.Setenv("ANTHROPIC_API_KEY", canary)
	t.Setenv("OPENAI_API_KEY", canary)
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("OPENAI_BASE_URL", "")
	var out bytes.Buffer
	if err := runKeysImport(nil, strings.NewReader("sure\n"), &out); err != nil {
		t.Fatal(err)
	}
	if listed, _ := store.List(); len(listed) != 0 {
		t.Fatal("an answer other than yes stored keys")
	}
	if err := runKeysImport([]string{"-yes"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if listed, _ := store.List(); len(listed) != 1 {
		t.Fatalf("one value was stored %d times", len(listed))
	}

	store = memoryStore(t)
	t.Setenv("OPENAI_API_KEY", "sk-proxy")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "https://openrouter.ai/api/v1")
	out.Reset()
	if err := runKeysImport([]string{"-yes"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if listed, _ := store.List(); len(listed) != 0 || !strings.Contains(out.String(), "OPENAI_BASE_URL") {
		t.Fatalf("a key meant for another endpoint was stored as OpenAI's: %q", out.String())
	}
}
