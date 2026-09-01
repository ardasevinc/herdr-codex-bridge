package install

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ardasevinc/herdr-codex-bridge/internal/assets"
	"github.com/ardasevinc/herdr-codex-bridge/internal/bridge"
	"github.com/ardasevinc/herdr-codex-bridge/internal/compat"
	bridgeconfig "github.com/ardasevinc/herdr-codex-bridge/internal/config"
)

const stateVersion = 1

type Options struct {
	Apply      bool
	Force      bool
	BinaryPath string
	SocketPath string
	Version    string
	Out        *os.File
}

type State struct {
	SchemaVersion        int    `json:"schema_version"`
	BridgeVersion        string `json:"bridge_version"`
	BinaryPath           string `json:"binary_path"`
	SocketPath           string `json:"socket_path"`
	CodexHome            string `json:"codex_home"`
	HooksPath            string `json:"hooks_path"`
	SkillPath            string `json:"skill_path"`
	KeyPath              string `json:"key_path"`
	StatePath            string `json:"state_path"`
	SkillSHA256          string `json:"skill_sha256"`
	SessionCommand       string `json:"session_command"`
	PromptCommand        string `json:"prompt_command"`
	OfficialWasInstalled bool   `json:"official_was_installed"`
	InstalledAt          string `json:"installed_at"`
}

type Paths struct {
	CodexHome       string
	Hooks           string
	Skill           string
	PluginConfigDir string
	Key             string
	State           string
}

func ResolvePaths() (Paths, error) {
	codexHome, err := bridgeconfig.CodexHome()
	if err != nil {
		return Paths{}, err
	}
	pluginConfigDir, err := bridgeconfig.PluginConfigDir()
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		CodexHome:       codexHome,
		Hooks:           filepath.Join(codexHome, "hooks.json"),
		Skill:           filepath.Join(codexHome, "skills", "herdr-self", "SKILL.md"),
		PluginConfigDir: pluginConfigDir,
		Key:             filepath.Join(pluginConfigDir, "bridge.key"),
		State:           filepath.Join(pluginConfigDir, "install-state.json"),
	}, nil
}

