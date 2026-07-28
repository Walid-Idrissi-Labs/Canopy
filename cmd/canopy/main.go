// Command canopy is the entry point.
//
// Running it with no arguments opens a conversation in the current directory. The subcommands are
// the headless half, and they are kept because the engine has to be inspectable without a terminal
// UI in the way: when the interface shows something surprising, there needs to be a way to ask the
// engine directly what it thinks.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
)

const usage = `canopy - a terminal coding agent for running several at once

usage:
  canopy               open a chat in this directory
  canopy pickup CODE   reopen the conversation named by a code Canopy printed
  canopy keys          manage provider credentials
  canopy ask           send one message to a provider and stream the reply
  canopy search        find a message across every saved conversation
  canopy report        run this repository's checks and print a markdown summary
  canopy snapshot      print the current project snapshot as JSON
  canopy watch         stream events as JSON lines until interrupted
  canopy demo          drive the stale flip and show it happening
  canopy version       print the version

flags:
  -source string       where state comes from (default "fake")

The snapshot, watch and demo commands read a fake project rather than this directory. They
exist to exercise the engine, and the interface is what reads real worktrees.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "canopy: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	// No arguments opens the dashboard, since that is what someone typing "canopy" wants.
	//
	// Unless there is no terminal to open it in. Piped output, CI and cron all end up here, and
	// the raw failure is "could not open a new TTY: device not configured", which tells the
	// reader nothing about what they did or what to do instead.
	if len(args) == 0 {
		if !isTerminal(os.Stdout) {
			printUsage(os.Stdout)
			return nil
		}
		return runChat("")
	}

	command := args[0]

	// pickup takes a code as free text, so it is dispatched before the shared flag set gets a
	// chance to try to parse one that begins with a dash.
	if command == "pickup" || command == "resume" {
		return runPickup(args[1:])
	}

	// keys owns its own subcommands and flags, so it is dispatched before the shared flag set
	// gets a chance to reject them.
	if command == "keys" {
		return runKeys(args[1:], os.Stdout)
	}
	// ask owns its own flags too, so it is dispatched before the shared flag set sees them.
	if command == "ask" {
		return runAsk(args[1:], os.Stdout)
	}
	// search takes its query as free text, which the shared flag set would try to parse as flags
	// the moment somebody searched for something beginning with a dash.
	if command == "search" || command == "find" {
		return runSearch(args[1:], os.Stdout)
	}
	// report reads the repository it is run in and takes no options, so it is dispatched before the
	// shared flag set, whose only flag is about the fake project this has nothing to do with.
	if command == "report" {
		return runReport(context.Background(), os.Stdout)
	}

	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	source := flags.String("source", "fake", `where the snapshot and watch commands read state from`)

	if err := flags.Parse(args[1:]); err != nil {
		return err
	}

	switch command {
	case "version", "-v", "--version":
		// Returned rather than dropped: this line is the command's entire output, so failing to
		// write it means the command did not do its job.
		_, err := fmt.Fprintln(os.Stdout, versionLine())
		return err
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return nil
	}

	if *source != "fake" {
		return fmt.Errorf("unknown source %q: these commands only read the fake project", *source)
	}

	switch command {
	case "dashboard", "ui", "chat":
		return runChat("")
	case "snapshot":
		return runSnapshot(os.Stdout)
	case "watch":
		return runWatch(os.Stdout)
	case "demo":
		return runDemo(os.Stdout)
	default:
		printUsage(os.Stderr)
		return fmt.Errorf("unknown command %q", command)
	}
}

// isTerminal reports whether f is attached to a terminal.
//
// Checked through the file mode rather than by pulling in a dependency, since the only question
// being asked is whether opening an interactive UI can possibly work.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// printUsage writes the usage text. A failure to print usage is not worth failing the process
// over, and there is nowhere useful left to report it to, so it is dropped explicitly rather than
// left as an unchecked call that only looks deliberate.
func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, usage)
}
