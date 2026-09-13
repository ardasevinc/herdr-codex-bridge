package codex

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

func TestParseManualRebind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		prompt     string
		recognized bool
		want       manualRebindRequest
		wantErr    string
	}{
		{name: "ordinary prompt", prompt: "please run herdr-rebind", recognized: false},
		{name: "preview", prompt: "herdr-rebind --pane w3:p17", recognized: true, want: manualRebindRequest{PaneID: "w3:p17"}},
		{name: "apply replacement", prompt: " herdr-rebind --apply --pane w3:p17 --replace ", recognized: true, want: manualRebindRequest{PaneID: "w3:p17", Replace: true, Apply: true}},
		{name: "missing pane", prompt: "herdr-rebind --apply", recognized: true, wantErr: "--pane requires"},
		{name: "relative pane", prompt: "herdr-rebind --pane p17", recognized: true, wantErr: "fully qualified"},
		{name: "unknown argument", prompt: "herdr-rebind --pane w3:p17 --force", recognized: true, wantErr: "unknown argument"},
		{name: "duplicate", prompt: "herdr-rebind --pane w3:p17 --pane w3:p18", recognized: true, wantErr: "only once"},
		{name: "multiline", prompt: "herdr-rebind --pane w3:p17\n--apply", recognized: true, wantErr: "one line"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, recognized, err := parseManualRebind(test.prompt)
			if recognized != test.recognized {
				t.Fatalf("recognized = %t, want %t", recognized, test.recognized)
			}
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, test.wantErr)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("request = %#v, error = %v, want %#v", got, err, test.want)
			}
		})
	}
}

func TestManualRebindBlocksEveryRecognizedOutcome(t *testing.T) {
	tests := []struct {
		name       string
		input      map[string]any
		panes      []herdr.Pane
		wantReason string
	}{
		{name: "malformed", input: manualInput("thread", "herdr-rebind --wat"), wantReason: "unknown argument"},
		{name: "missing payload identity", input: manualInput("", "herdr-rebind --pane w1:p1"), panes: codexPane("w1:p1", nil), wantReason: "has no session_id"},
		{name: "subagent", input: withField(manualInput("thread", "herdr-rebind --pane w1:p1"), "agent_id", "child"), panes: codexPane("w1:p1", nil), wantReason: "subagents cannot"},
		{name: "subagent type only", input: withField(manualInput("thread", "herdr-rebind --pane w1:p1"), "agent_type", "worker"), panes: codexPane("w1:p1", nil), wantReason: "subagents cannot"},
		{name: "missing target", input: manualInput("thread", "herdr-rebind --pane w1:p2"), panes: codexPane("w1:p1", nil), wantReason: "does not exist"},
		{name: "non codex target", input: manualInput("thread", "herdr-rebind --pane w1:p1"), panes: []herdr.Pane{{Agent: "shell", PaneID: "w1:p1"}}, wantReason: "does not contain Codex"},
		{name: "replace required", input: manualInput("thread", "herdr-rebind --pane w1:p1"), panes: codexPane("w1:p1", codexSession("other")), wantReason: "add --replace"},
		{name: "mapped elsewhere", input: manualInput("thread", "herdr-rebind --pane w1:p1 --replace"), panes: append(codexPane("w1:p1", codexSession("other")), codexPane("w1:p2", codexSession("thread"))...), wantReason: "mapped outside"},
		{name: "duplicate requested mapping", input: manualInput("thread", "herdr-rebind --pane w1:p1 --replace"), panes: append(codexPane("w1:p1", codexSession("thread")), codexPane("w1:p2", codexSession("thread"))...), wantReason: "mapped outside"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CODEX_THREAD_ID", "environment-thread-must-not-authorize-manual-rebind")
			server := startManualServer(t, test.panes)
			output := runManualHook(t, server.client(), test.input)
			assertBlockedReason(t, output, test.wantReason)
			if server.reportCount() != 0 {
				t.Fatalf("report calls = %d, want 0", server.reportCount())
			}
		})
	}
}

