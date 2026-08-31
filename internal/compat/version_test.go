package compat

import "testing"

func TestExtractAndCompare(t *testing.T) {
	actual, err := Extract("codex-cli 0.151.0")
	if err != nil {
		t.Fatal(err)
	}
	minimum, _ := Parse("0.149.0")
	if !AtLeast(actual, minimum) {
		t.Fatalf("expected %#v >= %#v", actual, minimum)
	}
	old, _ := Parse("0.8.1")
	wanted, _ := Parse("0.8.2")
	if AtLeast(old, wanted) {
		t.Fatal("old version accepted")
	}
}
