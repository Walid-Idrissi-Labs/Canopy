package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/pricing"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/acp"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/anthropic"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/codex"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/copilot"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/openai"
)

const askUsage = `canopy ask - send one message to a provider

usage:
  canopy ask [flags] "your prompt"
  echo "your prompt" | canopy ask [flags]

flags:
  -key string      which stored credential to use (default: the only one, if there is one)
  -model string    model id, overriding whichever one the credential is set to
  -effort string   low, medium, high, xhigh or max
  -system string   system prompt

The smallest thing that proves the whole path works: a credential, a request,
and a streamed reply. Useful permanently as a way to check a key or a model
without opening the interface.
`

func runAsk(args []string, out io.Writer) error {
	// Handled before flag parsing, because the flag package's own -h output is a bare list with no
	// examples and exits non-zero, which reads as a failure rather than as help.
	for _, arg := range args {
		if arg == "-h" || arg == "--help" || arg == "help" {
			_, err := fmt.Fprint(out, askUsage)
			return err
		}
	}

	flags := flag.NewFlagSet("ask", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	keyName := flags.String("key", "", "which stored credential to use")
	model := flags.String("model", "", "model id")
	effortName := flags.String("effort", "", "low, medium, high, xhigh or max")
	system := flags.String("system", "", "system prompt")

	if err := flags.Parse(args); err != nil {
		return err
	}

	prompt, err := readPrompt(flags.Args())
	if err != nil {
		return err
	}

	effort := core.Effort(*effortName)
	if !effort.Valid() {
		return fmt.Errorf("unknown effort %q, use one of: low, medium, high, xhigh, max", *effortName)
	}

	store, err := openStore(out)
	if err != nil {
		return err
	}

	meta, err := resolveKey(store, *keyName)
	if err != nil {
		return err
	}
	// The credential's own model unless one was named on the command line. The flag still wins,
	// because trying a different model against a key you already have is most of what this command
	// is for, and having to change the stored setting to do it would be absurd.
	if *model == "" {
		*model = meta.Model
	}

	client, id, err := clientFor(store, meta, *model)
	if err != nil {
		return err
	}

	// Interrupt cancels the turn rather than killing the process, so the connection closes and the
	// partial reply stays on screen marked as interrupted.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	stream, err := client.Stream(ctx, core.Request{
		Model:  *model,
		System: *system,
		Effort: effort,
		Messages: []core.Message{
			{Role: core.RoleUser, Text: prompt},
		},
	})
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()

	if err := store.MarkUsed(meta.Ref); err != nil {
		// Worth saying, not worth failing the request over: the answer is what was asked for.
		fmt.Fprintf(os.Stderr, "warning: could not record key usage: %v\n", err)
	}

	// Priced here rather than in the provider clients: what a turn costs depends on which endpoint
	// answered, and only the credential knows that.
	return drain(stream, out, pricer(id))
}

// pricer turns a model identity into the function that costs a turn.
//
// The note is what the figure alone cannot say: why there is no figure, or how old the numbers
// behind it are. Both are the difference between a number somebody can act on and one they should
// not.
func pricer(id pricing.ModelID) func(core.Usage) (core.Usage, string) {
	return func(usage core.Usage) (core.Usage, string) {
		usage, reason := pricing.Apply(id, usage)
		if reason != "" {
			return usage, reason
		}
		if note := pricing.StalenessNote(time.Now()); note != "" {
			return usage, note
		}
		// Caching is invisible unless it is reported, and an invisible saving is one nobody
		// notices has stopped happening.
		if saving, ok := pricing.Saving(id, usage); ok {
			return usage, cacheNote(saving)
		}
		return usage, ""
	}
}

// cacheNote describes what caching did to this turn's bill.
//
// It says "spent" rather than hiding a negative saving, because the turn that fills a cache really
// does pay a premium, and a report that only ever shows good news is one nobody can calibrate
// against.
func cacheNote(saving float64) string {
	if saving < 0 {
		return fmt.Sprintf("caching cost $%.4f extra on this turn, which later turns read back",
			-saving)
	}
	return fmt.Sprintf("caching saved $%.4f on this turn", saving)
}

