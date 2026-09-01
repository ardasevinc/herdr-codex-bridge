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

func TestUserPromptReissuesFreshMarkerWhenThreadIsUnmapped(t *testing.T) {
	t.Setenv("HERDR_ENV", "")
	key := bytes.Repeat([]byte{0x61}, 32)
	keyPath := filepath.Join(t.TempDir(), "bridge.key")
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_788_210_100, 0).UTC()
	input := `{"session_id":"new-thread","hook_event_name":"UserPromptSubmit"}`
	var output bytes.Buffer
	err := RunHook(context.Background(), "user-prompt-submit", strings.NewReader(input), &output, &herdr.Client{SocketPath: filepath.Join(t.TempDir(), "missing.sock")}, keyPath, now)
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
	if marker.SessionID != "new-thread" || marker.Source != "clear" || !got.SuppressOutput {
		t.Fatalf("unexpected recovery output: marker=%#v output=%#v", marker, got)
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
