package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type fileSnapshot struct {
	path   string
	exists bool
	data   []byte
	mode   os.FileMode
}

func captureFile(path string) (fileSnapshot, error) {
	snapshot := fileSnapshot{path: path}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return snapshot, err
	}
	snapshot.exists, snapshot.data, snapshot.mode = true, data, info.Mode().Perm()
	return snapshot, nil
}

func captureFiles(paths ...string) ([]fileSnapshot, error) {
	result := make([]fileSnapshot, 0, len(paths))
	for _, path := range paths {
		snapshot, err := captureFile(path)
		if err != nil {
			return nil, err
		}
		result = append(result, snapshot)
	}
	return result, nil
}

func restoreFiles(snapshots []fileSnapshot) error {
	var errs []error
	for index := len(snapshots) - 1; index >= 0; index-- {
		snapshot := snapshots[index]
		if snapshot.exists {
			if err := writeAtomic(snapshot.path, snapshot.data, snapshot.mode); err != nil {
				errs = append(errs, fmt.Errorf("restore %s: %w", snapshot.path, err))
			}
			continue
		}
		if err := os.Remove(snapshot.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove new file %s: %w", snapshot.path, err))
		}
		removeEmptyParents(filepath.Dir(snapshot.path))
	}
	return errors.Join(errs...)
}

func removeEmptyParents(path string) {
	for range 2 {
		if err := os.Remove(path); err != nil {
			return
		}
		path = filepath.Dir(path)
	}
}

type pluginSnapshot struct {
	present        bool
	enabled        bool
	source         string
	requestedRef   string
	resolvedCommit string
}

func capturePlugin() (pluginSnapshot, error) {
	output, err := exec.Command("herdr", "plugin", "list", "--json").Output()
	if err != nil {
		return pluginSnapshot{}, fmt.Errorf("inspect existing Herdr plugin: %w", err)
	}
	var envelope struct {
		Result struct {
			Plugins []struct {
				PluginID string `json:"plugin_id"`
				Enabled  bool   `json:"enabled"`
				Source   struct {
					Kind           string `json:"kind"`
					Owner          string `json:"owner"`
					Repo           string `json:"repo"`
					Subdir         string `json:"subdir"`
					RequestedRef   string `json:"requested_ref"`
					ResolvedCommit string `json:"resolved_commit"`
				} `json:"source"`
			} `json:"plugins"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		return pluginSnapshot{}, fmt.Errorf("decode existing Herdr plugins: %w", err)
	}
	for _, plugin := range envelope.Result.Plugins {
		if plugin.PluginID != "herdr-codex-bridge" {
			continue
		}
		if plugin.Source.Kind != "github" {
			return pluginSnapshot{}, errors.New("Herdr Codex Bridge plugin is linked locally; unlink it before setup --apply")
		}
		source := plugin.Source.Owner + "/" + plugin.Source.Repo
		if plugin.Source.Subdir != "" {
			source += "/" + plugin.Source.Subdir
		}
		return pluginSnapshot{
			present: true, enabled: plugin.Enabled, source: source,
			requestedRef: plugin.Source.RequestedRef, resolvedCommit: plugin.Source.ResolvedCommit,
		}, nil
	}
	return pluginSnapshot{}, nil
}

func restorePlugin(snapshot pluginSnapshot) error {
	current, err := capturePlugin()
	if err != nil {
		return err
	}
	if !snapshot.present {
		if !current.present {
			return nil
		}
		return run("herdr", "plugin", "uninstall", "herdr-codex-bridge")
	}
	if current.present && current.source == snapshot.source && current.resolvedCommit == snapshot.resolvedCommit {
		if current.enabled == snapshot.enabled {
			return nil
		}
		verb := "disable"
		if snapshot.enabled {
			verb = "enable"
		}
		return run("herdr", "plugin", verb, "herdr-codex-bridge")
	}
	args := []string{"plugin", "install", snapshot.source, "--yes"}
	ref := snapshot.resolvedCommit
	if ref == "" {
		ref = snapshot.requestedRef
	}
	if ref != "" {
		args = append(args, "--ref", ref)
	}
	if err := run("herdr", args...); err != nil {
		return err
	}
	if !snapshot.enabled {
		return run("herdr", "plugin", "disable", "herdr-codex-bridge")
	}
	return nil
}
