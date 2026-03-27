// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"code.gitea.io/tea/modules/utils"

	"github.com/adrg/xdg"
	"gopkg.in/yaml.v3"
)

// FlagDefaults defines all flags that can be overridden with a default value
// via the config file
type FlagDefaults struct {
	// Prefer a specific git remote to use for selecting a repository on gitea,
	// instead of relying on the remote associated with main/master/trunk branch.
	// The --remote flag still has precedence over this value.
	Remote string `yaml:"remote"`
}

// Preferences that are stored in and read from the config file
type Preferences struct {
	// Prefer using an external text editor over inline multiline prompts
	Editor       bool         `yaml:"editor"`
	FlagDefaults FlagDefaults `yaml:"flag_defaults"`
}

// LocalConfig represents local configurations
type LocalConfig struct {
	Logins []Login     `yaml:"logins"`
	Prefs  Preferences `yaml:"preferences"`
}

var (
	// config contain if loaded local tea config
	config                 LocalConfig
	loadConfigOnce         sync.Once
	configPathMu           sync.Mutex
	configPathTestOverride string
)

// GetConfigPath return path to tea config file
func GetConfigPath() string {
	configPathMu.Lock()
	override := configPathTestOverride
	configPathMu.Unlock()
	if override != "" {
		return override
	}

	configFilePath, err := xdg.ConfigFile("tea/config.yml")

	var exists bool
	if err != nil {
		exists = false
	} else {
		exists, _ = utils.PathExists(configFilePath)
	}

	// fallback to old config if no new one exists
	if !exists {
		file := filepath.Join(xdg.Home, ".tea", "tea.yml")
		exists, _ = utils.PathExists(file)
		if exists {
			return file
		}
	}

	if err != nil {
		log.Fatal("unable to get or create config file")
	}

	return configFilePath
}

// SetConfigPathForTesting overrides the config path used by helpers in tests.
func SetConfigPathForTesting(path string) {
	configPathMu.Lock()
	configPathTestOverride = path
	configPathMu.Unlock()
}

// GetPreferences returns preferences based on the config file
func GetPreferences() Preferences {
	_ = loadConfig()
	return config.Prefs
}

// loadConfig load config from file
func loadConfig() (err error) {
	loadConfigOnce.Do(func() {
		ymlPath := GetConfigPath()
		exist, _ := utils.FileExist(ymlPath)
		if exist {
			bs, readErr := os.ReadFile(ymlPath)
			if readErr != nil {
				err = fmt.Errorf("failed to read config file %s: %w", ymlPath, readErr)
				return
			}

			if unmarshalErr := yaml.Unmarshal(bs, &config); unmarshalErr != nil {
				err = fmt.Errorf("failed to parse config file %s: %w", ymlPath, unmarshalErr)
				return
			}
		}
	})
	return
}

// reloadConfigFromDisk re-reads the config file from disk, bypassing the sync.Once.
// This is used after acquiring a lock to ensure we have the latest config state.
// The caller must hold the config lock.
func reloadConfigFromDisk() error {
	ymlPath := GetConfigPath()
	exist, _ := utils.FileExist(ymlPath)
	if !exist {
		// No config file yet, start with empty config
		config = LocalConfig{}
		return nil
	}

	bs, err := os.ReadFile(ymlPath)
	if err != nil {
		return fmt.Errorf("failed to read config file %s: %w", ymlPath, err)
	}

	if err := yaml.Unmarshal(bs, &config); err != nil {
		return fmt.Errorf("failed to parse config file %s: %w", ymlPath, err)
	}

	return nil
}

// SetConfigForTesting replaces the in-memory config and marks it as loaded.
// This allows tests to inject config without relying on file-based loading.
func SetConfigForTesting(cfg LocalConfig) {
	loadConfigOnce.Do(func() {}) // ensure sync.Once is spent
	config = cfg
}

// saveConfigUnsafe saves config to file without acquiring a lock.
// Caller must hold the config lock.
func saveConfigUnsafe() error {
	ymlPath := GetConfigPath()
	bs, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(ymlPath, bs, 0o600)
}
