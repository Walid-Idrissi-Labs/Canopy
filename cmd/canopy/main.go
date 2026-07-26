// Command canopy is the entry point for the Canopy verification cockpit.
//
// This is still a skeleton. The headless snapshot harness lands in P1-06 and the dashboard in
// P1-07. See TASKS.md.
package main

import (
	"fmt"
	"os"
)

// version is set at build time with -ldflags for release builds.
var version = "dev"

func main() {
	fmt.Fprintf(os.Stdout, "canopy %s\n", version)
	fmt.Fprintln(os.Stdout, "Not implemented yet. See TASKS.md for the current phase.")
}
