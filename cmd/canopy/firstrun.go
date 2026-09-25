package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/catalog"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// runInit writes a canopy.json for this project with the tests its build files suggest, for a person
// to read and then trust. It never overwrites one, and nothing it writes runs until `canopy trust`.
func runInit(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	printOnly := flags.Bool("print", false, "print the proposal instead of writing it")
	if err := flags.Parse(args); err != nil {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "canopy.json")
	if _, err := os.Stat(path); err == nil && !*printOnly {
		return errors.New("this project already has a canopy.json; `canopy trust` shows what it runs")
	}
	tests := config.DetectTests(dir)
	if len(tests) == 0 {
		_, err := fmt.Fprintln(out, "No go.mod, Cargo.toml, package.json with a test script, or pytest setup here, "+
			"so there is no test to propose. The README's canopy.json section shows how to name one.")
		return err
	}
	data, err := json.MarshalIndent(struct {
		Tests []config.Test `json:"tests"`
	}{tests}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if *printOnly {
		_, err := out.Write(data)
		return err
	}
	// O_EXCL, so a file that appeared since the check above is not overwritten either.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	var names []string
	for _, test := range tests {
		names = append(names, strings.Join(test.Command.Argv, " "))
	}
	_, err = fmt.Fprintf(out, "Wrote canopy.json with %d tests: %s.\nRead it, then run `canopy trust` to let Canopy run them.\n",
		len(tests), strings.Join(names, ", "))
	return err
}

// importable is a credential another tool left in the environment, and the key it would become.
type importable struct {
	env      string
	name     string
	provider core.Provider
	baseURL  string
	model    string
	// endpointEnv names the variable that points this key at somewhere other than the provider;
	// a key set beside it belongs to that somewhere, so it is not imported as the provider's.
	endpointEnv string
}

// importables are the environment variables `canopy keys import` looks for. Only providers whose
// endpoint and a model are known, since a key stored without them cannot answer a message.
func importables() []importable {
	openAI := "https://api.openai.com/v1"
	var model string
	if models := catalog.For(core.ProviderOpenAICompatible, openAI); len(models) > 0 {
		model = models[0].ID
	}
	return []importable{
		{env: "ANTHROPIC_API_KEY", name: "claude", provider: core.ProviderAnthropic, endpointEnv: "ANTHROPIC_BASE_URL"},
		{env: "OPENAI_API_KEY", name: "openai", provider: core.ProviderOpenAICompatible, baseURL: openAI, model: model,
			endpointEnv: "OPENAI_BASE_URL"},
	}
}

// runKeysImport stores the provider keys found in the environment as named keys, after one
// confirmation. A key already stored, under any name, is not stored twice, and a name already taken
// is left alone.
func runKeysImport(args []string, in io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet("keys import", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	yes := flags.Bool("yes", false, "import without asking")
	if err := flags.Parse(args); err != nil {
		return err
	}
	store, err := openStore(out)
	if err != nil {
		return err
	}
	stored, err := store.List()
	if err != nil {
		return err
	}
	taken := map[string]bool{}
	byPrint := map[string]string{}
	for _, meta := range stored {
		taken[meta.Ref.Name] = true
		byPrint[meta.Fingerprint] = meta.Ref.Name
	}
	type found struct {
		importable
		secret core.Secret
	}
	var todo []found
	for _, candidate := range importables() {
		value := strings.TrimSpace(os.Getenv(candidate.env))
		if value == "" {
			continue
		}
		secret := core.NewSecret(value)
		switch {
		case os.Getenv(candidate.endpointEnv) != "":
			_, _ = fmt.Fprintf(out, "%s is left alone: %s points it at another endpoint, which `canopy keys add "+
				"-base-url` records.\n", candidate.env, candidate.endpointEnv)
		case byPrint[secret.Fingerprint()] != "":
			_, _ = fmt.Fprintf(out, "%s is already stored, as %q.\n", candidate.env, byPrint[secret.Fingerprint()])
		case taken[candidate.name]:
			_, _ = fmt.Fprintf(out, "%s: a key named %q already exists, so this one is left for "+
				"`canopy keys add` under another name.\n", candidate.env, candidate.name)
		default:
			todo = append(todo, found{candidate, secret})
			// One value in two variables is one key, stored once.
			byPrint[secret.Fingerprint()] = candidate.name
			taken[candidate.name] = true
		}
	}
	if len(todo) == 0 {
		_, err := fmt.Fprintln(out, "Nothing to import.")
		return err
	}
	for _, f := range todo {
		_, _ = fmt.Fprintf(out, "%s -> %q (%s, fingerprint %s)\n", f.env, f.name, f.provider, f.secret.Fingerprint())
	}
	if !*yes {
		_, _ = fmt.Fprintf(out, "Store these in the %s? [y/N] ", store.BackendName())
		answer, _ := bufio.NewReader(in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			_, err := fmt.Fprintln(out, "Nothing was stored.")
			return err
		}
	}
	for _, f := range todo {
		meta, err := store.Put(core.KeyMetadata{Ref: core.KeyRef{Name: f.name, Provider: f.provider},
			BaseURL: f.baseURL, Model: f.model}, f.secret)
		if err != nil {
			return fmt.Errorf("storing %s: %w", f.env, err)
		}
		_, _ = fmt.Fprintf(out, "Stored %q.\n", meta.Ref.Name)
	}
	return nil
}
