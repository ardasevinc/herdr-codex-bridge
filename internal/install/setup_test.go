package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupAndTeardownCodexInIsolatedHomes(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, "codex")
	configHome := filepath.Join(root, "config")
	binDir := filepath.Join(root, "bin")
	for _, dir := range []string{codexHome, binDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	initialHooks := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"guard"}]}]}}`
	if err := os.WriteFile(filepath.Join(codexHome, "hooks.json"), []byte(initialHooks), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "commands.log")
	officialState := filepath.Join(root, "official-installed")
	pluginState := filepath.Join(root, "plugin-installed")
	if err := os.WriteFile(officialState, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	herdrScript := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n" +
		"case \"$*\" in\n" +
		"  '--version') echo 'herdr 0.8.2' ;;\n" +
		"  'integration status') test -f " + shellQuote(officialState) + " && echo 'codex: current (v8)' ;;\n" +
		"  'integration uninstall codex') rm -f " + shellQuote(officialState) + " ;;\n" +
		"  'integration install codex') : > " + shellQuote(officialState) + " ;;\n" +
		"  'plugin install '* ) : > " + shellQuote(pluginState) + " ;;\n" +
		"  'plugin uninstall herdr-codex-bridge') rm -f " + shellQuote(pluginState) + " ;;\n" +
		"  'plugin list --json') if test -f " + shellQuote(pluginState) + "; then echo '{\"id\":\"test\",\"result\":{\"plugins\":[{\"plugin_id\":\"herdr-codex-bridge\",\"enabled\":true,\"source\":{\"kind\":\"github\",\"owner\":\"ardasevinc\",\"repo\":\"herdr-codex-bridge\",\"requested_ref\":\"v0.1.0\",\"resolved_commit\":\"abc\"}}]}}'; else echo '{\"id\":\"test\",\"result\":{\"plugins\":[]}}'; fi ;;\n" +
		"esac\n"
	writeExecutable(t, filepath.Join(binDir, "herdr"), herdrScript)
	writeExecutable(t, filepath.Join(binDir, "codex"), "#!/bin/sh\necho 'codex-cli 0.149.0'\n")
	helperPath := filepath.Join(binDir, "herdr-self")
	writeExecutable(t, helperPath, "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	out, err := os.Create(filepath.Join(root, "output.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()

	opts := Options{Apply: true, BinaryPath: helperPath, SocketPath: filepath.Join(root, "herdr.sock"), Version: "0.1.0", Out: out}
	if err := SetupCodex(opts); err != nil {
		t.Fatal(err)
	}
	if err := SetupCodex(opts); err != nil {
		t.Fatalf("repeat setup: %v", err)
	}
	paths, _ := ResolvePaths()
	state, err := loadState(paths.State)
	if err != nil || !state.OfficialWasInstalled {
		t.Fatalf("repeat setup forgot original integration state: %#v, %v", state, err)
	}
	for _, path := range []string{paths.State, paths.Key, paths.Skill} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
	hooks, _ := os.ReadFile(paths.Hooks)
	if !strings.Contains(string(hooks), "herdr-self") || !strings.Contains(string(hooks), "guard") {
		t.Fatalf("unexpected installed hooks: %s", hooks)
	}
	otherCodexHome := filepath.Join(root, "other-codex")
	if err := os.MkdirAll(otherCodexHome, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", otherCodexHome)
	if err := TeardownCodex(Options{Out: out}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("teardown with another CODEX_HOME = %v", err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	if err := TeardownCodex(opts); err != nil {
		t.Fatal(err)
	}
	hooks, _ = os.ReadFile(paths.Hooks)
	if strings.Contains(string(hooks), "herdr-self") || !strings.Contains(string(hooks), "guard") {
		t.Fatalf("unexpected teardown hooks: %s", hooks)
	}
	commands, _ := os.ReadFile(logPath)
	for _, wanted := range []string{
		"plugin install ardasevinc/herdr-codex-bridge --ref v0.1.0 --yes",
		"integration uninstall codex",
		"integration install codex",
		"plugin uninstall herdr-codex-bridge",
	} {
		if !strings.Contains(string(commands), wanted) {
			t.Fatalf("missing command %q in %s", wanted, commands)
		}
	}
}

func TestSetupRollsBackPluginWhenOfficialHookRemovalFails(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, "codex")
	configHome := filepath.Join(root, "config")
	binDir := filepath.Join(root, "bin")
	for _, dir := range []string{codexHome, binDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	initialHooks := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"guard"}]}]}}`
	hooksPath := filepath.Join(codexHome, "hooks.json")
	if err := os.WriteFile(hooksPath, []byte(initialHooks), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "commands.log")
	pluginState := filepath.Join(root, "plugin-installed")
	officialState := filepath.Join(root, "official-installed")
	if err := os.WriteFile(officialState, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	herdrScript := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n" +
		"case \"$*\" in\n" +
		"  '--version') echo 'herdr 0.8.2' ;;\n" +
		"  'integration status') test -f " + shellQuote(officialState) + " && echo 'codex: current (v8)' ;;\n" +
		"  'integration install codex') : > " + shellQuote(officialState) + " ;;\n" +
		"  'plugin list --json') if test -f " + shellQuote(pluginState) + "; then echo '{\"id\":\"test\",\"result\":{\"plugins\":[{\"plugin_id\":\"herdr-codex-bridge\",\"enabled\":true,\"source\":{\"kind\":\"github\",\"owner\":\"ardasevinc\",\"repo\":\"herdr-codex-bridge\",\"resolved_commit\":\"abc\"}}]}}'; else echo '{\"id\":\"test\",\"result\":{\"plugins\":[]}}'; fi ;;\n" +
		"  'plugin install '* ) : > " + shellQuote(pluginState) + " ;;\n" +
		"  'plugin uninstall herdr-codex-bridge') rm -f " + shellQuote(pluginState) + " ;;\n" +
		"  'integration uninstall codex') rm -f " + shellQuote(officialState) + "; exit 7 ;;\n" +
		"esac\n"
	writeExecutable(t, filepath.Join(binDir, "herdr"), herdrScript)
	writeExecutable(t, filepath.Join(binDir, "codex"), "#!/bin/sh\necho 'codex-cli 0.149.0'\n")
	helperPath := filepath.Join(binDir, "herdr-self")
	writeExecutable(t, helperPath, "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)

	err := SetupCodex(Options{Apply: true, BinaryPath: helperPath, SocketPath: filepath.Join(root, "herdr.sock"), Version: "0.1.0", Out: os.Stdout})
	if err == nil {
		t.Fatal("setup unexpectedly succeeded")
	}
	hooks, _ := os.ReadFile(hooksPath)
	if string(hooks) != initialHooks {
		t.Fatalf("hooks were not restored: %s", hooks)
	}
	paths, _ := ResolvePaths()
	if _, err := os.Stat(paths.State); !os.IsNotExist(err) {
		t.Fatalf("state survived rollback: %v", err)
	}
	if _, err := os.Stat(officialState); err != nil {
		t.Fatalf("official integration state was not restored: %v", err)
	}
	commands, _ := os.ReadFile(logPath)
	if !strings.Contains(string(commands), "plugin uninstall herdr-codex-bridge") {
		t.Fatalf("plugin rollback missing: %s", commands)
	}
	if !strings.Contains(string(commands), "integration install codex") {
		t.Fatalf("official integration rollback missing: %s", commands)
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
