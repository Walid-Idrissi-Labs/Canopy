package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
)

const keysUsage = `canopy keys - manage provider credentials

usage:
  canopy keys add <name>     store a credential, read from a prompt or stdin
  canopy keys list           show stored credentials, never their values
  canopy keys remove <name>  delete a credential
  canopy keys test <name>    check that a credential can be read back
  canopy keys rate <name>    record what this credential charges, so turns show a cost

flags for add:
  -provider string   anthropic or openai-compatible (default "anthropic")
  -base-url string   endpoint, required for openai-compatible

flags for rate:
  -in float          dollars per million input tokens
  -out float         dollars per million output tokens
  -cached float      dollars per million cached input tokens (default: same as -in)
  -clear             forget the rate, so turns read as unpriced again

The value is never taken from a command line argument. Arguments end up in shell
history and in the process list, where any other user on the machine can read them.

examples:
  canopy keys add claude
  canopy keys add kimi -provider openai-compatible -base-url https://api.moonshot.cn/v1
  pbpaste | canopy keys add claude
  canopy keys rate kimi -in 0.6 -out 2.5

Canopy holds published rates for Anthropic and knows that a local model is free. It
does not hold rates for the OpenAI compatible gateways, because the gateway sets the
price and there are many of them, so guessing from a model name would be a guess
presented as a fact. Your own figure is labelled as yours wherever it appears.
`

// secretFlagNames are flags a user might reasonably reach for to pass a credential inline.
//
// They are defined only so they can be refused with an explanation. Leaving them undefined would
// produce "flag provided but not defined", which tells someone they made a typo rather than that
// they were about to put a credential into their shell history.
var secretFlagNames = []string{"key", "secret", "value", "token", "api-key", "apikey", "password"}

func runKeys(args []string, out io.Writer) error {
	if len(args) == 0 {
		_, err := fmt.Fprint(out, keysUsage)
		return err
	}

	command, rest := args[0], args[1:]
	switch command {
	case "help", "-h", "--help":
		_, err := fmt.Fprint(out, keysUsage)
		return err
	case "add":
		return runKeysAdd(rest, out)
	case "list", "ls":
		return runKeysList(rest, out)
	case "remove", "rm", "delete":
		return runKeysRemove(rest, out)
	case "test":
		return runKeysTest(rest, out)
	case "rate", "price":
		return runKeysRate(rest, out)
	default:
		return fmt.Errorf("unknown keys command %q, try `canopy keys help`", command)
	}
}

// openKeyStore is swapped in tests so they never touch the real keychain, which would otherwise
// leave credentials on a developer's machine after a test run.
var openKeyStore = keys.Open

// openStore opens the key store and warns if credentials are going to a plain file.
//
// The warning is repeated on every use rather than shown once at setup, because the person who
// chose the backend is often not the person later assuming their keys are in the keychain.
func openStore(out io.Writer) (*keys.Store, error) {
	store, err := openKeyStore()
	if err != nil {
		return nil, err
	}
	if store.UsingInsecureBackend() {
		_, _ = fmt.Fprintf(os.Stderr,
			"warning: %s=file is set, so credentials are stored unencrypted on disk.\n"+
				"         Unset it to use the OS keychain.\n\n", keys.BackendEnvVar)
	}
	return store, nil
}

