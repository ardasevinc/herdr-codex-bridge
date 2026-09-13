package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ardasevinc/herdr-codex-bridge/internal/bridge"
	"github.com/ardasevinc/herdr-codex-bridge/internal/herdr"
)

func TestSafeWithoutCaller(t *testing.T) {
	tests := []struct {
		args []string
		safe bool
	}{
		{[]string{"status"}, true},
		{[]string{"agent", "list"}, true},
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

func TestSafeWithoutCallerRegistryMatchesIndependentContract(t *testing.T) {
	expected := []safeCommandGroup{
		{command: "status"},
		{command: "-V"},
		{command: "--default-config"},
		{command: "workspace", subcommands: []string{"list", "get"}},
		{command: "tab", subcommands: []string{"list", "get"}},
		{command: "pane", subcommands: []string{"list", "get", "layout", "process-info", "neighbor", "edges", "read", "wait-output"}},
		{command: "agent", subcommands: []string{"list", "get", "read", "wait", "explain"}},
		{command: "plugin", subcommands: []string{"list", "config-dir", "log", "logs"}},
		{command: "session", subcommands: []string{"list"}},
		{command: "integration", subcommands: []string{"status"}},
	}
	if !reflect.DeepEqual(safeWithoutAssociation, expected) {
		t.Fatalf("safe command registry changed:\n got: %#v\nwant: %#v", safeWithoutAssociation, expected)
	}
}

func TestBridgeHelpDocumentsEnforcedSafeCommands(t *testing.T) {
	help := bridgeHelp()
	for _, group := range safeWithoutAssociation {
		if !strings.Contains(help, group.command+":") {
			t.Fatalf("help omits safe command group %q", group.command)
		}
		for _, subcommand := range group.subcommands {
			if !strings.Contains(help, subcommand) {
				t.Fatalf("help omits safe command %s %s", group.command, subcommand)
			}
		}
	}
	for _, wanted := range []string{
		"herdr-self --json",
		"herdr-self doctor --json",
		"specifically reports an unmapped or ambiguous thread",
		"delegated without caller context",
		"All other delegated operations fail closed",
	} {
		if !strings.Contains(help, wanted) {
			t.Fatalf("help omits %q", wanted)
		}
	}
}

func TestCombinedSkillIsOneAuthoritativeDocument(t *testing.T) {
	const upstream = "---\r\nname: herdr\rdescription: upstream\n---\n\n# Herdr\r\n\r\n```sh\rtest x\r```"
	installFakeHerdr(t, "#!/bin/sh\nprintf '%s' '"+upstream+"'\n")
	var stdout bytes.Buffer
	runtime := Runtime{Stdout: &stdout, Stderr: &bytes.Buffer{}}
	if err := runtime.Run(context.Background(), []string{"--skill"}); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if !strings.HasPrefix(output, "---\nname: herdr-self\n") {
		t.Fatalf("combined skill does not start with bridge frontmatter: %q", output)
	}
	for _, wanted := range []string{
		"authoritative adaptation",
		"supersedes the bare upstream Herdr rule",
		"use `herdr-self` in place of `herdr`",
		"> ---\n> name: herdr\n",
		"> ```sh\n> test x\n> ```\n",
	} {
		if !strings.Contains(output, wanted) {
			t.Fatalf("combined skill omits %q", wanted)
		}
	}
	if strings.Contains(output, "\n---\nname: herdr\n") {
		t.Fatal("upstream skill was emitted as a peer document")
	}
	quoted := quotedReference(t, output)
	want := strings.ReplaceAll(strings.ReplaceAll(upstream, "\r\n", "\n"), "\r", "\n") + "\n"
	if quoted != want {
		t.Fatalf("quoted reference changed upstream content:\n got: %q\nwant: %q", quoted, want)
	}
}

func TestCombinedDocumentationFailureEmitsNoPartialOutput(t *testing.T) {
	installFakeHerdr(t, "#!/bin/sh\nprintf 'partial upstream stdout'\nprintf '%s\\n' boom >&2\nexit 7\n")
	for _, arg := range []string{"--help", "--skill"} {
		var stdout, stderr bytes.Buffer
		runtime := Runtime{Stdout: &stdout, Stderr: &stderr}
		err := runtime.Run(context.Background(), []string{arg})
		if err == nil || !strings.Contains(err.Error(), "exit status 7") {
			t.Fatalf("Run(%s) error = %v", arg, err)
		}
		if stdout.Len() != 0 {
			t.Fatalf("Run(%s) emitted partial stdout: %q", arg, stdout.String())
		}
		if !strings.Contains(stderr.String(), "boom") {
			t.Fatalf("Run(%s) did not propagate upstream stderr", arg)
		}
	}
}

func TestCombinedDocumentationEnforcesExactOutputLimit(t *testing.T) {
	for _, size := range []int{maxUpstreamDocumentationBytes, maxUpstreamDocumentationBytes + 1} {
		for _, arg := range []string{"--help", "--skill"} {
			t.Run(fmt.Sprintf("%s/%d", arg, size), func(t *testing.T) {
				installFakeHerdr(t, fmt.Sprintf("#!/bin/sh\nyes x | head -c %d\n", size))
				var stdout bytes.Buffer
				runtime := Runtime{Stdout: &stdout, Stderr: &bytes.Buffer{}}
				err := runtime.Run(context.Background(), []string{arg})
				if size == maxUpstreamDocumentationBytes {
					if err != nil {
						t.Fatalf("exact-limit output failed: %v", err)
					}
					if stdout.Len() <= size {
						t.Fatalf("combined output omitted framing: %d bytes", stdout.Len())
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), "upstream documentation exceeds") {
					t.Fatalf("oversized output error = %v", err)
				}
				if stdout.Len() != 0 {
					t.Fatalf("oversized upstream output leaked partial document: %d bytes", stdout.Len())
				}
			})
		}
	}
}

func TestCombinedHelpIncludesUpstreamAfterBridgeContract(t *testing.T) {
	installFakeHerdr(t, "#!/bin/sh\nprintf 'upstream help'\n")
	var stdout bytes.Buffer
	runtime := Runtime{Stdout: &stdout, Stderr: &bytes.Buffer{}}
	if err := runtime.Run(context.Background(), []string{"--help"}); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if bridgeIndex, upstreamIndex := strings.Index(output, "Herdr Codex Bridge"), strings.Index(output, "upstream help"); bridgeIndex < 0 || upstreamIndex <= bridgeIndex {
		t.Fatalf("unexpected combined help ordering: %q", output)
	}
}

func TestSelfJSONUsesCentralizedAssociationLookup(t *testing.T) {
	socket := serveAgents(t, singleAgent())
	var stdout bytes.Buffer
	runtime := Runtime{
		Stdout:  &stdout,
		Environ: []string{"CODEX_THREAD_ID=thread"},
	}
	if err := runtime.Run(context.Background(), []string{"--socket", socket, "--json"}); err != nil {
		t.Fatal(err)
	}
	var association bridge.Association
	if err := json.Unmarshal(stdout.Bytes(), &association); err != nil {
		t.Fatal(err)
	}
	if association.ThreadID != "thread" || association.PaneID != "w1:p3" || association.Source != "herdr:codex" {
		t.Fatalf("unexpected association: %#v", association)
	}
}

func TestDelegateInjectsMappedCentralizedCallerContext(t *testing.T) {
	socket := serveAgents(t, singleAgent())
	var capturedArgs, capturedEnv []string
	runtime := Runtime{
		Environ: []string{"CODEX_THREAD_ID=thread", "UNCHANGED=yes"},
		Exec: func(args []string, env []string) error {
			capturedArgs = append([]string(nil), args...)
			capturedEnv = append([]string(nil), env...)
			return nil
		},
	}
	wantArgs := []string{"agent", "read", "w1:p3"}
	if err := runtime.delegate(context.Background(), &herdr.Client{SocketPath: socket}, wantArgs, socket); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(capturedArgs, wantArgs) {
		t.Fatalf("delegated args = %q, want %q", capturedArgs, wantArgs)
	}
	for name, wanted := range map[string]string{
		"HERDR_ENV":          "1",
		"HERDR_WORKSPACE_ID": "w1",
		"HERDR_TAB_ID":       "w1:t2",
		"HERDR_PANE_ID":      "w1:p3",
		"HERDR_SOCKET_PATH":  socket,
		"UNCHANGED":          "yes",
	} {
		if got := envValue(capturedEnv, name); got != wanted {
			t.Fatalf("%s = %q, want %q", name, got, wanted)
		}
	}
}

func TestDelegateWithoutAssociationPreservesSafeBoundary(t *testing.T) {
	for _, test := range []struct {
		name     string
		agents   []map[string]any
		args     []string
		wantExec bool
		wantErr  error
	}{
		{name: "unmapped read", args: []string{"agent", "list"}, wantExec: true, wantErr: bridge.ErrUnmapped},
		{name: "unmapped mutation", args: []string{"pane", "close", "w1:p1"}, wantErr: bridge.ErrUnmapped},
		{name: "ambiguous read", agents: duplicateAgents(), args: []string{"pane", "list"}, wantExec: true, wantErr: bridge.ErrAmbiguous},
		{name: "ambiguous mutation", agents: duplicateAgents(), args: []string{"agent", "prompt", "w1:p1", "hello"}, wantErr: bridge.ErrAmbiguous},
	} {
		t.Run(test.name, func(t *testing.T) {
			socket := serveAgents(t, test.agents)
			executed := false
			var stderr bytes.Buffer
			runtime := Runtime{
				Environ: []string{"CODEX_THREAD_ID=thread"},
				Stderr:  &stderr,
				Exec: func(_ []string, env []string) error {
					executed = true
					if envValue(env, "HERDR_ENV") != "" {
						t.Fatal("unmapped delegation injected caller context")
					}
					return nil
				},
			}
			err := runtime.delegate(context.Background(), &herdr.Client{SocketPath: socket}, test.args, socket)
			if executed != test.wantExec {
				t.Fatalf("executed = %t, want %t", executed, test.wantExec)
			}
			if test.wantExec && (err != nil || !strings.Contains(stderr.String(), "delegating this read-only command without caller context")) {
				t.Fatalf("safe delegation error/stderr = %v / %q", err, stderr.String())
			}
			if test.wantExec && !strings.Contains(stderr.String(), test.wantErr.Error()) {
				t.Fatalf("safe delegation warning lost typed reason: %q", stderr.String())
			}
			if !test.wantExec && (err == nil || !errors.Is(err, test.wantErr) || !strings.Contains(err.Error(), "refusing command without a proven caller pane")) {
				t.Fatalf("unsafe delegation error = %v", err)
			}
		})
	}
}

func installFakeHerdr(t *testing.T, script string) {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "herdr")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func duplicateAgents() []map[string]any {
	return []map[string]any{
		{"agent": "codex", "workspace_id": "w1", "tab_id": "w1:t1", "pane_id": "w1:p1", "agent_session": map[string]any{"source": "herdr:codex", "agent": "codex", "kind": "id", "value": "thread"}},
		{"agent": "codex", "workspace_id": "w2", "tab_id": "w2:t2", "pane_id": "w2:p2", "agent_session": map[string]any{"source": "herdr:codex", "agent": "codex", "kind": "id", "value": "thread"}},
	}
}

func singleAgent() []map[string]any {
	return []map[string]any{{
		"agent": "codex", "workspace_id": "w1", "tab_id": "w1:t2", "pane_id": "w1:p3",
		"agent_session": map[string]any{"source": "herdr:codex", "agent": "codex", "kind": "id", "value": "thread"},
	}}
}

func quotedReference(t *testing.T, output string) string {
	t.Helper()
	marker := "\n> "
	start := strings.Index(output, marker)
	if start < 0 {
		t.Fatal("combined skill has no quoted reference")
	}
	lines := strings.Split(strings.TrimSuffix(output[start+1:], "\n"), "\n")
	for index, line := range lines {
		if !strings.HasPrefix(line, "> ") {
			t.Fatalf("reference line %d escaped blockquote: %q", index+1, line)
		}
		lines[index] = strings.TrimPrefix(line, "> ")
	}
	return strings.Join(lines, "\n") + "\n"
}

func serveAgents(t *testing.T, agents []map[string]any) string {
	t.Helper()
	socket := filepath.Join(os.TempDir(), fmt.Sprintf("hcb-app-%d-%d.sock", os.Getpid(), time.Now().UnixNano()))
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socket)
	})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		var request map[string]any
		_ = json.NewDecoder(bufio.NewReader(connection)).Decode(&request)
		_ = json.NewEncoder(connection).Encode(map[string]any{
			"id":     request["id"],
			"result": map[string]any{"agents": agents},
		})
	}()
	return socket
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
