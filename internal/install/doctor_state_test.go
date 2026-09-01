package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRendezvousState(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if err := validateRendezvousState(missing); err != nil {
		t.Fatalf("missing state: %v", err)
	}
	root := filepath.Join(t.TempDir(), "rendezvous-v1")
	lane := filepath.Join(root, "session-token")
	if err := os.MkdirAll(lane, 0o700); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(lane, "arm.json")
	if err := os.WriteFile(record, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateRendezvousState(root); err != nil {
		t.Fatalf("private state: %v", err)
	}
	if err := os.Chmod(record, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateRendezvousState(root); err == nil {
		t.Fatal("broad record permissions passed validation")
	}
}
