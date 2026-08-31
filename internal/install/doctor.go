package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ardasevinc/herdr-codex-bridge/internal/bridge"
	"github.com/ardasevinc/herdr-codex-bridge/internal/compat"
	bridgeconfig "github.com/ardasevinc/herdr-codex-bridge/internal/config"
	"github.com/ardasevinc/herdr-codex-bridge/internal/herdr"
	"github.com/ardasevinc/herdr-codex-bridge/internal/protocol"
)

type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type DoctorReport struct {
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
}

func Doctor(ctx context.Context, jsonOutput bool, socketPath, threadID string, out *os.File) error {
	if out == nil {
		out = os.Stdout
	}
	paths, err := ResolvePaths()
	if err != nil {
		return err
	}
	if socketPath == "" {
		socketPath, err = bridgeconfig.DefaultSocket()
		if err != nil {
			return err
		}
	}
	report := DoctorReport{OK: true}
	add := func(name string, err error, success string) {
		check := Check{Name: name, Status: "ok", Message: success}
		if err != nil {
			check.Status, check.Message, report.OK = "error", err.Error(), false
		}
		report.Checks = append(report.Checks, check)
	}
	warn := func(name, message string) {
		report.Checks = append(report.Checks, Check{Name: name, Status: "warning", Message: message})
	}
	_, err = exec.LookPath("herdr")
	add("herdr_binary", err, "herdr is available on PATH")
	add("herdr_version", minimumCommandVersion("herdr", "0.8.2"), "Herdr is compatible")
	add("codex_version", minimumCommandVersion("codex", "0.149.0"), "Codex is compatible")
	client := &herdr.Client{SocketPath: socketPath}
	pingCtx, cancel := context.WithTimeout(ctx, time.Second)
	err = client.Ping(pingCtx)
	cancel()
	add("herdr_socket", err, socketPath)
	state, stateErr := loadState(paths.State)
	if stateErr == nil {
		stateErr = validateStatePaths(paths, state)
	}
	add("install_state", stateErr, paths.State)
	if stateErr == nil {
		paths.Hooks, paths.Skill, paths.Key, paths.State = state.HooksPath, state.SkillPath, state.KeyPath, state.StatePath
		if _, binaryErr := os.Stat(state.BinaryPath); binaryErr != nil {
			add("global_helper", binaryErr, state.BinaryPath)
		} else {
			add("global_helper", nil, state.BinaryPath)
		}
		data, readErr := os.ReadFile(state.SkillPath)
		if readErr == nil && digest(data) != state.SkillSHA256 {
			readErr = errors.New("managed skill hash differs from install state")
		}
		add("codex_skill", readErr, state.SkillPath)
		keyErr := validateBridgeKey(paths.Key)
		add("bridge_key", keyErr, paths.Key)
		hookData, hookErr := os.ReadFile(paths.Hooks)
		if hookErr == nil && (!strings.Contains(string(hookData), state.SessionCommand) || !strings.Contains(string(hookData), state.PromptCommand)) {
			hookErr = errors.New("bridge hook commands are missing or drifted")
		}
		add("codex_hooks", hookErr, paths.Hooks)
		if hookErr == nil {
			warn("codex_hook_trust", "confirm the bridge hooks are trusted in Codex /hooks; Codex exposes no stable noninteractive trust query")
		}
	}
	plugins, pluginsErr := client.Plugins(ctx)
	if pluginsErr != nil {
		add("herdr_plugin", pluginsErr, "")
	} else {
		var found *herdr.Plugin
		for index := range plugins {
			if plugins[index].PluginID == bridgeconfig.PluginID {
				found = &plugins[index]
				break
			}
		}
		if found == nil {
			add("herdr_plugin", errors.New("Herdr plugin is not installed"), "")
		} else if !found.Enabled {
			add("herdr_plugin", errors.New("Herdr plugin is disabled"), found.Version)
		} else if stateErr == nil && found.Version != state.BridgeVersion {
			add("herdr_plugin", fmt.Errorf("plugin %s differs from helper %s", found.Version, state.BridgeVersion), found.PluginRoot)
		} else if _, workerErr := os.Stat(filepath.Join(found.PluginRoot, "bin", "herdr-self")); workerErr != nil {
			add("herdr_plugin", fmt.Errorf("pane worker missing: %w", workerErr), found.PluginRoot)
		} else {
			add("herdr_plugin", nil, fmt.Sprintf("%s at %s", found.Version, found.PluginRoot))
		}
	}
	if officialCodexInstalled() {
		add("official_integration_conflict", errors.New("official Herdr Codex integration is also installed"), "")
	} else {
		add("official_integration_conflict", nil, "official Herdr Codex integration is absent")
	}
	if threadID != "" && err == nil {
		resolveCtx, resolveCancel := context.WithTimeout(ctx, time.Second)
		association, resolveErr := bridge.Resolve(resolveCtx, client, threadID)
		resolveCancel()
		message := fmt.Sprintf("workspace=%s tab=%s pane=%s", association.WorkspaceID, association.TabID, association.PaneID)
		add("thread_mapping", resolveErr, message)
	}
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return err
		}
		if !report.OK {
			return errors.New("doctor found bridge problems")
		}
		return nil
	}
	for _, check := range report.Checks {
		fmt.Fprintf(out, "%-8s %-30s %s\n", check.Status, check.Name, check.Message)
	}
	if !report.OK {
		return errors.New("doctor found bridge problems")
	}
	return nil
}

func validateBridgeKey(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != 32 {
		return errors.New("bridge key must be a 32-byte regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("bridge key permissions must be 0600")
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	marker, err := protocol.New("doctor-self-test", "startup", now)
	if err != nil {
		return err
	}
	line, err := marker.Sign(key)
	if err != nil {
		return err
	}
	if _, err := protocol.ParseAndVerify(line, key, now); err != nil {
		return fmt.Errorf("bridge key self-test: %w", err)
	}
	return nil
}

func minimumCommandVersion(name, minimumText string) error {
	output, err := exec.Command(name, "--version").CombinedOutput()
	if err != nil {
		return err
	}
	actual, err := compat.Extract(string(output))
	if err != nil {
		return err
	}
	minimum, _ := compat.Parse(minimumText)
	if !compat.AtLeast(actual, minimum) {
		return fmt.Errorf("%s %s or newer is required", name, minimumText)
	}
	return nil
}