// drain writes the reply as it arrives and reports how the turn ended.
//
// The stop reason is checked before anything is presented as an answer. A refusal arrives as a
// successful response with possibly empty content, so a caller that just printed the text would
// show nothing and exit zero, as though the request had been answered.
func drain(stream core.Stream, out io.Writer, price func(core.Usage) (core.Usage, string)) error {
	w := &errWriter{w: out}
	var wroteText bool

	for stream.Next() {
		event := stream.Event()
		switch event.Kind {
		case core.EventText:
			w.printf("%s", event.Text)
			wroteText = true

		case core.EventToolCall:
			w.printf("\n[tool call: %s]\n", event.ToolCall.Name)

		case core.EventNotice:
			// Printed as its own line rather than merged into the reply, because it comes from
			// Canopy and not from the model, and it is usually the reason the reply looks
			// different from what was expected.
			w.printf("\n[%s]\n", event.Text)

		case core.EventDone:
			if wroteText {
				w.printf("\n")
			}
			return report(event, w, price)
		}
	}

	if err := stream.Err(); err != nil {
		return err
	}
	// A stream that ended without a done event is a bug in the provider adapter, not a completed
	// turn. Saying so is better than exiting zero on an answer nobody got.
	return errors.New("the stream ended without reporting how it finished")
}

func report(event core.StreamEvent, w *errWriter, price func(core.Usage) (core.Usage, string)) error {
	usage, note := price(event.Usage)

	switch event.StopReason {
	case core.StopEndTurn, core.StopToolUse:
		printUsageLine(usage, note, w)
		return w.err

	case core.StopRefusal:
		printUsageLine(usage, note, w)
		return errors.New("the provider declined this request")

	case core.StopMaxTokens:
		printUsageLine(usage, note, w)
		return errors.New("the reply was cut off at the output limit, so it is incomplete. " +
			"Raise the limit or shorten the request")

	case core.StopCancelled:
		w.printf("\n[interrupted, the reply above is partial]\n")
		printUsageLine(usage, note, w)
		return errors.New("cancelled")

	default:
		if event.Err != nil {
			return event.Err
		}
		return fmt.Errorf("the turn ended as %s", event.StopReason)
	}
}

func printUsageLine(usage core.Usage, note string, w *errWriter) {
	parts := []string{
		fmt.Sprintf("%d in", usage.InputTokens),
		fmt.Sprintf("%d out", usage.OutputTokens),
	}
	if usage.CacheReadTokens > 0 {
		parts = append(parts, fmt.Sprintf("%d cached", usage.CacheReadTokens))
	}

	// Cost is only printed when it is known. A zero rendered as a dollar figure would read as
	// "this was free", which is a different claim from "we could not price it".
	cost := "cost unknown"
	if usage.CostKnown {
		cost = fmt.Sprintf("$%.4f", usage.CostUSD)
	}

	line := fmt.Sprintf("[%s tokens, %s]", strings.Join(parts, ", "), cost)
	// The note says either why there is no figure or how old the figure is. Both are the difference
	// between a number somebody can act on and one they should not.
	if note != "" {
		line += "\n" + note
	}

	_, _ = fmt.Fprintf(os.Stderr, "\n%s\n", line)
}

// readPrompt takes the prompt from the arguments or from stdin.
func readPrompt(args []string) (string, error) {
	if len(args) > 0 {
		return strings.Join(args, " "), nil
	}

	if isTerminal(os.Stdin) {
		return "", errors.New("a prompt is required, for example `canopy ask \"hello\"`")
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("reading the prompt from stdin: %w", err)
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return "", errors.New("the prompt was empty")
	}
	return prompt, nil
}

