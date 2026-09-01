package bridge

import (
	"bytes"
	"testing"
	"time"

	"github.com/ardasevinc/herdr-codex-bridge/internal/herdr"
	"github.com/ardasevinc/herdr-codex-bridge/internal/protocol"
)

func TestPaneSnapshotCursorUsesTextWhenRevisionIsUnavailable(t *testing.T) {
	var cursor paneSnapshotCursor
	if !cursor.changed(herdr.PaneRead{Revision: 0, Text: "before"}) {
		t.Fatal("first revision-zero snapshot was ignored")
	}
	if cursor.changed(herdr.PaneRead{Revision: 0, Text: "before"}) {
		t.Fatal("unchanged revision-zero snapshot was treated as new")
	}
	if !cursor.changed(herdr.PaneRead{Revision: 0, Text: "after"}) {
		t.Fatal("changed revision-zero snapshot was ignored")
	}
}

func TestPaneSnapshotCursorUsesNonzeroRevision(t *testing.T) {
	var cursor paneSnapshotCursor
	if !cursor.changed(herdr.PaneRead{Revision: 7, Text: "before"}) {
		t.Fatal("first versioned snapshot was ignored")
	}
	if cursor.changed(herdr.PaneRead{Revision: 7, Text: "after"}) {
		t.Fatal("unchanged nonzero revision was treated as new")
	}
	if !cursor.changed(herdr.PaneRead{Revision: 8, Text: "after"}) {
		t.Fatal("new nonzero revision was ignored")
	}
}

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

func TestNewestValidMarkerAcceptsTUIHardWrappedRecord(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	started := time.Unix(1_788_245_300, 0).UTC()
	marker, err := protocol.New("01a05ba6-5535-77e1-a1c8-e2ccf3750bcd", "clear", started.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	line, err := marker.Sign(key)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := line[:80] + "\n" + line[80:141] + "\n" + line[141:]

	got, ok := newestValidMarker(wrapped, key, started.Add(2*time.Second), started)
	if !ok || got.SessionID != marker.SessionID || got.Source != marker.Source {
		t.Fatalf("got %#v, %v", got, ok)
	}
}

func TestNewestValidMarkerSkipsMalformedRecordBeforeValidMarker(t *testing.T) {
	key := bytes.Repeat([]byte{0x43}, 32)
	started := time.Unix(1_788_245_400, 0).UTC()
	marker, err := protocol.New("valid-thread", "startup", started.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	line, err := marker.Sign(key)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := newestValidMarker(protocol.Prefix+" malformed "+line, key, started.Add(2*time.Second), started)
	if !ok || got.SessionID != marker.SessionID {
		t.Fatalf("got %#v, %v", got, ok)
	}
}
