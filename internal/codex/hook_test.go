package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ardasevinc/herdr-codex-bridge/internal/bridge"
	"github.com/ardasevinc/herdr-codex-bridge/internal/herdr"
	"github.com/ardasevinc/herdr-codex-bridge/internal/protocol"
)

func TestSessionStartEmitsSignedRendezvousWithoutNativeEnv(t *testing.T) {
	t.Setenv("HERDR_ENV", "")
	t.Setenv("HERDR_PANE_ID", "")
	key := bytes.Repeat([]byte{0x51}, 32)
	keyPath := filepath.Join(t.TempDir(), "bridge.key")
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_788_210_000, 0).UTC()
	input := `{"session_id":"01a05858-28e3-77d1-85ab-882a13125206","hook_event_name":"SessionStart","source":"resume"}`
	var output bytes.Buffer
	err := RunHook(context.Background(), "session-start", strings.NewReader(input), &output, &herdr.Client{SocketPath: "/unused"}, keyPath, now)
	if err != nil {
		t.Fatal(err)
	}
	var got hookOutput
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	marker, err := protocol.ParseAndVerify(got.SystemMessage, key, now)
	if err != nil {
		t.Fatal(err)
	}
	if marker.SessionID != "01a05858-28e3-77d1-85ab-882a13125206" || marker.Source != "resume" {
		t.Fatalf("unexpected marker: %#v", marker)
	}
	if !strings.Contains(got.HookSpecificOutput.AdditionalContext, "centralized Codex app-server") {
		t.Fatalf("missing app-server guidance: %s", got.HookSpecificOutput.AdditionalContext)
	}
}

