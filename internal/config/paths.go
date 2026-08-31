package config

import (
	"errors"
	"os"
	"path/filepath"
)

const PluginID = "herdr-codex-bridge"

func CodexHome() (string, error) {
	if value := os.Getenv("CODEX_HOME"); value != "" {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func DefaultSocket() (string, error) {
	if value := os.Getenv("HERDR_SOCKET_PATH"); value != "" {
		return value, nil
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		configHome = filepath.Join(home, ".config")
	}
	if configHome == "" {
		return "", errors.New("cannot determine Herdr config directory")
	}
	return filepath.Join(configHome, "herdr", "herdr.sock"), nil
}

func PluginConfigDir() (string, error) {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "herdr", "plugins", "config", PluginID), nil
}
