package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/trust"
)

// gateProject returns the configuration Canopy may act on.
//
// Everything in canopy.json that runs a program or talks to the model is withheld until the person
// has trusted exactly that configuration. Asked on the terminal before the interface starts, since
// the answer decides whether MCP servers start at all; where there is nobody to ask, the answer is
// no and the warning says how to change it.
func gateProject(dir string, project config.Project, in io.Reader, out io.Writer, interactive bool) config.Project {
	req := trust.Describe(dir, project)
	if req.Empty() {
		return project
	}
	store, err := trust.Open()
	if err != nil {
		fmt.Fprintf(out, "warning: repository trust cannot be read (%v); its commands will not run\n", err)
		return trust.Withhold(project)
	}
	if store.Trusted(req) {
		return project
	}

	if store.Changed(req) {
		fmt.Fprintln(out, "This repository's configuration has changed since you trusted it.")
	}
	fmt.Fprint(out, req.Text())
	if !interactive {
		fmt.Fprintln(out, "warning: not trusted, so none of it runs. Run `canopy trust` here to review and accept it.")
		return trust.Withhold(project)
	}
	fmt.Fprint(out, "Trust this and let Canopy run it? [y/N] ")
	answer, _ := bufio.NewReader(in).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		fmt.Fprintln(out, "Not trusted. Canopy opens without running any of it; `canopy trust` changes that.")
		return trust.Withhold(project)
	}
	if err := store.Grant(req); err != nil {
		fmt.Fprintf(out, "warning: could not record the answer (%v); it applies to this run only\n", err)
	}
	return project
}

// runTrust reviews, grants, revokes or lists repository trust.
func runTrust(args []string, out io.Writer) error {
	store, err := trust.Open()
	if err != nil {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "review":
		project, _, err := config.Load(dir)
		if err != nil {
			return err
		}
		req := trust.Describe(dir, project)
		if req.Empty() {
			fmt.Fprintln(out, "This repository's configuration asks Canopy to run nothing, so there is nothing to trust.")
			return nil
		}
		if store.Trusted(req) {
			fmt.Fprint(out, req.Text())
			fmt.Fprintln(out, "Already trusted, exactly as shown.")
			return nil
		}
		gateProject(dir, project, os.Stdin, out, isTerminal(os.Stdin))
		return nil
	case "revoke":
		return store.Revoke(dir)
	case "list":
		dirs, err := store.List()
		if err != nil {
			return err
		}
		for _, d := range dirs {
			fmt.Fprintln(out, d)
		}
		return nil
	default:
		return errors.New("usage: canopy trust [review|revoke|list]")
	}
}
