package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ardasevinc/herdr-codex-bridge/internal/bridge"
	bridgeconfig "github.com/ardasevinc/herdr-codex-bridge/internal/config"
	"github.com/ardasevinc/herdr-codex-bridge/internal/herdr"
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
	_, err = exec.LookPath("herdr")
	add("herdr_binary", err, "herdr is available on PATH")
	client := &herdr.Client{SocketPath: socketPath}
	pingCtx, cancel := context.WithTimeout(ctx, time.Second)
	err = client.Ping(pingCtx)
	cancel()
	add("herdr_socket", err, socketPath)
	state, stateErr := loadState(paths.State)
	add("install_state", stateErr, paths.State)
	if stateErr == nil {
		data, readErr := os.ReadFile(state.SkillPath)
		if readErr == nil && digest(data) != state.SkillSHA256 {
			readErr = errors.New("managed skill hash differs from install state")
		}
		add("codex_skill", readErr, state.SkillPath)
		keyInfo, keyErr := os.Stat(paths.Key)
		if keyErr == nil && keyInfo.Mode().Perm()&0o077 != 0 {
			keyErr = errors.New("bridge key permissions must be 0600")
		}
		add("bridge_key", keyErr, paths.Key)
		hookData, hookErr := os.ReadFile(paths.Hooks)
		if hookErr == nil && (!strings.Contains(string(hookData), state.SessionCommand) || !strings.Contains(string(hookData), state.PromptCommand)) {
			hookErr = errors.New("bridge hook commands are missing or drifted")
		}
		add("codex_hooks", hookErr, paths.Hooks)
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
		return encoder.Encode(report)
	}
	for _, check := range report.Checks {
		fmt.Fprintf(out, "%-8s %-30s %s\n", check.Status, check.Name, check.Message)
	}
	if !report.OK {
		return errors.New("doctor found bridge problems")
	}
	return nil
}
