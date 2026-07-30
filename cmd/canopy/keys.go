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

	"github.com/Walid-Idrissi-Labs/Canopy/internal/catalog"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/anthropic"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
)

const keysUsage = `canopy keys - manage provider credentials

usage:
  canopy keys add <name>     store a credential, read from a prompt or stdin
  canopy keys model <name> <model>   change which model this credential talks to
  canopy keys models <name>  show every model this credential can be pointed at
  canopy keys list           show stored credentials, never their values
  canopy keys rename <old> <new>     change what a credential is called
  canopy keys remove <name>  delete a credential
  canopy keys test <name>    check that a credential can be read back
  canopy keys rate <name>    record what this credential charges, so turns show a cost

flags for add:
  -provider string   anthropic or openai-compatible (default "anthropic")
  -base-url string   endpoint, required for openai-compatible
  -model string      the model this credential talks to, required except for anthropic

subcommands of models:
  canopy keys models <name>                     list what this key can run
  canopy keys models add <name> <id> [label]    teach it one Canopy does not know
  canopy keys models remove <name> <id>         forget one that was added by hand

flags for rate:
  -in float          dollars per million input tokens
  -out float         dollars per million output tokens
  -cached float      dollars per million cached input tokens (default: same as -in)
  -clear             forget the rate, so turns read as unpriced again

The value is never taken from a command line argument. Arguments end up in shell
history and in the process list, where any other user on the machine can read them.

examples:
  canopy keys add claude
  canopy keys add kimi -provider openai-compatible -base-url https://api.moonshot.cn/v1 -model moonshot-v1-8k
  canopy keys rename kimi moonshot
  canopy keys model kimi moonshot-v1-32k
  canopy keys models claude
  canopy keys models add kimi minimaxai/minimax-m2.7 "MiniMax M2.7"
  pbpaste | canopy keys add claude
  canopy keys rate kimi -in 0.6 -out 2.5

A key is one credential and many models. Canopy ships the lineups it knows, for
Anthropic and for OpenAI's own endpoint, and anything else is added by hand. The
list is a convenience and never a gate: keys model takes any id, listed or not,
because the day the list is wrong is the day it would block the one model you
actually want.

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
	case "rename", "mv":
		return runKeysRename(rest, out)
	case "remove", "rm", "delete":
		return runKeysRemove(rest, out)
	case "test":
		return runKeysTest(rest, out)
	case "rate", "price":
		return runKeysRate(rest, out)
	case "model":
		return runKeysModel(rest, out)
	case "models":
		return runKeysModels(rest, out)
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
	model := flags.String("model", "", "the model this credential talks to")

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
	// Refused at the point of storing rather than at the point of use. A credential with no model on
	// a provider that has no default is one that cannot answer a single message, and finding that out
	// later, from the far end, is a much worse place to learn it.
	if provider != core.ProviderAnthropic && strings.TrimSpace(*model) == "" {
		return fmt.Errorf(
			"provider %q has no default model, so name one with -model. "+
				"For example -model minimaxai/minimax-m2.7. "+
				"Anthropic is the only provider Canopy can pick a model for", provider)
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
		Model:   strings.TrimSpace(*model),
	}, secret)
	if err != nil {
		return err
	}

	using := meta.Model
	if using == "" {
		using = anthropic.DefaultModel
	}
	_, err = fmt.Fprintf(out, "Stored %q for %s in the %s (fingerprint %s), talking to %s.\n",
		meta.Ref.Name, meta.Ref.Provider, store.BackendName(), meta.Fingerprint, using)
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
	w.printf("NAME\tPROVIDER\tMODEL\tFINGERPRINT\tRATE\tADDED\tLAST USED\n")
	for _, meta := range all {
		w.printf("%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			meta.Ref.Name,
			meta.Ref.Provider,
			formatModel(meta),
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

// formatModel says which model a credential talks to, and says loudly when it cannot.
//
// Loudly, because a credential with no model on a provider that has no default cannot answer a
// single message, and the place that failure used to surface was the far end of somebody else's
// gateway complaining about a malformed request.
func formatModel(meta core.KeyMetadata) string {
	switch {
	case meta.Model != "":
		return meta.Model
	case meta.Ref.Provider == core.ProviderAnthropic:
		return anthropic.DefaultModel + " (default)"
	default:
		return "NOT SET, run `canopy keys model " + meta.Ref.Name + " <model>`"
	}
}

func formatLastUsed(at *time.Time) string {
	if at == nil {
		return "never"
	}
	return at.Format("2006-01-02")
}

// runKeysRename changes what a credential is called, without asking for its value again.
//
// Its own command rather than a flag on add, for the reason every other correction here has one:
// re-entering a secret to fix something that is not the secret is a flow where people go and find an
// API key, and that is a flow where keys end up in shell history and in clipboards.
//
// The conversations move with it. A credential's name is what each one writes down and what the
// resolver looks up on its next message, so a rename that stopped at the key store would leave every
// conversation started on it pointing at a name nothing answers to, and the failure would arrive one
// message later from somebody else's gateway. What this cannot reach is a Canopy already running
// beside it, which is holding those conversations in memory, so it says so.
func runKeysRename(args []string, out io.Writer) error {
	if len(args) != 2 {
		return errors.New("usage: canopy keys rename <old> <new>, " +
			"for example `canopy keys rename kimi moonshot`")
	}
	from, to := args[0], args[1]

	store, err := openStore(out)
	if err != nil {
		return err
	}
	meta, renameErr := store.Rename(core.KeyRef{Name: from}, to)
	if meta.Ref.Name == "" {
		return renameErr
	}

	// A backend cleanup warning means the rename still landed: metadata and the new secret agree,
	// while the old backend entry is an extra copy to remove. Stored conversations must still
	// follow. Returning the warning before moving them would turn a recoverable duplicate into a
	// credential whose sessions all point at the wrong name.
	moved, err := moveCredentialHistory(from, meta.Ref.Name)
	if err != nil {
		// The history update is one SQLite statement, so an error means no conversation was moved.
		// Put the credential back as well; only then is "fix the cause and repeat" truthful.
		rolledBack, rollbackErr := store.Rename(meta.Ref, from)
		switch {
		case rolledBack.Ref.Name == from && rollbackErr == nil:
			return fmt.Errorf(
				"the conversations could not be moved from %q to %q: %w. The credential was "+
					"restored to %q, so fix the history error and repeat the rename",
				from, meta.Ref.Name, err, from)
		case rolledBack.Ref.Name == from:
			return fmt.Errorf(
				"the conversations could not be moved from %q to %q: %v. The credential metadata "+
					"was restored to %q, but cleaning up the new backend entry also failed: %w",
				from, meta.Ref.Name, err, from, rollbackErr)
		default:
			// The rollback did not land. Name the split state rather than suggesting a repeat with
			// an old name the store no longer has.
			return fmt.Errorf(
				"the credential is now called %q, but its conversations still use %q because "+
					"their history update failed: %v. Restoring the credential name also failed: "+
					"%v. Do not repeat the old command; repair one side before sending another "+
					"message on those conversations",
				meta.Ref.Name, from, err, rollbackErr)
		}
	}

	if _, err := fmt.Fprintf(out, "%q is now called %q.\n", from, meta.Ref.Name); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "%s followed it. A Canopy already running will not, so restart it.\n",
		conversations(moved)); err != nil {
		return err
	}
	if renameErr != nil {
		// The operation is complete, but an old duplicate secret remains in the backend. Preserve
		// the store's actionable warning and a non-zero exit instead of hiding it behind success.
		return fmt.Errorf(
			"the credential and its conversations were renamed, but backend cleanup needs attention: %w",
			renameErr)
	}
	return nil
}

// moveCredentialHistory is replaceable in the command tests so the failure between the secret
// store and SQLite can be exercised without corrupting a real history file.
var moveCredentialHistory = renameInHistory

// renameInHistory moves the stored conversations onto the new credential name.
//
// No history at all is not a failure. Renaming a credential before ever having a conversation on it
// is an ordinary thing to do, and the first run of Canopy has no database to open.
func renameInHistory(from, to string) (int, error) {
	path, err := session.DefaultPath()
	if err != nil {
		return 0, err
	}
	storage, err := session.OpenStorage(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = storage.Close() }()

	return storage.RenameCredential(from, to)
}

// conversations counts in words, so the line reads as a sentence rather than as a log entry.
func conversations(n int) string {
	switch n {
	case 0:
		return "No conversation was recorded on it, so nothing else"
	case 1:
		return "The one conversation on it"
	default:
		return fmt.Sprintf("All %d conversations on it", n)
	}
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

// runKeysModel changes which model a credential talks to.
//
// Its own command rather than a flag on add, because changing the model must not require
// re-entering the secret. Somebody fixing a typo in a model id should not have to go and find their
// API key again, and a flow that asked them to would get it pasted from somewhere less careful.
func runKeysModel(args []string, out io.Writer) error {
	if len(args) < 2 {
		return errors.New("usage: canopy keys model <name> <model>, " +
			"for example `canopy keys model nim minimaxai/minimax-m2.7`")
	}
	name, model := args[0], strings.TrimSpace(strings.Join(args[1:], " "))
	if model == "" {
		return errors.New("a model id is required")
	}

	store, err := openStore(out)
	if err != nil {
		return err
	}
	ref := core.KeyRef{Name: name}
	if err := store.SetModel(ref, model); err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "%q now talks to %s.\n", name, model)
	return err
}

// runKeysModels is the plural: what a credential could be pointed at, rather than what it is.
//
// Two sources, kept apart in the listing on purpose. What the catalog knows is dated and may be
// wrong, and what somebody added by hand is theirs and is not. Printing them as one list would make
// the second look as though Canopy had supplied it, which is exactly the confusion the as-of line
// underneath exists to prevent.
func runKeysModels(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("which key? For example `canopy keys models claude`")
	}

	switch args[0] {
	case "add":
		return runKeysModelsAdd(args[1:], out)
	case "remove", "rm", "delete":
		return runKeysModelsRemove(args[1:], out)
	}

	if len(args) > 1 {
		return fmt.Errorf("unknown subcommand %q, try `canopy keys models %s`", args[1], args[0])
	}
	return listKeyModels(args[0], out)
}

func listKeyModels(name string, out io.Writer) error {
	store, err := openStore(out)
	if err != nil {
		return err
	}
	meta, err := store.Metadata(core.KeyRef{Name: name})
	if err != nil {
		return err
	}
	added, err := store.Models(meta.Ref)
	if err != nil {
		return err
	}

	known := catalog.For(meta.Ref.Provider, meta.BaseURL)
	if len(known) == 0 && len(added) == 0 {
		_, err := fmt.Fprintf(out,
			"Canopy knows no models for %q, and none have been added.\n"+
				"Add one with `canopy keys models add %s <id>`, or point the key straight at a "+
				"model with `canopy keys model %s <id>`.\n",
			name, name, name)
		return err
	}

	tab := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	w := &errWriter{w: tab}
	w.printf("\tID\tNAME\tFROM\n")
	for _, model := range known {
		w.printf("%s\t%s\t%s\t%s\n", current(meta, model.ID), model.ID, model.Label(), "catalog")
	}
	for _, model := range added {
		w.printf("%s\t%s\t%s\t%s\n", current(meta, model.ID), model.ID, model.Label(), "added")
	}
	if err := tab.Flush(); err != nil {
		return err
	}
	if w.err != nil {
		return w.err
	}

	_, err = fmt.Fprintf(out,
		"\nThe catalog was last checked on %s. Any model can be set whether it is listed\n"+
			"or not: `canopy keys model %s <id>`.\n",
		catalog.AsOf.Format("2006-01-02"), name)
	if err != nil {
		return err
	}
	// And says out loud when that date is old, rather than leaving somebody to subtract it from
	// today. A date on screen is not the same as the screen saying the date has gone stale.
	if note := catalog.StalenessNote(time.Now()); note != "" {
		_, err = fmt.Fprintf(out, "%s.\n", note)
	}
	return err
}

// current marks the model this credential actually talks to, which is the one thing a list of
// possibilities cannot say for itself.
func current(meta core.KeyMetadata, id string) string {
	if meta.Model == id {
		return "*"
	}
	return " "
}

func runKeysModelsAdd(args []string, out io.Writer) error {
	if len(args) < 2 {
		return errors.New("usage: canopy keys models add <name> <id> [label], " +
			"for example `canopy keys models add nim minimaxai/minimax-m2.7 \"MiniMax M2.7\"`")
	}
	name, id := args[0], args[1]
	label := strings.TrimSpace(strings.Join(args[2:], " "))

	store, err := openStore(out)
	if err != nil {
		return err
	}
	if err := store.AddModel(core.KeyRef{Name: name}, id, label); err != nil {
		return err
	}

	// Said out loud, because adding is not selecting and somebody who expected one call to do both
	// would otherwise find their next conversation still on the old model with nothing to explain it.
	_, err = fmt.Fprintf(out,
		"%q can now be pointed at %s. Point it there with `canopy keys model %s %s`.\n",
		name, id, name, id)
	return err
}

func runKeysModelsRemove(args []string, out io.Writer) error {
	if len(args) != 2 {
		return errors.New("usage: canopy keys models remove <name> <id>")
	}
	name, id := args[0], args[1]

	store, err := openStore(out)
	if err != nil {
		return err
	}
	if err := store.RemoveModel(core.KeyRef{Name: name}, id); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%q no longer offers %s.\n", name, id)
	return err
}
