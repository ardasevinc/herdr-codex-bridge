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
	herdrScript := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n" +
		"case \"$*\" in\n" +
		"  '--version') echo 'herdr 0.8.2' ;;\n" +
		"  'integration status') echo 'codex: current (v8)' ;;\n" +
		"  'plugin list --json') echo '{\"id\":\"test\",\"result\":{\"plugins\":[],\"type\":\"plugin_list\"}}' ;;\n" +
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
	paths, _ := ResolvePaths()
	for _, path := range []string{paths.State, paths.Key, paths.Skill} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
	hooks, _ := os.ReadFile(paths.Hooks)
	if !strings.Contains(string(hooks), "herdr-self") || !strings.Contains(string(hooks), "guard") {
		t.Fatalf("unexpected installed hooks: %s", hooks)
	}
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
	herdrScript := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n" +
		"case \"$*\" in\n" +
		"  '--version') echo 'herdr 0.8.2' ;;\n" +
		"  'integration status') echo 'codex: current (v8)' ;;\n" +
		"  'plugin list --json') echo '{\"id\":\"test\",\"result\":{\"plugins\":[]}}' ;;\n" +
		"  'integration uninstall codex') exit 7 ;;\n" +
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
	commands, _ := os.ReadFile(logPath)
	if !strings.Contains(string(commands), "plugin uninstall herdr-codex-bridge") {
		t.Fatalf("plugin rollback missing: %s", commands)
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
