//go:build darwin || linux

package bridge

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ardasevinc/herdr-codex-bridge/internal/herdr"
	"github.com/ardasevinc/herdr-codex-bridge/internal/protocol"
)

func TestLivePaneMarkerAcceptance(t *testing.T) {
	paneID := os.Getenv("HERDR_BRIDGE_ACCEPTANCE_PANE")
	socketPath := os.Getenv("HERDR_BRIDGE_ACCEPTANCE_SOCKET")
	keyPath := os.Getenv("HERDR_BRIDGE_ACCEPTANCE_KEY")
	if paneID == "" || socketPath == "" || keyPath == "" {
		t.Skip("set HERDR_BRIDGE_ACCEPTANCE_PANE, HERDR_BRIDGE_ACCEPTANCE_SOCKET, and HERDR_BRIDGE_ACCEPTANCE_KEY")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := &herdr.Client{SocketPath: socketPath}
	read, err := client.ReadPane(ctx, paneID, 120)
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	marker, ok := newestValidMarker(read.Text, key, now, now.Add(-protocol.MaxClockSkew))
	if !ok {
		t.Fatalf("no current authenticated marker in live pane snapshot (revision=%d, truncated=%t, bytes=%d)", read.Revision, read.Truncated, len(read.Text))
	}
	if err := client.ReportSession(ctx, paneID, marker.SessionID, marker.Source, uint64(now.UnixNano())); err != nil {
		t.Fatal(err)
	}
	association, err := Resolve(ctx, client, marker.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if association.PaneID != paneID {
		t.Fatal("reported session resolved to a different pane")
	}
}
