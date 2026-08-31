package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureKeyRejectsInvalidExistingKey(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "bridge.key")
	if err := os.WriteFile(keyPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureKey(keyPath); err == nil {
		t.Fatal("ensureKey accepted a zero-byte key")
	}
}
