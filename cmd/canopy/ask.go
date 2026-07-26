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

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/anthropic"
)

const askUsage = `canopy ask - send one message to a provider

usage:
  canopy ask [flags] "your prompt"
  echo "your prompt" | canopy ask [flags]

flags:
  -key string      which stored credential to use (default: the only one, if there is one)
  -model string    model id (default: the provider's default)
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

	ref, err := resolveKey(store, *keyName)
	if err != nil {
		return err
	}
	secret, err := store.Get(ref)
	if err != nil {
		return err
	}

	// Interrupt cancels the turn rather than killing the process, so the connection closes and the
	// partial reply stays on screen marked as interrupted.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := anthropic.New(secret)
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

	if err := store.MarkUsed(ref); err != nil {
		// Worth saying, not worth failing the request over: the answer is what was asked for.
		fmt.Fprintf(os.Stderr, "warning: could not record key usage: %v\n", err)
	}

	return drain(stream, out)
}

// drain writes the reply as it arrives and reports how the turn ended.
//
// The stop reason is checked before anything is presented as an answer. A refusal arrives as a
// successful response with possibly empty content, so a caller that just printed the text would
// show nothing and exit zero, as though the request had been answered.
func drain(stream core.Stream, out io.Writer) error {
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

		case core.EventDone:
			if wroteText {
				w.printf("\n")
			}
			return report(event, w)
		}
	}

	if err := stream.Err(); err != nil {
		return err
	}
	// A stream that ended without a done event is a bug in the provider adapter, not a completed
	// turn. Saying so is better than exiting zero on an answer nobody got.
	return errors.New("the stream ended without reporting how it finished")
}

func report(event core.StreamEvent, w *errWriter) error {
	switch event.StopReason {
	case core.StopEndTurn, core.StopToolUse:
		printUsageLine(event.Usage, w)
		return w.err

	case core.StopRefusal:
		printUsageLine(event.Usage, w)
		return errors.New("the provider declined this request")

	case core.StopMaxTokens:
		printUsageLine(event.Usage, w)
		return errors.New("the reply was cut off at the output limit, so it is incomplete. " +
			"Raise the limit or shorten the request")

	case core.StopCancelled:
		w.printf("\n[interrupted, the reply above is partial]\n")
		printUsageLine(event.Usage, w)
		return errors.New("cancelled")

	default:
		if event.Err != nil {
			return event.Err
		}
		return fmt.Errorf("the turn ended as %s", event.StopReason)
	}
}

func printUsageLine(usage core.Usage, w *errWriter) {
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

	_, _ = fmt.Fprintf(os.Stderr, "\n[%s tokens, %s]\n", strings.Join(parts, ", "), cost)
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

// resolveKey picks which credential to use.
//
// With one stored credential the choice is obvious and asking would be pointless. With several it
// refuses and lists them, rather than picking one: silently choosing which key gets billed is not
// a decision this should make on someone's behalf.
func resolveKey(store *keys.Store, name string) (core.KeyRef, error) {
	if name != "" {
		meta, err := store.Metadata(core.KeyRef{Name: name})
		if err != nil {
			return core.KeyRef{}, err
		}
		return meta.Ref, nil
	}

	all, err := store.List()
	if err != nil {
		return core.KeyRef{}, err
	}

	var usable []core.KeyMetadata
	for _, meta := range all {
		if meta.Ref.Provider == core.ProviderAnthropic {
			usable = append(usable, meta)
		}
	}

	switch len(usable) {
	case 0:
		if len(all) > 0 {
			return core.KeyRef{}, errors.New(
				"no anthropic credential is stored. `canopy ask` only speaks to Anthropic so far, " +
					"other providers arrive in A2-06. Add one with `canopy keys add claude`")
		}
		return core.KeyRef{}, errors.New(
			"no credentials stored. Add one with `canopy keys add claude`")
	case 1:
		return usable[0].Ref, nil
	default:
		names := make([]string, len(usable))
		for i, meta := range usable {
			names[i] = meta.Ref.Name
		}
		return core.KeyRef{}, fmt.Errorf(
			"several credentials could be used (%s), so pick one with -key. "+
				"Choosing which key gets billed is not a decision to make silently",
			strings.Join(names, ", "))
	}
}
