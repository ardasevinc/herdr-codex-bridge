package bridge

import (
	"bytes"
	"testing"
	"time"

	"github.com/ardasevinc/herdr-codex-bridge/internal/protocol"
)

func TestNewestValidMarkerRequiresWatcherFreshness(t *testing.T) {
	key := bytes.Repeat([]byte{0x31}, 32)
	started := time.Unix(1_788_210_000, 0).UTC()
	old, _ := protocol.New("old-thread", "startup", started.Add(-time.Minute))
	fresh, _ := protocol.New("fresh-thread", "resume", started.Add(time.Second))
	oldLine, _ := old.Sign(key)
	freshLine, _ := fresh.Sign(key)

	got, ok := newestValidMarker(oldLine+"\n"+freshLine, key, started.Add(2*time.Second), started)
	if !ok || got.SessionID != "fresh-thread" {
		t.Fatalf("got %#v, %v", got, ok)
	}
}
