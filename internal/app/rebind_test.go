package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ardasevinc/herdr-codex-bridge/internal/herdr"
)

func TestRebindCLIBypassesCallerAssociationAndUsesThreadEnvironment(t *testing.T) {
	server := startRebindServer(t, "", []herdr.Pane{{Agent: "codex", PaneID: "w1:p1"}})
	var stdout bytes.Buffer
	delegated := false
	runtime := Runtime{
		Stdout: &stdout,
		Stderr: &bytes.Buffer{},
		Environ: []string{
			"CODEX_THREAD_ID=tool-thread",
			"HERDR_ENV=1",
			"HERDR_PANE_ID=w9:p9",
		},
		Exec: func(_, _ []string) error {
			delegated = true
			return nil
		},
	}
	if err := runtime.Run(context.Background(), []string{"--socket", server.socket, "rebind", "--pane", "w1:p1", "--apply"}); err != nil {
		t.Fatal(err)
	}
	if delegated {
		t.Fatal("rebind delegated to upstream Herdr")
	}
	if !strings.Contains(stdout.String(), "observed the requested unique mapping") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if server.reportCount() != 1 || server.reportSession() != "tool-thread" {
		t.Fatalf("reports/session = %d/%q", server.reportCount(), server.reportSession())
	}
}

func TestRebindCLIPreviewAndNoOpSendNoReports(t *testing.T) {
	for _, test := range []struct {
		name   string
		panes  []herdr.Pane
		args   []string
		output string
	}{
		{name: "preview", panes: []herdr.Pane{{Agent: "codex", PaneID: "w1:p1"}}, args: []string{"rebind", "--pane", "w1:p1"}, output: "preview; no report sent"},
		{name: "already correct", panes: []herdr.Pane{{Agent: "codex", PaneID: "w1:p1", AgentSession: rebindSession("tool-thread")}}, args: []string{"rebind", "--pane", "w1:p1", "--apply"}, output: "already correct; no report sent"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := startRebindServer(t, "", test.panes)
			var stdout bytes.Buffer
			runtime := Runtime{Stdout: &stdout, Stderr: &bytes.Buffer{}, Environ: []string{"CODEX_THREAD_ID=tool-thread"}}
			args := append([]string{"--socket", server.socket}, test.args...)
			if err := runtime.Run(context.Background(), args); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stdout.String(), test.output) || server.reportCount() != 0 {
				t.Fatalf("stdout/reports = %q/%d", stdout.String(), server.reportCount())
			}
		})
	}
}

func TestRebindCLIRejectsMissingIdentityBeforeRPC(t *testing.T) {
	runtime := Runtime{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Environ: []string{}}
	err := runtime.Run(context.Background(), []string{"--socket", filepath.Join(t.TempDir(), "absent.sock"), "rebind", "--pane", "w1:p1"})
	if err == nil || !strings.Contains(err.Error(), "CODEX_THREAD_ID is unset") {
		t.Fatalf("error = %v", err)
	}
}

func TestRebindCLIUsesCanonicalSocketUnlessExplicitlyOverridden(t *testing.T) {
	configHome, err := os.MkdirTemp("/tmp", "hcb-rebind-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(configHome) })
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HERDR_SOCKET_PATH", filepath.Join(t.TempDir(), "poisoned.sock"))
	canonical := filepath.Join(configHome, "herdr", "herdr.sock")
	if err := os.MkdirAll(filepath.Dir(canonical), 0o700); err != nil {
		t.Fatal(err)
	}
	canonicalServer := startRebindServer(t, canonical, []herdr.Pane{{Agent: "codex", PaneID: "w1:p1"}})
	runtime := Runtime{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Environ: []string{"CODEX_THREAD_ID=thread", "HERDR_SOCKET_PATH=/also-poisoned.sock"}}
	if err := runtime.Run(context.Background(), []string{"rebind", "--pane", "w1:p1"}); err != nil {
		t.Fatal(err)
	}
	if canonicalServer.paneListCount() != 1 {
		t.Fatalf("canonical pane.list calls = %d, want 1", canonicalServer.paneListCount())
	}

	explicitServer := startRebindServer(t, "", []herdr.Pane{{Agent: "codex", PaneID: "w2:p2"}})
	if err := runtime.Run(context.Background(), []string{"rebind", "--pane", "w2:p2", "--socket", explicitServer.socket}); err != nil {
		t.Fatal(err)
	}
	if explicitServer.paneListCount() != 1 {
		t.Fatalf("explicit pane.list calls = %d, want 1", explicitServer.paneListCount())
	}
}

func TestRebindCLIRejectionAndUncertaintyReturnErrors(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*rebindServer)
		want      string
	}{
		{name: "rejected", configure: func(server *rebindServer) { server.decline = true }, want: "declined the manual rebind"},
		{name: "uncertain", configure: func(server *rebindServer) { server.dropReport = true }, want: "outcome is uncertain"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := startRebindServer(t, "", []herdr.Pane{{Agent: "codex", PaneID: "w1:p1"}}, test.configure)
			var stdout bytes.Buffer
			runtime := Runtime{Stdout: &stdout, Stderr: &bytes.Buffer{}, Environ: []string{"CODEX_THREAD_ID=thread"}}
			err := runtime.Run(context.Background(), []string{"--socket", server.socket, "rebind", "--pane", "w1:p1", "--apply"})
			if err == nil || !strings.Contains(err.Error(), test.want) || stdout.Len() != 0 {
				t.Fatalf("error/stdout = %v/%q", err, stdout.String())
			}
		})
	}
}

