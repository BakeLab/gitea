// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"fmt"
	"slices"
	"strings"
)

const maxBBProfileCapabilityOverlays = 8

func expandBBProfileCapabilityOverlays(profiles []bbExecutorProfile, overlays []bbExecutorCapabilityOverlay) ([]bbExecutorProfile, error) {
	if len(overlays) == 0 {
		return profiles, nil
	}
	if len(overlays) > maxBBProfileCapabilityOverlays {
		return nil, fmt.Errorf("BB executor supports at most %d capability overlays", maxBBProfileCapabilityOverlays)
	}

	seenLabels := make(map[string]bool, len(overlays))
	for _, overlay := range overlays {
		label := strings.TrimSpace(overlay.Label)
		name, mode, hasMode := strings.Cut(label, ":")
		if name == "" || !hasMode || (mode != "host" && mode != "guest") || overlay.Group != "accelerator" || len(overlay.Platforms) == 0 || len(overlay.Architectures) == 0 || len(overlay.Sandbox) == 0 {
			return nil, fmt.Errorf("BB executor capability overlays require a label with mode, group, platforms, architectures and sandbox")
		}
		key := strings.ToLower(name)
		if seenLabels[key] {
			return nil, fmt.Errorf("BB executor capability overlay label %q is duplicated", name)
		}
		seenLabels[key] = true
		for _, platform := range overlay.Platforms {
			if platform != "linux" && platform != "darwin" {
				return nil, fmt.Errorf("BB executor capability overlay %q has unsupported platform %q", name, platform)
			}
		}
		for _, architecture := range overlay.Architectures {
			if architecture != "amd64" && architecture != "arm64" {
				return nil, fmt.Errorf("BB executor capability overlay %q has unsupported architecture %q", name, architecture)
			}
		}
		// Hardware overlays may add an accelerator request. Platform, network,
		// image, and trust settings remain owned by the base profile.
		if len(overlay.Sandbox) != 1 {
			return nil, fmt.Errorf("BB executor capability overlay %q may only set sandbox.accelerator", name)
		}
		accelerator, ok := overlay.Sandbox["accelerator"].(map[string]any)
		class, classOK := accelerator["class"].(string)
		if !ok || !classOK || !strings.EqualFold(class, name) || accelerator["count"] == nil {
			return nil, fmt.Errorf("BB executor capability overlay %q requires sandbox.accelerator class and count", name)
		}
	}

	result := slices.Clone(profiles)
	for _, base := range profiles {
		arch := bbProfileArchitecture(base)
		platform := base.Platform
		if platform == "" {
			platform = "linux"
		}
		if arch == "" {
			continue
		}
		var compose func(int, bbExecutorProfile, map[string]bool, map[string]bool) error
		compose = func(index int, current bbExecutorProfile, groups, labels map[string]bool) error {
			if index == len(overlays) {
				if len(labels) > 0 {
					result = append(result, current)
				}
				return nil
			}
			overlay := overlays[index]
			name, _, _ := strings.Cut(overlay.Label, ":")
			_, hasAccelerator := current.Sandbox["accelerator"]
			if groups[overlay.Group] || !slices.Contains(overlay.Platforms, platform) || !slices.Contains(overlay.Architectures, arch) || bbProfileHasLabel(current, name) || hasAccelerator {
				return compose(index+1, current, groups, labels)
			}
			if err := compose(index+1, current, groups, labels); err != nil {
				return err
			}
			next := current
			next.Name = current.Name + "+" + name
			next.Labels = append(slices.Clone(current.Labels), overlay.Label)
			next.Sandbox = bbMergeSandbox(current.Sandbox, overlay.Sandbox)
			if err := compose(index+1, next, withBBProfileValue(groups, overlay.Group), withBBProfileValue(labels, name)); err != nil {
				return err
			}
			return nil
		}
		if err := compose(0, base, map[string]bool{}, map[string]bool{}); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func bbProfileArchitecture(profile bbExecutorProfile) string {
	cpu, _ := profile.Sandbox["cpu"].(map[string]any)
	arch, _ := cpu["arch"].(string)
	return arch
}

func bbProfileHasLabel(profile bbExecutorProfile, name string) bool {
	for _, label := range profile.Labels {
		labelName, _, _ := strings.Cut(label, ":")
		if strings.EqualFold(labelName, name) {
			return true
		}
	}
	return false
}

func withBBProfileValue(values map[string]bool, value string) map[string]bool {
	copy := make(map[string]bool, len(values)+1)
	for key, present := range values {
		copy[key] = present
	}
	copy[value] = true
	return copy
}
