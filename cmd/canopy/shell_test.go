package main

import (
	"context"
	"strings"
	"testing"
)

// A ! command runs like the person's own terminal, minus the provider keys Canopy holds.
func TestABangCommandRunsWithoutProviderKeys(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-should-not-be-seen")
	t.Setenv("SHELL", "/bin/sh")
	result := shellIn(t.TempDir())(context.Background(), `echo "key=[$ANTHROPIC_API_KEY]"; pwd; exit 3`)
	if result.Failed != "" || result.ExitCode != 3 || !strings.Contains(result.Output, "key=[]") {
		t.Fatalf("%+v", result)
	}
}
