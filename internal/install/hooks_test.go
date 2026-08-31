package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateHooksPreservesForeignHooksAndReplacesOfficialCodexHook(t *testing.T) {
	codexHome := t.TempDir()
	path := filepath.Join(codexHome, "hooks.json")
	official := "bash '" + filepath.Join(codexHome, "herdr-agent-state.sh") + "' session"
	initial := `{"hooks":{"PreToolUse":[{"matcher":"^Bash$","hooks":[{"type":"command","command":"guard"}]}],"SessionStart":[{"hooks":[{"type":"command","command":` + quoteJSON(official) + `,"timeout":10}]}]}}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	session := "'/opt/homebrew/bin/herdr-self' _hook session-start"
	prompt := "'/opt/homebrew/bin/herdr-self' _hook user-prompt-submit"
	if err := updateHooks(path, session, prompt, true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	for _, wanted := range []string{"guard", session, prompt, "startup|resume|clear|compact"} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("missing %q in %s", wanted, text)
		}
	}
	if strings.Contains(text, official) {
		t.Fatalf("official hook remained: %s", text)
	}
	if err := updateHooks(path, session, prompt, false); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	text = string(data)
	if !strings.Contains(text, "guard") || strings.Contains(text, "herdr-self") {
		t.Fatalf("teardown damaged hooks: %s", text)
	}
}

func quoteJSON(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
