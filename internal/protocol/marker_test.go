package protocol

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestMarkerRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	now := time.Unix(1_788_210_000, 0).UTC()
	marker, err := New("01a05858-28e3-77d1-85ab-882a13125206", "resume", now)
	if err != nil {
		t.Fatal(err)
	}
	line, err := marker.Sign(key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseAndVerify("prefix "+line+" suffix", key, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != marker.SessionID || got.Source != "resume" || got.Nonce != marker.Nonce {
		t.Fatalf("unexpected marker: %#v", got)
	}
}

func TestMarkerRejectsTamperingAndStaleness(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	now := time.Unix(1_788_210_000, 0).UTC()
	marker, _ := New("thread", "startup", now)
	line, _ := marker.Sign(key)

	if _, err := ParseAndVerify(strings.Replace(line, "thread", "other", 1), key, now); err == nil {
		t.Fatal("tampered marker accepted")
	}
	if _, err := ParseAndVerify(line, key, now.Add(MaxClockSkew+time.Millisecond)); err == nil {
		t.Fatal("stale marker accepted")
	}
}
