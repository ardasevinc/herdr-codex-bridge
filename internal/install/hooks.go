package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func updateHooks(path, sessionCommand, promptCommand string, installing bool) error {
	root := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	remove := func(event string, predicates ...string) {
		entries, _ := hooks[event].([]any)
		kept := entries[:0]
		for _, rawEntry := range entries {
			entry, _ := rawEntry.(map[string]any)
			commands, _ := entry["hooks"].([]any)
			filtered := commands[:0]
			for _, rawCommand := range commands {
				command, _ := rawCommand.(map[string]any)
				text, _ := command["command"].(string)
				owned := false
				for _, predicate := range predicates {
					if predicate != "" && strings.Contains(text, predicate) {
						owned = true
					}
				}
				if !owned {
					filtered = append(filtered, rawCommand)
				}
			}
			if len(filtered) > 0 {
				entry["hooks"] = filtered
				kept = append(kept, entry)
			}
		}
		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}
	codexHome := filepath.Dir(path)
	officialPath := filepath.Join(codexHome, "herdr-agent-state.sh")
	remove("SessionStart", sessionCommand, officialPath)
	remove("UserPromptSubmit", promptCommand)
	if installing {
		hooks["SessionStart"] = append(asSlice(hooks["SessionStart"]), hookEntry(sessionCommand, "startup|resume|clear|compact"))
		hooks["UserPromptSubmit"] = append(asSlice(hooks["UserPromptSubmit"]), hookEntry(promptCommand, ""))
	}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAtomic(path, data, 0o644)
}

func asSlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func hookEntry(command, matcher string) map[string]any {
	entry := map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 10}},
	}
	if matcher != "" {
		entry["matcher"] = matcher
	}
	return entry
}
