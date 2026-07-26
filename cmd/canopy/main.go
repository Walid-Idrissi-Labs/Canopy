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
	"os"
)

// version is set at build time with -ldflags for release builds.
var version = "dev"

const usage = `canopy - a local verification cockpit for parallel git worktrees

usage:
  canopy snapshot      print the current project snapshot as JSON
  canopy watch         stream events as JSON lines until interrupted
  canopy demo          drive the stale flip and show it happening
  canopy version       print the version

flags:
  -source string       where state comes from (default "fake")

Only the fake source exists so far. Real worktree discovery is P2-01.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "canopy: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stdout, usage)
		return nil
	}

	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	source := flags.String("source", "fake", `where state comes from, only "fake" for now`)

	if err := flags.Parse(args[1:]); err != nil {
		return err
	}

	switch command {
	case "version":
		fmt.Fprintf(os.Stdout, "canopy %s\n", version)
		return nil
	case "help", "-h", "--help":
		fmt.Fprint(os.Stdout, usage)
		return nil
	}

	if *source != "fake" {
		return fmt.Errorf("unknown source %q, only \"fake\" exists so far (real discovery is P2-01)", *source)
	}

	switch command {
	case "snapshot":
		return runSnapshot(os.Stdout)
	case "watch":
		return runWatch(os.Stdout)
	case "demo":
		return runDemo(os.Stdout)
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", command)
	}
}
