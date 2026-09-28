package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// DetectTests proposes the tests a project most likely runs, from the build files at its root. A
// proposal and nothing more: it is written into a canopy.json a person reads before trusting, and
// nothing detected here runs until they have.
//
// Only what the file itself says. A package.json with no test script, or with the placeholder npm
// writes, has no test to propose, and guessing one would put a command in front of a person that
// the project never asked for.
func DetectTests(dir string) []Test {
	var tests []Test
	exists := func(name string) bool {
		info, err := os.Stat(filepath.Join(dir, name))
		return err == nil && !info.IsDir()
	}
	argv := func(name string, required bool, timeout string, args ...string) Test {
		return Test{Name: name, Command: TestCommand{Argv: args}, Required: required, Timeout: timeout}
	}

	if exists("go.mod") {
		tests = append(tests,
			argv("build", true, "5m", "go", "build", "./..."),
			argv("unit", true, "15m", "go", "test", "./..."),
			argv("vet", false, "5m", "go", "vet", "./..."))
	}
	if exists("Cargo.toml") {
		tests = append(tests, argv("cargo test", true, "20m", "cargo", "test"))
	}
	if exists("package.json") {
		if runner := nodeRunner(dir, exists); runner != "" && hasTestScript(filepath.Join(dir, "package.json")) {
			// "run test", so every runner runs the script the project wrote: bare `bun test` is bun's
			// own test runner, which ignores the script entirely.
			tests = append(tests, argv(runner+" test", true, "15m", runner, "run", "test"))
		}
	}
	if exists("pyproject.toml") || exists("pytest.ini") || exists("setup.cfg") || exists("tox.ini") {
		if usesPytest(dir, exists) {
			// The project's own environment where it has one; python3 otherwise, since a bare
			// `python` is missing on stock macOS and Debian.
			python := "python3"
			if exists(filepath.Join(".venv", "bin", "python")) {
				python = filepath.Join(".venv", "bin", "python")
			}
			tests = append(tests, argv("pytest", true, "15m", python, "-m", "pytest"))
		}
	}
	return tests
}

// nodeRunner is the package manager the lockfile says the project uses.
func nodeRunner(dir string, exists func(string) bool) string {
	switch {
	case exists("pnpm-lock.yaml"):
		return "pnpm"
	case exists("yarn.lock"):
		return "yarn"
	case exists("bun.lockb") || exists("bun.lock"):
		return "bun"
	default:
		return "npm"
	}
}

// hasTestScript reports whether package.json has a test script other than npm's placeholder.
func hasTestScript(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return false
	}
	script := strings.TrimSpace(pkg.Scripts["test"])
	return script != "" && !strings.Contains(script, "no test specified")
}

// usesPytest reports whether the Python project's configuration mentions pytest, or it has a tests
// directory pytest would find.
func usesPytest(dir string, exists func(string) bool) bool {
	if exists("pytest.ini") {
		return true
	}
	for _, name := range []string{"pyproject.toml", "setup.cfg", "tox.ini"} {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil && strings.Contains(string(data), "pytest") {
			return true
		}
	}
	info, err := os.Stat(filepath.Join(dir, "tests"))
	return err == nil && info.IsDir()
}
