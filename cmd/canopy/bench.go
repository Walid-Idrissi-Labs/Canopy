package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/bench"
)

// runBench measures Canopy on the built-in tasks: each is laid out fresh, attempted by a headless
// run of this same binary, and scored by its own check. Every attempt calls a real model and costs
// money, so nothing here runs unless somebody types it.
func runBench(args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("bench", flag.ContinueOnError)
	flags.SetOutput(errOut)
	keyName := flags.String("key", "", "the named key to use; the default key when omitted")
	model := flags.String("model", "", "the model; the key's default when omitted")
	effort := flags.String("effort", "", "the effort for every attempt")
	only := flags.String("tasks", "", "comma-separated task names; all of them when omitted")
	list := flags.Bool("list", false, "list the tasks and exit")
	save := flags.String("out", "", "write the report as JSON to this file")
	against := flags.String("compare", "", "compare with an earlier JSON report")
	timeout := flags.Duration("timeout", 10*time.Minute, "the limit for each attempt")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	tasks, err := bench.Tasks()
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return exitFailed
	}
	if *list {
		for _, t := range tasks {
			_, _ = fmt.Fprintf(out, "%-20s %-10s %s\n", t.Name, t.Kind, t.Prompt)
		}
		return exitOK
	}
	var names []string
	if *only != "" {
		names = strings.Split(*only, ",")
	}
	if tasks, err = bench.Select(tasks, names); err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return exitUsage
	}
	var before *bench.Report
	if *against != "" {
		data, err := os.ReadFile(*against)
		if err != nil {
			_, _ = fmt.Fprintln(errOut, err)
			return exitUsage
		}
		before = &bench.Report{}
		if err := json.Unmarshal(data, before); err != nil {
			_, _ = fmt.Fprintf(errOut, "%s is not a bench report: %v\n", *against, err)
			return exitUsage
		}
	}
	self, err := os.Executable()
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return exitFailed
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	attempt := func(ctx context.Context, task bench.Task, dir string) (bench.Usage, error) {
		runArgs := []string{"run", "-p", task.Prompt, "-mode", "runway", "-yes", "-output", "json",
			"-timeout", timeout.String()}
		for flagName, value := range map[string]string{"-key": *keyName, "-model": *model, "-effort": *effort} {
			if value != "" {
				runArgs = append(runArgs, flagName, value)
			}
		}
		cmd := exec.CommandContext(ctx, self, runArgs...)
		cmd.Dir = dir
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		runErr := cmd.Run()
		usage, ok := lastResult(stdout.Bytes())
		if !ok && runErr != nil {
			return usage, fmt.Errorf("%v: %s", runErr, strings.TrimSpace(stderr.String()))
		}
		return usage, nil
	}

	_, _ = fmt.Fprintf(errOut, "running %d tasks; each one calls the model and is billed\n", len(tasks))
	report := bench.Report{At: time.Now().UTC(), Key: *keyName, Model: *model, Effort: *effort, Version: version}
	report.Results = bench.Run(ctx, tasks, attempt, func(r bench.Result) {
		status := "pass"
		switch {
		case r.Skipped != "":
			status = "skipped"
		case !r.Passed:
			status = "FAIL"
		}
		_, _ = fmt.Fprintf(errOut, "%-8s %-20s %6.1fs  $%.4f  %s\n", status, r.Task, r.Seconds, r.CostUSD, r.Error)
	})
	_, _ = fmt.Fprint(out, report.Markdown())
	if before != nil {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprint(out, bench.Compare(*before, report))
	}
	if *save != "" {
		data, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(*save, append(data, '\n'), 0o644); err != nil {
			_, _ = fmt.Fprintln(errOut, err)
			return exitFailed
		}
	}
	if passed, ran, _, _ := report.Totals(); passed < ran {
		return exitFailed
	}
	return exitOK
}

// lastResult reads the result line a headless JSON run ends with.
func lastResult(output []byte) (bench.Usage, bool) {
	var usage bench.Usage
	found := false
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var line struct {
			Type      string      `json:"type"`
			Usage     bench.Usage `json:"usage"`
			CostUSD   float64     `json:"cost_usd"`
			ToolCalls int         `json:"tool_calls"`
		}
		if json.Unmarshal(scanner.Bytes(), &line) != nil || line.Type != "result" {
			continue
		}
		usage = line.Usage
		usage.CostUSD, usage.ToolCalls = line.CostUSD, line.ToolCalls
		found = true
	}
	return usage, found
}