func TestCompactNeverEmitsVisibleMarkerWhenUnmapped(t *testing.T) {
	t.Setenv("HERDR_ENV", "")
	input := `{"session_id":"thread","hook_event_name":"SessionStart","source":"compact"}`
	var output bytes.Buffer
	err := RunHook(context.Background(), "session-start", strings.NewReader(input), &output, &herdr.Client{SocketPath: filepath.Join(t.TempDir(), "missing.sock")}, "/unused", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var got hookOutput
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.SystemMessage != "" {
		t.Fatalf("compact emitted visible marker: %q", got.SystemMessage)
	}
}

func TestUserPromptIsQuietWhenThreadHasNoPaneWitness(t *testing.T) {
	t.Setenv("HERDR_ENV", "")
	key := bytes.Repeat([]byte{0x61}, 32)
	keyPath := filepath.Join(t.TempDir(), "bridge.key")
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var sessionOutput bytes.Buffer
	sessionInput := `{"session_id":"new-thread","hook_event_name":"SessionStart","source":"startup"}`
	if err := RunHook(context.Background(), "session-start", strings.NewReader(sessionInput), &sessionOutput, &herdr.Client{SocketPath: "/unused"}, keyPath, now); err != nil {
		t.Fatal(err)
	}
	input := `{"session_id":"new-thread","hook_event_name":"UserPromptSubmit"}`
	var output bytes.Buffer
	err := RunHook(context.Background(), "user-prompt-submit", strings.NewReader(input), &output, &herdr.Client{SocketPath: filepath.Join(t.TempDir(), "missing.sock")}, keyPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("unwitnessed prompt hook emitted output: %q", output.String())
	}
	var repeat bytes.Buffer
	if err := RunHook(context.Background(), "user-prompt-submit", strings.NewReader(input), &repeat, &herdr.Client{SocketPath: filepath.Join(t.TempDir(), "still-missing.sock")}, keyPath, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if repeat.Len() != 0 {
		t.Fatalf("repeated unwitnessed prompt hook emitted output: %q", repeat.String())
	}
}

func TestUserPromptEmitsOneFreshMarkerAfterDelayedPaneWitness(t *testing.T) {
	t.Setenv("HERDR_ENV", "")
	key := bytes.Repeat([]byte{0x62}, 32)
	keyPath := filepath.Join(t.TempDir(), "bridge.key")
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC().Add(-6 * time.Hour)
	var sessionOutput bytes.Buffer
	sessionInput := `{"session_id":"delayed-thread","hook_event_name":"SessionStart","source":"startup"}`
	if err := RunHook(context.Background(), "session-start", strings.NewReader(sessionInput), &sessionOutput, &herdr.Client{SocketPath: "/unused"}, keyPath, startedAt); err != nil {
		t.Fatal(err)
	}
	var session hookOutput
	if err := json.Unmarshal(sessionOutput.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	oldMarker, err := protocol.ParseAndVerifySignature(session.SystemMessage, key)
	if err != nil {
		t.Fatal(err)
	}
	store, err := bridge.NewRendezvousStore(keyPath, key)
	if err != nil {
		t.Fatal(err)
	}
	promptAt := startedAt.Add(6 * time.Hour)
	if witnessed, err := store.Witness(oldMarker, "w1:p1", promptAt); err != nil || !witnessed {
		t.Fatalf("delayed witness = %t, %v", witnessed, err)
	}
	socketPath := startHookHerdrServer(t, nil, []herdr.Pane{{Agent: "codex", PaneID: "w1:p1"}})
	input := `{"session_id":"delayed-thread","hook_event_name":"UserPromptSubmit"}`
	var output bytes.Buffer
	if err := RunHook(context.Background(), "user-prompt-submit", strings.NewReader(input), &output, &herdr.Client{SocketPath: socketPath}, keyPath, promptAt); err != nil {
		t.Fatal(err)
	}
	var got hookOutput
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	marker, err := protocol.ParseAndVerify(got.SystemMessage, key, promptAt)
	if err != nil {
		t.Fatal(err)
	}
	if marker.SessionID != "delayed-thread" || marker.Source != "clear" || got.SuppressOutput {
		t.Fatalf("unexpected recovery output: marker=%#v output=%#v", marker, got)
	}
	var repeat bytes.Buffer
	if err := RunHook(context.Background(), "user-prompt-submit", strings.NewReader(input), &repeat, &herdr.Client{SocketPath: socketPath}, keyPath, promptAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if repeat.Len() != 0 {
		t.Fatalf("repeated recovery marker: %q", repeat.String())
	}
}

func TestUserPromptWarnsOnceAndEmitsNoMarkerForAmbiguousWitnesses(t *testing.T) {
	t.Setenv("HERDR_ENV", "")
	key := bytes.Repeat([]byte{0x63}, 32)
	keyPath := filepath.Join(t.TempDir(), "bridge.key")
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var sessionOutput bytes.Buffer
	sessionInput := `{"session_id":"ambiguous-thread","hook_event_name":"SessionStart","source":"startup"}`
	if err := RunHook(context.Background(), "session-start", strings.NewReader(sessionInput), &sessionOutput, &herdr.Client{SocketPath: "/unused"}, keyPath, now); err != nil {
		t.Fatal(err)
	}
	var session hookOutput
	if err := json.Unmarshal(sessionOutput.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	marker, err := protocol.ParseAndVerifySignature(session.SystemMessage, key)
	if err != nil {
		t.Fatal(err)
	}
	store, err := bridge.NewRendezvousStore(keyPath, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, paneID := range []string{"w1:p1", "w1:p2"} {
		if witnessed, witnessErr := store.Witness(marker, paneID, now); witnessErr != nil || !witnessed {
			t.Fatalf("witness %s = %t, %v", paneID, witnessed, witnessErr)
		}
	}
	panes := []herdr.Pane{{Agent: "codex", PaneID: "w1:p1"}, {Agent: "codex", PaneID: "w1:p2"}}
	socketPath := startHookHerdrServer(t, nil, panes)
	input := `{"session_id":"ambiguous-thread","hook_event_name":"UserPromptSubmit"}`
	var first bytes.Buffer
	if err := RunHook(context.Background(), "user-prompt-submit", strings.NewReader(input), &first, &herdr.Client{SocketPath: socketPath}, keyPath, now); err != nil {
		t.Fatal(err)
	}
	var warning hookOutput
	if err := json.Unmarshal(first.Bytes(), &warning); err != nil {
		t.Fatal(err)
	}
	if warning.SystemMessage != "" || !strings.Contains(warning.HookSpecificOutput.AdditionalContext, "multiple panes claiming") {
		t.Fatalf("unexpected ambiguity output: %#v", warning)
	}
	var repeat bytes.Buffer
	if err := RunHook(context.Background(), "user-prompt-submit", strings.NewReader(input), &repeat, &herdr.Client{SocketPath: socketPath}, keyPath, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if repeat.Len() != 0 {
		t.Fatalf("repeated ambiguity warning: %q", repeat.String())
	}
}

func TestUserPromptIsQuietWhenThreadIsMapped(t *testing.T) {
	t.Setenv("HERDR_ENV", "")
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("hcb-hook-%d-%d.sock", os.Getpid(), time.Now().UnixNano()))
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		var request struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(bufio.NewReader(connection)).Decode(&request)
		_ = json.NewEncoder(connection).Encode(map[string]any{
			"id": request.ID,
			"result": map[string]any{"agents": []map[string]any{{
				"agent": "codex", "workspace_id": "w1", "tab_id": "w1:t1", "pane_id": "w1:p1",
				"agent_session": map[string]any{"source": "herdr:codex", "agent": "codex", "kind": "id", "value": "mapped-thread"},
			}}},
		})
	}()

	var output bytes.Buffer
	input := `{"session_id":"mapped-thread","hook_event_name":"UserPromptSubmit"}`
	err = RunHook(context.Background(), "user-prompt-submit", strings.NewReader(input), &output, &herdr.Client{SocketPath: socketPath}, "/unused", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("mapped prompt hook emitted output: %q", output.String())
	}
}

func startHookHerdrServer(t *testing.T, agents []herdr.Agent, panes []herdr.Pane) string {
	t.Helper()
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("hcb-hook-server-%d-%d.sock", os.Getpid(), time.Now().UnixNano()))
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer connection.Close()
				var request struct {
					ID     string `json:"id"`
					Method string `json:"method"`
				}
				if json.NewDecoder(bufio.NewReader(connection)).Decode(&request) != nil {
					return
				}
				result := any(map[string]any{})
				switch request.Method {
				case "agent.list":
					result = map[string]any{"agents": agents}
				case "pane.list":
					result = map[string]any{"panes": panes}
				}
				_ = json.NewEncoder(connection).Encode(map[string]any{"id": request.ID, "result": result})
			}()
		}
	}()
	return socketPath
}