func TestManualRebindPreviewAndAlreadyCorrectSendNoReport(t *testing.T) {
	tests := []struct {
		name       string
		prompt     string
		panes      []herdr.Pane
		wantReason string
	}{
		{name: "unmapped preview", prompt: "herdr-rebind --pane w1:p1", panes: codexPane("w1:p1", nil), wantReason: "preview; no report sent"},
		{name: "replacement preview", prompt: "herdr-rebind --pane w1:p1 --replace", panes: codexPane("w1:p1", codexSession("other")), wantReason: "rerun with --apply"},
		{name: "already correct apply", prompt: "herdr-rebind --pane w1:p1 --apply", panes: codexPane("w1:p1", codexSession("thread")), wantReason: "already correct; no report sent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := startManualServer(t, test.panes)
			output := runManualHook(t, server.client(), manualInput("thread", test.prompt))
			assertBlockedReason(t, output, test.wantReason)
			if server.reportCount() != 0 {
				t.Fatalf("report calls = %d, want 0", server.reportCount())
			}
		})
	}
}

func TestManualRebindAppliesOneClearReportAndVerifies(t *testing.T) {
	server := startManualServer(t, codexPane("w1:p1", codexSession("old-thread")))
	output := runManualHook(t, server.client(), manualInput("new-thread", "herdr-rebind --pane w1:p1 --replace --apply"))
	assertBlockedReason(t, output, "observed the requested unique mapping")
	if server.reportCount() != 1 {
		t.Fatalf("report calls = %d, want 1", server.reportCount())
	}
	params := server.lastReport()
	for key, want := range map[string]any{
		"pane_id": "w1:p1", "source": "herdr:codex", "agent": "codex",
		"agent_session_id": "new-thread", "session_start_source": "clear",
	} {
		if got := params[key]; got != want {
			t.Fatalf("report %s = %#v, want %#v", key, got, want)
		}
	}
}

func TestManualRebindDistinguishesDeclinedAndUncertainReports(t *testing.T) {
	t.Run("declined", func(t *testing.T) {
		server := startManualServer(t, codexPane("w1:p1", nil), func(server *manualServer) { server.declineReport = true })
		output := runManualHook(t, server.client(), manualInput("thread", "herdr-rebind --pane w1:p1 --apply"))
		assertBlockedReason(t, output, "declined the manual rebind")
		if server.reportCount() != 1 {
			t.Fatalf("report calls = %d, want 1", server.reportCount())
		}
	})
	t.Run("transport uncertain", func(t *testing.T) {
		server := startManualServer(t, codexPane("w1:p1", nil), func(server *manualServer) { server.dropReportResponse = true })
		output := runManualHook(t, server.client(), manualInput("thread", "herdr-rebind --pane w1:p1 --apply"))
		assertBlockedReason(t, output, "outcome is uncertain")
		if server.reportCount() != 1 {
			t.Fatalf("report calls = %d, want 1", server.reportCount())
		}
	})
	t.Run("verification uncertain", func(t *testing.T) {
		server := startManualServer(t, codexPane("w1:p1", nil), func(server *manualServer) { server.failVerification = true })
		output := runManualHook(t, server.client(), manualInput("thread", "herdr-rebind --pane w1:p1 --apply"))
		assertBlockedReason(t, output, "outcome is uncertain")
		if server.reportCount() != 1 {
			t.Fatalf("report calls = %d, want 1", server.reportCount())
		}
	})
}

func TestManualRebindBoundsEveryRPCPhase(t *testing.T) {
	tests := []struct {
		name        string
		configure   func(*manualServer)
		wantReason  string
		wantReports int
	}{
		{name: "precheck", configure: func(server *manualServer) { server.stallPrecheck = true }, wantReason: "rejected", wantReports: 0},
		{name: "report", configure: func(server *manualServer) { server.stallReport = true }, wantReason: "outcome is uncertain", wantReports: 1},
		{name: "verification", configure: func(server *manualServer) { server.stallVerification = true }, wantReason: "outcome is uncertain", wantReports: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := startManualServer(t, codexPane("w1:p1", nil), test.configure)
			startedAt := time.Now()
			output := runManualHook(t, server.client(), manualInput("thread", "herdr-rebind --pane w1:p1 --apply"))
			if elapsed := time.Since(startedAt); elapsed > 2*manualRPCTimeout {
				t.Fatalf("manual rebind took %s, expected one bounded RPC phase", elapsed)
			}
			assertBlockedReason(t, output, test.wantReason)
			if server.reportCount() != test.wantReports {
				t.Fatalf("report calls = %d, want %d", server.reportCount(), test.wantReports)
			}
		})
	}
}