func runKeysAdd(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("keys add", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	providerName := flags.String("provider", string(core.ProviderAnthropic), "anthropic or openai-compatible")
	baseURL := flags.String("base-url", "", "endpoint, required for openai-compatible")

	refused := make(map[string]*string, len(secretFlagNames))
	for _, name := range secretFlagNames {
		refused[name] = flags.String(name, "", "not supported, see below")
	}

	// Go's flag package stops parsing at the first positional argument, so `keys add kimi
	// -provider openai-compatible` would treat the flags as extra positionals. Since that is the
	// natural way to type it, and the way the help text shows it, the name is taken off the front
	// before the flags are parsed.
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}

	if err := flags.Parse(args); err != nil {
		return err
	}

	for name, value := range refused {
		if *value != "" {
			return fmt.Errorf(
				"-%s is not supported. A credential passed as an argument is written to your shell "+
					"history and is visible in the process list to anyone else on this machine. "+
					"Run `canopy keys add <name>` and paste it at the prompt, or pipe it in on stdin",
				name)
		}
	}

	positional := flags.Args()
	if name == "" && len(positional) > 0 {
		name, positional = positional[0], positional[1:]
	}
	if name == "" {
		return errors.New("a name is required, for example `canopy keys add claude`")
	}
	if len(positional) > 0 {
		// Almost certainly `canopy keys add claude sk-ant-...`. Say so without echoing whatever
		// the extra argument was.
		return errors.New(
			"too many arguments. The credential is not passed on the command line, since arguments " +
				"reach shell history and the process list. Run `canopy keys add <name>` and paste it " +
				"at the prompt, or pipe it in on stdin")
	}

	if err := core.ValidateKeyName(name); err != nil {
		return err
	}

	provider := core.Provider(*providerName)
	if !provider.Valid() {
		return fmt.Errorf("unknown provider %q, use one of: anthropic, openai-compatible", *providerName)
	}
	if provider.RequiresBaseURL() && *baseURL == "" {
		return fmt.Errorf("provider %q needs -base-url, for example -base-url https://api.moonshot.cn/v1", provider)
	}

	secret, err := readSecret(name)
	if err != nil {
		return err
	}
	if secret.IsZero() {
		return errors.New("no value was entered, nothing was stored")
	}

	store, err := openStore(out)
	if err != nil {
		return err
	}

	meta, err := store.Put(core.KeyMetadata{
		Ref:     core.KeyRef{Name: name, Provider: provider},
		BaseURL: *baseURL,
	}, secret)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "Stored %q for %s in the %s (fingerprint %s).\n",
		meta.Ref.Name, meta.Ref.Provider, store.BackendName(), meta.Fingerprint)
	return err
}

// readSecret takes a credential from a terminal prompt or from stdin.
//
// Piped input is supported because scripting a key in from a password manager is a reasonable
// thing to want, and the alternative is people reaching for an argument.
func readSecret(name string) (core.Secret, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintf(os.Stderr, "Paste the value for %q (it will not be shown): ", name)
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return core.Secret{}, fmt.Errorf("reading the value: %w", err)
		}
		return core.NewSecret(strings.TrimSpace(string(raw))), nil
	}

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return core.Secret{}, fmt.Errorf("reading the value from stdin: %w", err)
	}
	return core.NewSecret(strings.TrimSpace(line)), nil
}

// runKeysRate records what a credential charges.
//
// Its own command rather than a flag on add, because correcting a price must not require re typing
// a secret. A flow that asks for one is a flow where people paste keys into shell history.
func runKeysRate(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("which key? For example `canopy keys rate kimi -in 0.6 -out 2.5`")
	}

	// The name comes off the front before parsing, because Go's flag package stops at the first
	// positional argument and would silently ignore every flag after it.
	name, rest := args[0], args[1:]

	flags := flag.NewFlagSet("keys rate", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	in := flags.Float64("in", 0, "dollars per million input tokens")
	outRate := flags.Float64("out", 0, "dollars per million output tokens")
	cached := flags.Float64("cached", 0, "dollars per million cached input tokens")
	clear := flags.Bool("clear", false, "forget the rate")
	if err := flags.Parse(rest); err != nil {
		return err
	}

	store, err := openStore(out)
	if err != nil {
		return err
	}
	meta, err := store.Metadata(core.KeyRef{Name: name})
	if err != nil {
		return err
	}

	if *clear {
		if err := store.SetRate(meta.Ref, core.KeyRate{}); err != nil {
			return err
		}
		_, err := fmt.Fprintf(out,
			"Forgot the rate for %q. Turns on it will read as unpriced rather than as free.\n", name)
		return err
	}

	rate := core.KeyRate{InputPerMTok: *in, OutputPerMTok: *outRate, CacheReadPerMTok: *cached}
	if rate.IsZero() {
		return showRate(out, meta)
	}
	if err := rate.Validate(); err != nil {
		return err
	}
	if err := store.SetRate(meta.Ref, rate); err != nil {
		return err
	}

	_, err = fmt.Fprintf(out,
		"Recorded $%g in and $%g out per million tokens for %q.\n"+
			"Shown as your own figure, never as a checked one.\n",
		rate.InputPerMTok, rate.OutputPerMTok, name)
	return err
}

