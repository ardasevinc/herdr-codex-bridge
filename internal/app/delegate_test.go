package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestSafeWithoutCaller(t *testing.T) {
	tests := []struct {
		args []string
		safe bool
	}{
		{[]string{"status"}, true},
		{[]string{"agent", "list", "--json"}, true},
		{[]string{"pane", "read", "w1:p1"}, true},
		{[]string{"pane", "zoom", "--on"}, false},
		{[]string{"pane", "layout"}, true},
		{[]string{"pane", "send-text", "w1:p1", "hello"}, false},
		{[]string{"workspace", "close", "w1"}, false},
		{[]string{"api", "call", "pane.close"}, false},
	}
	for _, test := range tests {
		if got := safeWithoutCaller(test.args); got != test.safe {
			t.Errorf("safeWithoutCaller(%q) = %t, want %t", test.args, got, test.safe)
		}
	}
}

func TestInstallSubcommandHelp(t *testing.T) {
	for _, args := range [][]string{
		{"setup", "--help"},
		{"setup", "codex", "-h"},
		{"teardown", "--help"},
		{"teardown", "codex", "--help"},
	} {
		var stdout bytes.Buffer
		runtime := Runtime{Stdout: &stdout, Stderr: &bytes.Buffer{}}
		if err := runtime.Run(context.Background(), args); err != nil {
			t.Fatalf("Run(%q): %v", args, err)
		}
		output := stdout.String()
		if !strings.Contains(output, "Usage: herdr-self "+args[0]+" codex") || !strings.Contains(output, "--apply") || !strings.Contains(output, "--force") {
			t.Fatalf("Run(%q) help = %q", args, output)
		}
	}
}
