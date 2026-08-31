package version

import "testing"

func TestEffectivePrefersInjectedVersion(t *testing.T) {
	previous := Version
	Version = "v0.1.0"
	t.Cleanup(func() { Version = previous })
	if got := Effective(); got != "0.1.0" {
		t.Fatalf("got %q", got)
	}
}
