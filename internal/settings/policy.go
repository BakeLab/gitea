// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package settings

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// Policy holds the restrictions an administrator sets for every user of a system. Users can only
// tighten them in config.yml, never loosen them.
type Policy struct {
	// Path is the file the policy was read from, or empty when there is none.
	Path                 string   `yaml:"-"`
	Extensions           string   `yaml:"extensions,omitempty"`
	ExtensionHosts       []string `yaml:"extension_hosts,omitempty"`
	ExtensionOwners      []string `yaml:"extension_owners,omitempty"`
	ExtensionCredentials string   `yaml:"extension_credentials,omitempty"`
}

// LoadPolicy reads the policy at path. A missing file is no policy; a broken one is an error,
// since ignoring it would lift the restrictions it sets.
func LoadPolicy(path string) (Policy, error) {
	if path == "" {
		return Policy{}, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Policy{}, nil
	}
	if err != nil {
		return Policy{}, fmt.Errorf("read policy %s: %w", path, err)
	}
	var policy Policy
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := decode(path, data, &policy); err != nil {
			return Policy{}, err
		}
	}
	policy.Path = path
	if policy.Extensions != "" && policy.Extensions != "enabled" && policy.Extensions != "disabled" {
		return Policy{}, fmt.Errorf("policy %s: extensions must be enabled or disabled", path)
	}
	if policy.ExtensionCredentials != "" && policy.ExtensionCredentials != "allowed" && policy.ExtensionCredentials != "denied" {
		return Policy{}, fmt.Errorf("policy %s: extension_credentials must be allowed or denied", path)
	}
	if err := validateExtensionSources(policy.ExtensionHosts, policy.ExtensionOwners); err != nil {
		return Policy{}, fmt.Errorf("policy %s: %w", path, err)
	}
	return policy, nil
}

// ParseExtensionOwner splits an extension_owners entry, HOST/OWNER, into the normalized instance
// URL and the owner.
func ParseExtensionOwner(entry string) (host, owner string, err error) {
	slash := strings.LastIndex(entry, "/")
	if slash < 0 || slash == len(entry)-1 {
		return "", "", fmt.Errorf("invalid extension owner %q: use HOST/OWNER, such as https://gitea.com/gitea", entry)
	}
	host, err = NormalizeHost(entry[:slash])
	if err != nil {
		return "", "", fmt.Errorf("invalid extension owner %q: %w", entry, err)
	}
	return host, entry[slash+1:], nil
}

func validateExtensionSources(hosts, owners []string) error {
	for _, host := range hosts {
		if normalized, err := NormalizeHost(host); err != nil || normalized != host {
			return fmt.Errorf("extension host %q must be a normalized instance URL", host)
		}
	}
	for _, owner := range owners {
		if _, _, err := ParseExtensionOwner(owner); err != nil {
			return err
		}
	}
	return nil
}