// showRate prints what is currently recorded, which is what `canopy keys rate <name>` with no flags
// asks for. Reading before writing is the more common intent when somebody is unsure.
func showRate(out io.Writer, meta core.KeyMetadata) error {
	if meta.Rate.IsZero() {
		_, err := fmt.Fprintf(out,
			"No rate recorded for %q, so its turns read as unpriced.\n"+
				"Set one with `canopy keys rate %s -in 0.6 -out 2.5`.\n",
			meta.Ref.Name, meta.Ref.Name)
		return err
	}

	cached := meta.Rate.CacheReadPerMTok
	note := ""
	if cached == 0 {
		cached = meta.Rate.InputPerMTok
		note = " (assumed, since none was given)"
	}
	_, err := fmt.Fprintf(out,
		"%s: $%g in, $%g out, $%g cached%s, per million tokens.\nYour own figure.\n",
		meta.Ref.Name, meta.Rate.InputPerMTok, meta.Rate.OutputPerMTok, cached, note)
	return err
}

// formatRate is the rate column.
//
// Worth a column of its own because it answers the question somebody actually has when looking at
// this list: which of these will show me what a session cost. "Published" and "not set" are as
// useful as a figure, since both say where a number would come from.
func formatRate(meta core.KeyMetadata) string {
	if meta.Rate.IsZero() {
		if meta.Ref.Provider == core.ProviderAnthropic {
			return "published"
		}
		return "not set"
	}
	return fmt.Sprintf("$%g/$%g", meta.Rate.InputPerMTok, meta.Rate.OutputPerMTok)
}

func runKeysList(args []string, out io.Writer) error {
	if len(args) > 0 {
		return fmt.Errorf("`canopy keys list` takes no arguments")
	}

	store, err := openStore(out)
	if err != nil {
		return err
	}
	all, err := store.List()
	if err != nil {
		return err
	}

	if len(all) == 0 {
		_, err := fmt.Fprintf(out, "No credentials stored. Add one with `canopy keys add claude`.\n")
		return err
	}

	tab := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	w := &errWriter{w: tab}
	w.printf("NAME\tPROVIDER\tFINGERPRINT\tRATE\tADDED\tLAST USED\n")
	for _, meta := range all {
		w.printf("%s\t%s\t%s\t%s\t%s\t%s\n",
			meta.Ref.Name,
			meta.Ref.Provider,
			meta.Fingerprint,
			formatRate(meta),
			meta.CreatedAt.Format("2006-01-02"),
			formatLastUsed(meta.LastUsedAt))
	}
	if err := tab.Flush(); err != nil {
		return err
	}
	if w.err != nil {
		return w.err
	}

	_, err = fmt.Fprintf(out, "\nStored in the %s. Values are never displayed.\n", store.BackendName())
	return err
}

func formatLastUsed(at *time.Time) string {
	if at == nil {
		return "never"
	}
	return at.Format("2006-01-02")
}

func runKeysRemove(args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("a name is required, for example `canopy keys remove claude`")
	}
	name := args[0]

	store, err := openStore(out)
	if err != nil {
		return err
	}
	if err := store.Remove(core.KeyRef{Name: name}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Removed %q.\n", name)
	return err
}

func runKeysTest(args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("a name is required, for example `canopy keys test claude`")
	}
	name := args[0]

	store, err := openStore(out)
	if err != nil {
		return err
	}

	meta, err := store.Metadata(core.KeyRef{Name: name})
	if err != nil {
		return err
	}
	secret, err := store.Get(core.KeyRef{Name: name})
	if err != nil {
		return err
	}

	// The fingerprint is recomputed from what came back rather than trusted from the record. If
	// the two disagree, the stored value is not the one this metadata describes, and a caller
	// would otherwise use it believing it was.
	if got := secret.Fingerprint(); got != meta.Fingerprint {
		return fmt.Errorf(
			"key %q does not match its record: stored fingerprint %s, actual %s. "+
				"The credential was changed outside Canopy. Add it again with `canopy keys add %s`",
			name, meta.Fingerprint, got, name)
	}

	w := &errWriter{w: out}
	w.printf("%s is readable from the %s.\n", name, store.BackendName())
	w.printf("  provider     %s\n", meta.Ref.Provider)
	if meta.BaseURL != "" {
		w.printf("  base url     %s\n", meta.BaseURL)
	}
	w.printf("  fingerprint  %s\n", meta.Fingerprint)
	w.printf("\nThis checks storage only. Whether the provider accepts the credential is not\n")
	w.printf("checked yet, because no provider client exists until A2.\n")
	return w.err
}
