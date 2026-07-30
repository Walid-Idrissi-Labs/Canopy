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
)

const keysUsage = `canopy keys - manage provider credentials

usage:
  canopy keys add <name>     store a credential, read from a prompt or stdin
  canopy keys signin <name>  sign in with a subscription instead of pasting a key
  canopy keys signout <name> end a sign-in and delete what it left behind
  canopy keys model <name> <model>   change which model this credential talks to
  canopy keys models <name>  show every model this credential can be pointed at
  canopy keys list           show stored credentials, never their values
  canopy keys remove <name>  delete a credential
  canopy keys test <name>    say what is actually true of a credential
  canopy keys rate <name>    record what this credential charges, so turns show a cost

flags for add:
  -provider string   anthropic or openai-compatible (default "anthropic")
  -base-url string   endpoint, required for openai-compatible
  -model string      the model this credential talks to, required except for anthropic

flags for signin:
  -route string      which way in to use, listed by running it without one

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
  canopy keys model kimi moonshot-v1-32k
  canopy keys models claude
  canopy keys models add kimi minimaxai/minimax-m2.7 "MiniMax M2.7"
  pbpaste | canopy keys add claude
  canopy keys rate kimi -in 0.6 -out 2.5

Not every credential is a value you hold. Somebody with a Copilot seat, a Claude
Code installation or a ChatGPT subscription has nothing to paste, and signs in
instead: canopy keys signin. Nothing is typed, a code and a page are printed for
you to visit, and the same credential appears in the interface.

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

// refuseSecretFlags defines those flags on a command and returns the check that rejects them.
//
// Shared rather than written once per command, because the protection is only worth anything on
// every command somebody might reach for it on. `keys signin` needs it at least as much as `keys
// add` does: a person who has read that a subscription involves a token is exactly the person who
// tries `-token`, and the flag being undefined would answer them with "typo" rather than with what
// they were about to do to their shell history.
func refuseSecretFlags(flags *flag.FlagSet) func() error {
	refused := make(map[string]*string, len(secretFlagNames))
	for _, name := range secretFlagNames {
		refused[name] = flags.String(name, "", "not supported, see below")
	}
	return func() error {
		for name, value := range refused {
			if *value != "" {
				return fmt.Errorf(
					"-%s is not supported. A credential passed as an argument is written to your "+
						"shell history and is visible in the process list to anyone else on this "+
						"machine. Run `canopy keys add <name>` and paste it at the prompt, pipe it "+
						"in on stdin, or sign in with `canopy keys signin <name>` and paste nothing",
					name)
			}
		}
		return nil
	}
}

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
	case "signin", "login":
		return runKeysSignIn(rest, out)
	case "signout", "logout":
		return runKeysSignOut(rest, out)
	case "list", "ls":
		return runKeysList(rest, out)
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

	refused := refuseSecretFlags(flags)

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

	if err := refused(); err != nil {
		return err
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

	// Who each credential is signed in as, read before the table is drawn so the table knows whether
	// it needs the column at all. None of this touches the backend: an account and an expiry are
	// facts S-01 keeps out of the keychain half precisely so a listing can print them without
	// stopping to unlock anything.
	signIns := make(map[string]keys.SignIn, len(all))
	anySignedIn := false
	for _, meta := range all {
		in, err := store.SignIn(meta.Ref)
		if err != nil {
			continue
		}
		signIns[meta.Ref.Name] = in
		anySignedIn = anySignedIn || in.Kind.IsSignIn()
	}

	tab := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	w := &errWriter{w: tab}

	// The account column appears only when a credential has one. A machine with nothing but pasted
	// keys sees the listing it has always seen, rather than an empty column asking a question about
	// a feature it does not use.
	account := func(name string) string {
		if !anySignedIn {
			return ""
		}
		return "\t" + formatAccount(signIns[name])
	}

	header := "NAME\tPROVIDER\tMODEL\tFINGERPRINT\tRATE\tADDED\tLAST USED"
	if anySignedIn {
		header += "\tSIGNED IN AS"
	}
	w.printf("%s\n", header)
	for _, meta := range all {
		w.printf("%s\t%s\t%s\t%s\t%s\t%s\t%s%s\n",
			meta.Ref.Name,
			meta.Ref.Provider,
			formatModelFor(meta, signIns[meta.Ref.Name]),
			meta.Fingerprint,
			formatRate(meta),
			meta.CreatedAt.Format("2006-01-02"),
			formatLastUsed(meta.LastUsedAt),
			account(meta.Ref.Name))
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

// formatAccount is the signed-in column: who, and whether the grant is still good.
//
// A dash rather than a blank for a pasted credential, so a column that is empty for a row reads as
// "this one has no account" rather than as a value that failed to print.
func formatAccount(in keys.SignIn) string {
	if !in.Kind.IsSignIn() {
		return "-"
	}
	who := in.Account
	if who == "" {
		who = "(unnamed)"
	}
	switch {
	case in.Kind == keys.KindDelegated:
		return who + " (delegated)"
	case in.ExpiresAt == nil:
		return who
	case time.Now().After(*in.ExpiresAt):
		return who + " (lapsed)"
	default:
		return who
	}
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

// formatModelFor is formatModel for a credential that may not choose its own model.
//
// A delegated turn runs inside the vendor's own agent, which picks the model there. Naming Canopy's
// default in that column would be stating something that has no effect on a single message, and
// telling somebody to set one would be telling them to fix what is not broken. The credential screen
// says the same words for the same case.
func formatModelFor(meta core.KeyMetadata, in keys.SignIn) string {
	if in.Kind == keys.KindDelegated && meta.Model == "" {
		return "the vendor chooses"
	}
	return formatModel(meta)
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

// runKeysTest says the strongest true thing available about a credential.
//
// It was storage only, for every credential, because every credential was a value somebody pasted
// and a fingerprint comparison was the whole of what could be checked without spending money. A
// signed-in credential has no pasted value to fingerprint, so that check has nothing to do on one,
// and skipping it would leave the command answering "fine" for a credential it had not looked at.
// What it does instead is give each kind of credential the strongest honest answer available to that
// kind, and say in each case what was and was not asked of the vendor.
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

	in, err := store.SignIn(meta.Ref)
	if err != nil {
		return err
	}
	if in.Kind.IsSignIn() {
		return testSignedIn(out, store, meta, in)
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
	// It used to say the provider was not contacted "because no provider client exists until A2",
	// which stopped being true eight phases ago and had been a lie about the reason ever since. The
	// reason is the one below and it has not changed: the only way to ask whether this value is
	// still accepted is to make a request the account is billed for, and a command somebody runs to
	// check something should not spend their money to answer.
	w.printf("\nThis checks storage only. Nothing was sent to %s, so a value that is stored\n",
		meta.Ref.Provider)
	w.printf("correctly and is no longer accepted still passes here.\n")
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
