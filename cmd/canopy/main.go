// Command canopy is the entry point for the Canopy verification cockpit.
//
// Until the dashboard lands in P1-07 this is a headless harness. It exists so the engine can be
// driven and inspected without a terminal UI in the way, which matters for two reasons: the
// engine has to be testable on its own, and when the dashboard eventually shows something
// surprising, there needs to be a way to ask the engine directly what it thinks.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

// version is set at build time with -ldflags for release builds.
var version = "dev"

const usage = `canopy - a local verification cockpit for parallel git worktrees

usage:
  canopy               open the dashboard
  canopy keys          manage provider credentials
  canopy ask           send one message to a provider and stream the reply
  canopy snapshot      print the current project snapshot as JSON
  canopy watch         stream events as JSON lines until interrupted
  canopy demo          drive the stale flip and show it happening
  canopy version       print the version

flags:
  -source string       where state comes from (default "fake")

Only the fake source exists so far. Real worktree discovery is A5-01.
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
		return runDashboard()
	}

	command := args[0]

	// keys owns its own subcommands and flags, so it is dispatched before the shared flag set
	// gets a chance to reject them.
	if command == "keys" {
		return runKeys(args[1:], os.Stdout)
	}
	// ask owns its own flags too, so it is dispatched before the shared flag set sees them.
	if command == "ask" {
		return runAsk(args[1:], os.Stdout)
	}

	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	source := flags.String("source", "fake", `where state comes from, only "fake" for now`)

	if err := flags.Parse(args[1:]); err != nil {
		return err
	}

	switch command {
	case "version":
		// Returned rather than dropped: this line is the command's entire output, so failing to
		// write it means the command did not do its job.
		_, err := fmt.Fprintf(os.Stdout, "canopy %s\n", version)
		return err
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return nil
	}

	if *source != "fake" {
		return fmt.Errorf("unknown source %q, only \"fake\" exists so far (real discovery is A5-01)", *source)
	}

	switch command {
	case "dashboard", "ui":
		return runDashboard()
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
