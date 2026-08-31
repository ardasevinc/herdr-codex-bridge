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
	present      bool
	source       string
	requestedRef string
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
		ref := plugin.Source.RequestedRef
		if ref == "" {
			ref = plugin.Source.ResolvedCommit
		}
		return pluginSnapshot{present: true, source: source, requestedRef: ref}, nil
	}
	return pluginSnapshot{}, nil
}

func restorePlugin(snapshot pluginSnapshot) error {
	if !snapshot.present {
		return run("herdr", "plugin", "uninstall", "herdr-codex-bridge")
	}
	args := []string{"plugin", "install", snapshot.source, "--yes"}
	if snapshot.requestedRef != "" {
		args = append(args, "--ref", snapshot.requestedRef)
	}
	return run("herdr", args...)
}