func SetupCodex(opts Options) (retErr error) {
	paths, err := ResolvePaths()
	if err != nil {
		return err
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.BinaryPath == "" {
		opts.BinaryPath, err = os.Executable()
		if err != nil {
			return err
		}
	}
	if opts.SocketPath == "" {
		opts.SocketPath, err = bridgeconfig.DefaultSocket()
		if err != nil {
			return err
		}
	}
	if !filepath.IsAbs(opts.BinaryPath) {
		return errors.New("herdr-self executable path must be absolute")
	}
	if err := preflight(paths); err != nil {
		return err
	}
	previous, previousErr := loadState(paths.State)
	if previousErr == nil {
		if err := validateStatePaths(paths, previous); err != nil {
			return err
		}
	} else if !errors.Is(previousErr, os.ErrNotExist) {
		return previousErr
	}
	officialInstalled := officialCodexInstalled()
	officialWasInstalled := officialInstalled
	if previousErr == nil {
		officialWasInstalled = previous.OfficialWasInstalled || officialInstalled
	}
	sessionCommand := hookCommand(opts.BinaryPath, "session-start", paths.Key, opts.SocketPath)
	promptCommand := hookCommand(opts.BinaryPath, "user-prompt-submit", paths.Key, opts.SocketPath)
	fmt.Fprintf(opts.Out, "Herdr Codex Bridge setup plan\n")
	fmt.Fprintf(opts.Out, "  install plugin: ardasevinc/herdr-codex-bridge@v%s\n", opts.Version)
	fmt.Fprintf(opts.Out, "  replace official Herdr Codex hook: %t\n", officialInstalled)
	fmt.Fprintf(opts.Out, "  update hooks: %s\n", paths.Hooks)
	fmt.Fprintf(opts.Out, "  install skill: %s\n", paths.Skill)
	fmt.Fprintf(opts.Out, "  create shared key: %s\n", paths.Key)
	if !opts.Apply {
		fmt.Fprintln(opts.Out, "dry run only; rerun with --apply to make these changes")
		return nil
	}
	if opts.Version == "" || opts.Version == "dev" {
		return errors.New("setup --apply requires a released herdr-self build")
	}
	if err := ensureManagedFilesSafe(paths, opts.Force); err != nil {
		return err
	}
	pluginBefore, err := capturePlugin()
	if err != nil {
		return err
	}
	filesBefore, err := captureFiles(paths.Hooks, paths.Skill, paths.Key, paths.State)
	if err != nil {
		return err
	}
	rendezvousPath := bridge.RendezvousPath(paths.Key)
	_, rendezvousStatErr := os.Stat(rendezvousPath)
	rendezvousExisted := rendezvousStatErr == nil
	if rendezvousStatErr != nil && !errors.Is(rendezvousStatErr, os.ErrNotExist) {
		return rendezvousStatErr
	}
	defer func() {
		if retErr == nil {
			return
		}
		var rollbackErrs []error
		if err := restoreFiles(filesBefore); err != nil {
			rollbackErrs = append(rollbackErrs, err)
		}
		if err := restoreOfficialIntegration(officialInstalled); err != nil {
			rollbackErrs = append(rollbackErrs, err)
		}
		if err := restorePlugin(pluginBefore); err != nil {
			rollbackErrs = append(rollbackErrs, err)
		}
		if !rendezvousExisted {
			if err := os.RemoveAll(rendezvousPath); err != nil {
				rollbackErrs = append(rollbackErrs, err)
			}
		}
		if rollbackErr := errors.Join(rollbackErrs...); rollbackErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("rollback failed: %w", rollbackErr))
		}
	}()
	if err := run("herdr", "plugin", "install", "ardasevinc/herdr-codex-bridge", "--ref", "v"+opts.Version, "--yes"); err != nil {
		return err
	}
	if officialInstalled {
		if err := run("herdr", "integration", "uninstall", "codex"); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(paths.PluginConfigDir, 0o700); err != nil {
		return err
	}
	if err := ensureKey(paths.Key); err != nil {
		return err
	}
	if err := writeAtomic(paths.Skill, assets.CodexSkill, 0o644); err != nil {
		return err
	}
	if err := updateHooks(paths.Hooks, sessionCommand, promptCommand, true); err != nil {
		return err
	}
	state := State{
		SchemaVersion: stateVersion, BridgeVersion: opts.Version, BinaryPath: opts.BinaryPath,
		SocketPath: opts.SocketPath, CodexHome: paths.CodexHome, HooksPath: paths.Hooks,
		SkillPath: paths.Skill, KeyPath: paths.Key, StatePath: paths.State, SkillSHA256: digest(assets.CodexSkill),
		SessionCommand: sessionCommand, PromptCommand: promptCommand,
		OfficialWasInstalled: officialWasInstalled, InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	data = append(data, '\n')
	if err := writeAtomic(paths.State, data, 0o600); err != nil {
		return err
	}
	key, err := os.ReadFile(paths.Key)
	if err != nil {
		return err
	}
	rendezvous, err := bridge.NewRendezvousStore(paths.Key, key)
	if err != nil {
		return err
	}
	if err := rendezvous.Sweep(time.Now().UTC()); err != nil {
		return err
	}
	fmt.Fprintln(opts.Out, "setup applied; verify hooks in a fresh or resumed Codex session")
	return nil
}

func TeardownCodex(opts Options) (retErr error) {
	paths, err := ResolvePaths()
	if err != nil {
		return err
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	state, err := loadState(paths.State)
	if err != nil {
		return err
	}
	if err := validateStatePaths(paths, state); err != nil {
		return err
	}
	paths.Hooks, paths.Skill, paths.Key, paths.State = state.HooksPath, state.SkillPath, state.KeyPath, state.StatePath
	fmt.Fprintln(opts.Out, "Herdr Codex Bridge teardown plan")
	fmt.Fprintf(opts.Out, "  remove bridge hooks from: %s\n", paths.Hooks)
	fmt.Fprintf(opts.Out, "  remove managed skill: %s\n", paths.Skill)
	fmt.Fprintf(opts.Out, "  remove private rendezvous state: %s\n", bridge.RendezvousPath(paths.Key))
	fmt.Fprintf(opts.Out, "  restore official Herdr Codex integration: %t\n", state.OfficialWasInstalled)
	if !opts.Apply {
		fmt.Fprintln(opts.Out, "dry run only; rerun with --apply to make these changes")
		return nil
	}
	if err := ensureManagedFilesSafe(paths, opts.Force); err != nil {
		return err
	}
	pluginBefore, err := capturePlugin()
	if err != nil {
		return err
	}
	filesBefore, err := captureFiles(paths.Hooks, paths.Skill, paths.Key, paths.State)
	if err != nil {
		return err
	}
	officialBefore := officialCodexInstalled()
	defer func() {
		if retErr == nil {
			return
		}
		var rollbackErrs []error
		if err := restoreFiles(filesBefore); err != nil {
			rollbackErrs = append(rollbackErrs, err)
		}
		if err := restoreOfficialIntegration(officialBefore); err != nil {
			rollbackErrs = append(rollbackErrs, err)
		}
		if err := restorePlugin(pluginBefore); err != nil {
			rollbackErrs = append(rollbackErrs, err)
		}
		if rollbackErr := errors.Join(rollbackErrs...); rollbackErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("rollback failed: %w", rollbackErr))
		}
	}()
	if err := updateHooks(paths.Hooks, state.SessionCommand, state.PromptCommand, false); err != nil {
		return err
	}
	if err := os.Remove(paths.Skill); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if state.OfficialWasInstalled {
		if err := run("herdr", "integration", "install", "codex"); err != nil {
			return err
		}
	}
	if err := run("herdr", "plugin", "uninstall", bridgeconfig.PluginID); err != nil {
		return err
	}
	for _, path := range []string{paths.Key, paths.State} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.RemoveAll(bridge.RendezvousPath(paths.Key)); err != nil {
		return err
	}
	fmt.Fprintln(opts.Out, "teardown applied; restart the centralized Codex app-server")
	return nil
}

func loadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, fmt.Errorf("read bridge install state: %w", err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("decode bridge install state: %w", err)
	}
	return state, nil
}

func validateStatePaths(paths Paths, state State) error {
	if state.SchemaVersion != stateVersion {
		return fmt.Errorf("unsupported bridge install state schema %d", state.SchemaVersion)
	}
	if state.CodexHome == "" || state.HooksPath == "" || state.SkillPath == "" || state.KeyPath == "" || state.StatePath == "" {
		return errors.New("bridge install state is missing managed paths; rerun setup with the original CODEX_HOME")
	}
	if filepath.Clean(paths.CodexHome) != filepath.Clean(state.CodexHome) || filepath.Clean(paths.State) != filepath.Clean(state.StatePath) {
		return fmt.Errorf("current CODEX_HOME does not match bridge install state: current=%s installed=%s", paths.CodexHome, state.CodexHome)
	}
	return nil
}

func ensureManagedFilesSafe(paths Paths, force bool) error {
	state, err := loadState(paths.State)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	data, err := os.ReadFile(state.SkillPath)
	if err == nil && digest(data) != state.SkillSHA256 && !force {
		return errors.New("managed Codex skill was modified locally; rerun with --force to replace it")
	}
	return nil
}

func preflight(paths Paths) error {
	if info, err := os.Stat(paths.CodexHome); err != nil || !info.IsDir() {
		return fmt.Errorf("Codex home not found at %s", paths.CodexHome)
	}
	if data, err := os.ReadFile(paths.Hooks); err == nil {
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			return fmt.Errorf("parse %s: %w", paths.Hooks, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	checks := []struct {
		name    string
		args    []string
		minimum string
	}{
		{name: "herdr", args: []string{"--version"}, minimum: "0.8.2"},
		{name: "codex", args: []string{"--version"}, minimum: "0.149.0"},
	}
	for _, check := range checks {
		output, err := exec.Command(check.name, check.args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("run %s --version: %w", check.name, err)
		}
		actual, err := compat.Extract(string(output))
		if err != nil {
			return fmt.Errorf("parse %s version: %w", check.name, err)
		}
		minimum, _ := compat.Parse(check.minimum)
		if !compat.AtLeast(actual, minimum) {
			return fmt.Errorf("%s %s or newer is required", check.name, check.minimum)
		}
	}
	return nil
}

func ensureKey(path string) error {
	if info, err := os.Stat(path); err == nil {
		if !info.Mode().IsRegular() || info.Size() != 32 {
			return errors.New("bridge key must be a 32-byte regular file")
		}
		if info.Mode().Perm()&0o077 != 0 {
			return errors.New("bridge key permissions are too broad; expected 0600")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	return writeAtomic(path, key, 0o600)
}

func restoreOfficialIntegration(wantInstalled bool) error {
	installed := officialCodexInstalled()
	if installed == wantInstalled {
		return nil
	}
	if wantInstalled {
		return run("herdr", "integration", "install", "codex")
	}
	return run("herdr", "integration", "uninstall", "codex")
}

func officialCodexInstalled() bool {
	command := exec.Command("herdr", "integration", "status")
	output, err := command.Output()
	return err == nil && strings.Contains(string(output), "codex: current")
}

func hookCommand(binary, action, keyPath, socketPath string) string {
	return shellQuote(binary) + " _hook " + action + " --key-path " + shellQuote(keyPath) + " --socket " + shellQuote(socketPath)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func run(name string, args ...string) error {
	command := exec.Command(name, args...)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("run %s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".herdr-codex-bridge-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
