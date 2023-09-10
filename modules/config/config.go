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
	config         LocalConfig
	loadConfigOnce sync.Once
)

// GetConfigPath return path to tea config file
func GetConfigPath() string {
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
			bs, err := os.ReadFile(ymlPath)
			if err != nil {
				err = fmt.Errorf("Failed to read config file: %s", ymlPath)
			}

			err = yaml.Unmarshal(bs, &config)
			if err != nil {
				err = fmt.Errorf("Failed to parse contents of config file: %s", ymlPath)
			}
		}
	})
	return
}

// saveConfig save config to file
func saveConfig() error {
	ymlPath := GetConfigPath()
	bs, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(ymlPath, bs, 0o660)
}
