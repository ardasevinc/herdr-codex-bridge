package app

import "testing"

func TestSafeWithoutCaller(t *testing.T) {
	tests := []struct {
		args []string
		safe bool
	}{
		{[]string{"status"}, true},
		{[]string{"agent", "list", "--json"}, true},
		{[]string{"pane", "read", "w1:p1"}, true},
		{[]string{"pane", "zoom", "--on"}, false},
		{[]string{"pane", "layout"}, true},
		{[]string{"pane", "send-text", "w1:p1", "hello"}, false},
		{[]string{"workspace", "close", "w1"}, false},
		{[]string{"api", "call", "pane.close"}, false},
	}
	for _, test := range tests {
		if got := safeWithoutCaller(test.args); got != test.safe {
			t.Errorf("safeWithoutCaller(%q) = %t, want %t", test.args, got, test.safe)
		}
	}
}
