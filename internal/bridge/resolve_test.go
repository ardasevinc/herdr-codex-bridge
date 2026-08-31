//go:build darwin || linux

package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ardasevinc/herdr-codex-bridge/internal/herdr"
)

func TestResolveRequiresExactCardinality(t *testing.T) {
	socket := serveAgentList(t, []map[string]any{
		{"agent": "codex", "workspace_id": "w1", "tab_id": "w1:t1", "pane_id": "w1:p1", "agent_session": map[string]any{"source": "herdr:codex", "agent": "codex", "kind": "id", "value": "thread"}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := Resolve(ctx, &herdr.Client{SocketPath: socket}, "thread")
	if err != nil || got.PaneID != "w1:p1" {
		t.Fatalf("got %#v, %v", got, err)
	}

	socket = serveAgentList(t, []map[string]any{
		{"agent": "codex", "workspace_id": "w1", "tab_id": "w1:t1", "pane_id": "w1:p1", "agent_session": map[string]any{"source": "herdr:codex", "agent": "codex", "kind": "id", "value": "thread"}},
		{"agent": "codex", "workspace_id": "w2", "tab_id": "w2:t2", "pane_id": "w2:p3", "agent_session": map[string]any{"source": "herdr:codex", "agent": "codex", "kind": "id", "value": "thread"}},
	})
	_, err = Resolve(ctx, &herdr.Client{SocketPath: socket}, "thread")
	if !errors.Is(err, ErrAmbiguous) || !strings.Contains(err.Error(), "w1:p1, w2:p3") {
		t.Fatalf("unexpected ambiguity: %v", err)
	}
}

func serveAgentList(t *testing.T, agents []map[string]any) string {
	t.Helper()
	socket := filepath.Join(os.TempDir(), fmt.Sprintf("hcb-%d-%d.sock", os.Getpid(), time.Now().UnixNano()))
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close(); _ = os.Remove(socket) })
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		var request struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(bufio.NewReader(connection)).Decode(&request)
		_ = json.NewEncoder(connection).Encode(map[string]any{"id": request.ID, "result": map[string]any{"type": "agent_list", "agents": agents}})
	}()
	return socket
}