func TestRebindHelpRequiresNoIdentityOrServer(t *testing.T) {
	var stdout bytes.Buffer
	runtime := Runtime{Stdout: &stdout, Stderr: &bytes.Buffer{}, Environ: []string{}}
	if err := runtime.Run(context.Background(), []string{"rebind", "--help"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Usage: herdr-self rebind") || !strings.Contains(stdout.String(), "CODEX_THREAD_ID") {
		t.Fatalf("help = %q", stdout.String())
	}
}

type rebindServer struct {
	socket     string
	listener   net.Listener
	mu         sync.Mutex
	panes      []herdr.Pane
	reports    []map[string]any
	paneLists  int
	decline    bool
	dropReport bool
}

func startRebindServer(t *testing.T, socket string, panes []herdr.Pane, options ...func(*rebindServer)) *rebindServer {
	t.Helper()
	if socket == "" {
		socket = filepath.Join(os.TempDir(), "hcb-app-rebind-"+time.Now().Format("150405.000000000")+".sock")
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &rebindServer{socket: socket, listener: listener, panes: panes}
	for _, option := range options {
		option(server)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socket)
	})
	go server.serve()
	return server
}

func (server *rebindServer) reportCount() int {
	server.mu.Lock()
	defer server.mu.Unlock()
	return len(server.reports)
}

func (server *rebindServer) reportSession() string {
	server.mu.Lock()
	defer server.mu.Unlock()
	value, _ := server.reports[len(server.reports)-1]["agent_session_id"].(string)
	return value
}

func (server *rebindServer) paneListCount() int {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.paneLists
}

func (server *rebindServer) serve() {
	for {
		connection, err := server.listener.Accept()
		if err != nil {
			return
		}
		go server.handle(connection)
	}
}

func (server *rebindServer) handle(connection net.Conn) {
	defer connection.Close()
	var request struct {
		ID     string         `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if json.NewDecoder(bufio.NewReader(connection)).Decode(&request) != nil {
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	switch request.Method {
	case "pane.list":
		server.paneLists++
		_ = json.NewEncoder(connection).Encode(map[string]any{"id": request.ID, "result": map[string]any{"panes": server.panes}})
	case "pane.report_agent_session":
		server.reports = append(server.reports, request.Params)
		if server.dropReport {
			return
		}
		if server.decline {
			_ = json.NewEncoder(connection).Encode(map[string]any{"id": request.ID, "error": map[string]any{"code": "declined", "message": "injected decline"}})
			return
		}
		paneID, _ := request.Params["pane_id"].(string)
		sessionID, _ := request.Params["agent_session_id"].(string)
		for index := range server.panes {
			if server.panes[index].PaneID == paneID {
				server.panes[index].AgentSession = rebindSession(sessionID)
			}
		}
		_ = json.NewEncoder(connection).Encode(map[string]any{"id": request.ID, "result": map[string]any{}})
	}
}

func rebindSession(sessionID string) *herdr.AgentSession {
	return &herdr.AgentSession{Source: "herdr:codex", Agent: "codex", Kind: "id", Value: sessionID}
}