func TestManualRebindRejectsMismatchedPostReportState(t *testing.T) {
	for _, mode := range []string{"none", "missing", "noncodex", "wrong-agent", "wrong-kind", "wrong-value", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			server := startManualServer(t, codexPane("w1:p1", nil), func(server *manualServer) { server.reportMutation = mode })
			output := runManualHook(t, server.client(), manualInput("thread", "herdr-rebind --pane w1:p1 --apply"))
			assertBlockedReason(t, output, "outcome is uncertain")
			if server.reportCount() != 1 {
				t.Fatalf("report calls = %d, want 1", server.reportCount())
			}
		})
	}
}

func TestManualRebindUsesPayloadIdentityDespiteConflictingEnvironment(t *testing.T) {
	t.Setenv("CODEX_THREAD_ID", "wrong-environment-thread")
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w9:p9")
	server := startManualServer(t, codexPane("w1:p1", nil))
	output := runManualHook(t, server.client(), manualInput("payload-thread", "herdr-rebind --pane w1:p1 --apply"))
	assertBlockedReason(t, output, "observed the requested unique mapping")
	if got := server.lastReport()["agent_session_id"]; got != "payload-thread" {
		t.Fatalf("reported session = %#v, want payload-thread", got)
	}
}

func TestMalformedUserPromptPayloadBlocksWithoutRPC(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "invalid prompt type", input: `{"session_id":"thread","hook_event_name":"UserPromptSubmit","prompt":42}`},
		{name: "oversized", input: `{"session_id":"thread","hook_event_name":"UserPromptSubmit","prompt":"herdr-rebind --pane w1:p1 ` + strings.Repeat("x", 1<<20) + `"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := RunHook(context.Background(), "user-prompt-submit", strings.NewReader(test.input), &output, &herdr.Client{}, "/unused", time.Now()); err != nil {
				t.Fatal(err)
			}
			assertBlockedReason(t, output, "malformed UserPromptSubmit")
		})
	}
}

func TestCustomPromptTemplatePreservesEveryArgument(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "codex-prompts", "herdr-rebind.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.TrimSpace(strings.SplitN(string(data), "---", 3)[2])
	if body != "herdr-rebind --pane $ARGUMENTS" {
		t.Fatalf("template body = %q", body)
	}
	for _, test := range []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "preview", args: []string{"w1:p1"}},
		{name: "apply", args: []string{"w1:p1", "--replace", "--apply"}},
		{name: "extra argument retained", args: []string{"w1:p1", "--replace", "--apply", "--invalid"}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			expanded := strings.ReplaceAll(body, "$ARGUMENTS", strings.Join(test.args, " "))
			_, recognized, parseErr := parseManualRebind(expanded)
			if !recognized || (parseErr != nil) != test.wantErr {
				t.Fatalf("expanded = %q, recognized = %t, error = %v", expanded, recognized, parseErr)
			}
		})
	}
}

func TestOrdinaryPromptDoesNotEnterManualRebind(t *testing.T) {
	t.Setenv("HERDR_ENV", "")
	server := startManualServer(t, codexPane("w1:p1", nil))
	output := runManualHook(t, server.client(), manualInput("ordinary-thread", "please explain herdr-rebind --pane w1:p1"))
	if output.Len() != 0 {
		t.Fatalf("ordinary prompt output = %q", output.String())
	}
	if server.reportCount() != 0 {
		t.Fatalf("report calls = %d, want 0", server.reportCount())
	}
}

func manualInput(sessionID, prompt string) map[string]any {
	return map[string]any{"session_id": sessionID, "hook_event_name": "UserPromptSubmit", "prompt": prompt}
}

func withField(input map[string]any, key string, value any) map[string]any {
	input[key] = value
	return input
}

func runManualHook(t *testing.T, client *herdr.Client, input map[string]any) bytes.Buffer {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := RunHook(context.Background(), "user-prompt-submit", bytes.NewReader(encoded), &output, client, "/unused", time.Unix(1_789_000_000, 0)); err != nil {
		t.Fatal(err)
	}
	return output
}

func assertBlockedReason(t *testing.T, output bytes.Buffer, substring string) {
	t.Helper()
	var got hookOutput
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatalf("decode hook output %q: %v", output.String(), err)
	}
	if got.Decision != "block" || got.Reason == "" || !strings.Contains(got.Reason, substring) {
		t.Fatalf("hook output = %#v, want blocked reason containing %q", got, substring)
	}
}

func codexPane(paneID string, session *herdr.AgentSession) []herdr.Pane {
	return []herdr.Pane{{Agent: "codex", PaneID: paneID, AgentSession: session}}
}

func codexSession(value string) *herdr.AgentSession {
	return &herdr.AgentSession{Source: "herdr:codex", Agent: "codex", Kind: "id", Value: value}
}

type manualServer struct {
	testingT           *testing.T
	listener           net.Listener
	mu                 sync.Mutex
	panes              []herdr.Pane
	reports            []map[string]any
	declineReport      bool
	dropReportResponse bool
	failVerification   bool
	stallPrecheck      bool
	stallReport        bool
	stallVerification  bool
	reportMutation     string
	paneListCalls      int
}

func startManualServer(t *testing.T, panes []herdr.Pane, options ...func(*manualServer)) *manualServer {
	t.Helper()
	socketPath := filepath.Join(os.TempDir(), "hcb-"+time.Now().Format("150405.000000000")+".sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &manualServer{testingT: t, listener: listener, panes: panes}
	for _, option := range options {
		option(server)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})
	go server.serve()
	return server
}

func (s *manualServer) client() *herdr.Client {
	return &herdr.Client{SocketPath: s.listener.Addr().String()}
}

func (s *manualServer) reportCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reports)
}

func (s *manualServer) lastReport() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reports[len(s.reports)-1]
}

func (s *manualServer) serve() {
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(connection)
	}
}

func (s *manualServer) handle(connection net.Conn) {
	defer connection.Close()
	var request struct {
		ID     string         `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if json.NewDecoder(bufio.NewReader(connection)).Decode(&request) != nil {
		return
	}
	switch request.Method {
	case "pane.list":
		s.mu.Lock()
		s.paneListCalls++
		paneListCall := s.paneListCalls
		stall := paneListCall == 1 && s.stallPrecheck || paneListCall > 1 && s.stallVerification
		fail := s.failVerification && len(s.reports) > 0
		panes := append([]herdr.Pane(nil), s.panes...)
		s.mu.Unlock()
		if stall {
			time.Sleep(2 * manualRPCTimeout)
			return
		}
		if fail {
			_ = json.NewEncoder(connection).Encode(map[string]any{"id": request.ID, "error": map[string]any{"code": "verify_failed", "message": "injected verification failure"}})
			return
		}
		_ = json.NewEncoder(connection).Encode(map[string]any{"id": request.ID, "result": map[string]any{"panes": panes}})
	case "pane.report_agent_session":
		s.mu.Lock()
		s.reports = append(s.reports, request.Params)
		stall := s.stallReport
		drop := s.dropReportResponse
		decline := s.declineReport
		mutation := s.reportMutation
		if !decline {
			s.applyReport(request.Params, mutation)
		}
		s.mu.Unlock()
		if stall {
			time.Sleep(2 * manualRPCTimeout)
			return
		}
		if drop {
			return
		}
		if decline {
			_ = json.NewEncoder(connection).Encode(map[string]any{"id": request.ID, "error": map[string]any{"code": "report_declined", "message": "injected decline"}})
			return
		}
		_ = json.NewEncoder(connection).Encode(map[string]any{"id": request.ID, "result": map[string]any{}})
	}
}

func (s *manualServer) applyReport(params map[string]any, mutation string) {
	paneID, _ := params["pane_id"].(string)
	sessionID, _ := params["agent_session_id"].(string)
	if mutation == "none" {
		return
	}
	for index := range s.panes {
		if s.panes[index].PaneID != paneID {
			continue
		}
		switch mutation {
		case "missing":
			s.panes = append(s.panes[:index], s.panes[index+1:]...)
			return
		case "noncodex":
			s.panes[index].Agent = "shell"
			s.panes[index].AgentSession = nil
		case "wrong-agent":
			s.panes[index].AgentSession = &herdr.AgentSession{Source: "herdr:codex", Agent: "claude", Kind: "id", Value: sessionID}
		case "wrong-kind":
			s.panes[index].AgentSession = &herdr.AgentSession{Source: "herdr:codex", Agent: "codex", Kind: "name", Value: sessionID}
		case "wrong-value":
			s.panes[index].AgentSession = codexSession("different-thread")
		default:
			s.panes[index].AgentSession = codexSession(sessionID)
		}
	}
	if mutation == "duplicate" {
		s.panes = append(s.panes, herdr.Pane{Agent: "codex", PaneID: "w1:p2", AgentSession: codexSession(sessionID)})
	}
}
