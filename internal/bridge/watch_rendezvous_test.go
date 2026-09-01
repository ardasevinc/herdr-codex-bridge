package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ardasevinc/herdr-codex-bridge/internal/herdr"
	"github.com/ardasevinc/herdr-codex-bridge/internal/protocol"
)

func TestWatcherClaimsOldMarkerThenMapsOnlyFreshMarker(t *testing.T) {
	key := bytes.Repeat([]byte{0x52}, 32)
	keyPath := filepath.Join(t.TempDir(), "bridge.key")
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewRendezvousStore(keyPath, key)
	if err != nil {
		t.Fatal(err)
	}
	oldMarker, err := protocol.New("idle-thread", "startup", time.Now().UTC().Add(-6*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Arm(oldMarker, oldMarker.IssuedAt); err != nil {
		t.Fatal(err)
	}
	oldLine, err := oldMarker.Sign(key)
	if err != nil {
		t.Fatal(err)
	}
	server := newWatcherHerdrServer(t, "w1:p1", oldLine)
	client := &herdr.Client{SocketPath: server.socketPath}
	startedAt := time.Now().UTC()
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- WatchPane(context.Background(), client, "w1:p1", keyPath, startedAt, 3*time.Second)
	}()

	claimDeadline := time.Now().Add(2 * time.Second)
	for {
		claims, claimErr := store.Claims(oldMarker.SessionID, time.Now())
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		if len(claims) == 1 {
			break
		}
		if time.Now().After(claimDeadline) {
			t.Fatal("watcher did not record an old-marker claim")
		}
		time.Sleep(25 * time.Millisecond)
	}
	if server.mappedSession() != "" {
		t.Fatal("old marker created a mapping")
	}

	freshMarker, err := protocol.New(oldMarker.SessionID, "clear", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	freshLine, err := freshMarker.Sign(key)
	if err != nil {
		t.Fatal(err)
	}
	server.setPaneText(oldLine + "\n" + freshLine)
	select {
	case err := <-watchDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not map the fresh marker")
	}
	if server.mappedSession() != oldMarker.SessionID {
		t.Fatalf("mapped session = %q", server.mappedSession())
	}
}

type watcherHerdrServer struct {
	socketPath string
	listener   net.Listener
	mu         sync.Mutex
	paneID     string
	text       string
	revision   uint64
	mapped     string
}

func newWatcherHerdrServer(t *testing.T, paneID, text string) *watcherHerdrServer {
	t.Helper()
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("hcb-watch-%d-%d.sock", os.Getpid(), time.Now().UnixNano()))
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &watcherHerdrServer{socketPath: socketPath, listener: listener, paneID: paneID, text: text, revision: 1}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})
	go server.serve()
	return server
}

func (server *watcherHerdrServer) serve() {
	for {
		connection, err := server.listener.Accept()
		if err != nil {
			return
		}
		go server.respond(connection)
	}
}

func (server *watcherHerdrServer) respond(connection net.Conn) {
	defer connection.Close()
	var request struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.NewDecoder(bufio.NewReader(connection)).Decode(&request) != nil {
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	result := any(map[string]any{})
	session := (*herdr.AgentSession)(nil)
	if server.mapped != "" {
		session = &herdr.AgentSession{Source: "herdr:codex", Agent: "codex", Kind: "id", Value: server.mapped}
	}
	switch request.Method {
	case "pane.list":
		result = map[string]any{"panes": []herdr.Pane{{Agent: "codex", PaneID: server.paneID, AgentSession: session}}}
	case "pane.read":
		result = map[string]any{"read": herdr.PaneRead{PaneID: server.paneID, Text: server.text, Revision: server.revision}}
	case "pane.report_agent_session":
		var params struct {
			SessionID string `json:"agent_session_id"`
		}
		if json.Unmarshal(request.Params, &params) == nil {
			server.mapped = params.SessionID
		}
	}
	_ = json.NewEncoder(connection).Encode(map[string]any{"id": request.ID, "result": result})
}

func (server *watcherHerdrServer) setPaneText(text string) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.text = text
	server.revision++
}

func (server *watcherHerdrServer) mappedSession() string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.mapped
}