// newClient builds the provider client a credential points at.
//
// The credential decides, not a flag. That is the whole point of naming keys: `-key nemotron`
// carries its provider and endpoint with it, so nothing above has to be told which API to speak.
// clientFor builds the client for a credential and says how a turn on it should be priced.
//
// Kind before provider, for the reason internal/session/resolver.go gives at the same fork: a
// delegated credential is Anthropic by provider and holds no secret at all, so asking the refresher
// for one refuses it before the route is ever reached.
//
// This is the second place the provider fork lives, which is what constraint 4 already says about
// this command and the interface. It is worth two answers rather than one only because they answer
// slightly different questions, and the thing that must not drift, what a credential is worth right
// now, is keys.Refresher for both.
func clientFor(
	store *keys.Store, meta core.KeyMetadata, model string,
) (core.ProviderClient, pricing.ModelID, error) {
	in, err := store.SignIn(meta.Ref)
	if err != nil {
		return nil, pricing.ModelID{}, err
	}

	if in.Kind == keys.KindDelegated {
		// Delegated, so nothing downstream prints a dollar figure for a turn metered against a plan
		// that is billed monthly. See pricing.ModelID.Delegated.
		id := pricing.ModelID{Provider: meta.Ref.Provider, Model: model, Delegated: true}

		// Route before provider, the same fork the resolver makes and for the same reason: both
		// delegated routes reach a different vendor and one of them is openai-compatible, so a
		// switch on provider would hand a ChatGPT credential to Claude Code.
		if in.Route == codex.Route {
			found, findErr := codex.Discovery{}.Find()
			if findErr != nil {
				return nil, pricing.ModelID{}, fmt.Errorf(
					"key %q delegates to Codex: %w", meta.Ref.Name, findErr)
			}
			return codex.New(found, codex.WithVersion(version)), id, nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), delegateTimeout)
		defer cancel()

		found, findErr := acp.Discovery{}.Find(ctx)
		if findErr != nil {
			return nil, pricing.ModelID{}, fmt.Errorf(
				"key %q delegates to Claude Code: %w", meta.Ref.Name, findErr)
		}
		return acp.New(found, acp.WithVersion(version)), id, nil
	}

	// Through the refresher rather than straight to the store, so a signed-in credential whose token
	// is nearly out is renewed before the request is built rather than discovered as a 401 after it
	// was sent. `canopy ask` and the interface have to agree about what a credential is worth, and
	// the one place that decides is keys.Refresher.
	// The refresher is told which routes exist before it is asked for anything, so a Copilot grant
	// close to expiry is renewed here exactly as it would be inside the interface. Without this the
	// two surfaces would disagree about a credential, which is the thing S-02 exists to prevent.
	refresher := keys.NewRefresher(store)
	refresher.Renews(signInSources())
	secret, err := refresher.Credential(meta)
	if err != nil {
		return nil, pricing.ModelID{}, err
	}

	if in.Route == copilot.Route {
		if model == "" {
			model = meta.Model
		}
		// One conversation, one message, and then the process ends, which is the one case where the
		// session-per-conversation arrangement costs nothing: there is no second turn to remember.
		// Named after the credential rather than after the provider so that somebody with two seats
		// can see which answered.
		//
		// Delegated for the same reason the interface marks it so: a Copilot seat is billed monthly
		// and these tokens are metered against it, so a per-token figure would be arithmetic
		// presented as somebody's spend. See pricing.ModelID.Delegated.
		client := copilot.New(meta.Ref.Name, copilot.Conversation{Token: secret, Model: model})
		id := pricing.ModelID{Provider: meta.Ref.Provider, Model: model, Delegated: true}
		return client, id, nil
	}

	client, err := newClient(meta, secret, model)
	if err != nil {
		return nil, pricing.ModelID{}, err
	}
	return client, pricing.NewModelID(meta.Ref.Provider, meta.BaseURL, model).
		WithUserRate(meta.Rate), nil
}

// delegateTimeout bounds looking for the delegated agent before a turn.
const delegateTimeout = 30 * time.Second

func newClient(meta core.KeyMetadata, secret core.Secret, model string) (core.ProviderClient, error) {
	switch meta.Ref.Provider {
	case core.ProviderAnthropic:
		return anthropic.New(secret), nil

	case core.ProviderOpenAICompatible:
		// No default model here, deliberately. Anthropic has one because we know what runs there;
		// this provider is whatever endpoint the user pointed it at, and guessing a model name for
		// someone else's gateway would fail with a confusing 404 instead of a clear message.
		if model == "" {
			return nil, fmt.Errorf(
				"key %q is an %s credential, which has no default model, so name one with -model "+
					"(for example -model nvidia/llama-3.3-nemotron-super-49b-v1)",
				meta.Ref.Name, meta.Ref.Provider)
		}
		return openai.New(meta.BaseURL, secret, openai.WithName(meta.Ref.Name)), nil

	default:
		return nil, fmt.Errorf("key %q has provider %q, which this build does not know how to reach",
			meta.Ref.Name, meta.Ref.Provider)
	}
}

// resolveKey picks which credential to use.
//
// With one stored credential the choice is obvious and asking would be pointless. With several it
// refuses and lists them, rather than picking one: silently choosing which key gets billed is not
// a decision this should make on someone's behalf.
func resolveKey(store *keys.Store, name string) (core.KeyMetadata, error) {
	if name != "" {
		return store.Metadata(core.KeyRef{Name: name})
	}

	all, err := store.List()
	if err != nil {
		return core.KeyMetadata{}, err
	}

	switch len(all) {
	case 0:
		return core.KeyMetadata{}, errors.New(
			"no credentials stored. Add one with `canopy keys add claude`")
	case 1:
		return all[0], nil
	default:
		names := make([]string, len(all))
		for i, meta := range all {
			names[i] = meta.Ref.Name
		}
		return core.KeyMetadata{}, fmt.Errorf(
			"several credentials could be used (%s), so pick one with -key. "+
				"Choosing which key gets billed is not a decision to make silently",
			strings.Join(names, ", "))
	}
}
