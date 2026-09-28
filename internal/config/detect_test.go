package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDetectTestsReadsTheBuildFiles(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  [][]string
	}{
		{"go", map[string]string{"go.mod": "module x"},
			[][]string{{"go", "build", "./..."}, {"go", "test", "./..."}, {"go", "vet", "./..."}}},
		{"rust", map[string]string{"Cargo.toml": "[package]"}, [][]string{{"cargo", "test"}}},
		{"npm", map[string]string{"package.json": `{"scripts": {"test": "vitest run"}}`}, [][]string{{"npm", "run", "test"}}},
		{"pnpm", map[string]string{"package.json": `{"scripts": {"test": "jest"}}`, "pnpm-lock.yaml": ""},
			[][]string{{"pnpm", "run", "test"}}},
		{"yarn", map[string]string{"package.json": `{"scripts": {"test": "jest"}}`, "yarn.lock": ""},
			[][]string{{"yarn", "run", "test"}}},
		{"bun runs the script, not its own runner", map[string]string{"package.json": `{"scripts": {"test": "vitest run"}}`, "bun.lockb": ""},
			[][]string{{"bun", "run", "test"}}},
		{"pytest.ini", map[string]string{"pytest.ini": "[pytest]"}, [][]string{{"python3", "-m", "pytest"}}},
		{"npm placeholder", map[string]string{"package.json": `{"scripts": {"test": "echo \"Error: no test specified\" && exit 1"}}`}, nil},
		{"no test script", map[string]string{"package.json": `{"scripts": {"build": "tsc"}}`}, nil},
		{"pytest in pyproject", map[string]string{"pyproject.toml": "[tool.pytest.ini_options]"},
			[][]string{{"python3", "-m", "pytest"}}},
		{"python without pytest", map[string]string{"pyproject.toml": "[project]"}, nil},
		{"nothing", map[string]string{"README.md": "hi"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var got [][]string
			for _, test := range DetectTests(dir) {
				got = append(got, test.Command.Argv)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("detected %v, want %v", got, tc.want)
			}
		})
	}
}

// A Python project with a tests directory is taken to use pytest even when no file names it.
func TestATestsDirectoryMeansPytest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "setup.cfg"), []byte("[metadata]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DetectTests(dir); len(got) != 1 || got[0].Name != "pytest" {
		t.Fatalf("detected %+v", got)
	}
}

// A project with its own virtual environment is tested in it.
func TestAVirtualEnvironmentIsUsed(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".venv", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"pytest.ini": "[pytest]", filepath.Join(".venv", "bin", "python"): ""} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if got := DetectTests(dir); len(got) != 1 || got[0].Command.Argv[0] != filepath.Join(".venv", "bin", "python") {
		t.Fatalf("detected %+v", got)
	}
}
