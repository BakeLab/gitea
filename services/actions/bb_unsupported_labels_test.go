// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import "testing"

func TestUnsupportedLabelReasonMatchesExactSetCaseInsensitively(t *testing.T) {
	config := bbExecutorConfig{UnsupportedLabels: []bbExecutorUnsupportedLabels{{
		Labels: []string{"self-hosted", "linux", "gfx942"},
		Reason: "This deployment has no gfx942 GPU.",
	}}}

	reason, ok := config.unsupportedLabelReason([]string{"GFX942", "Linux", "self-hosted"})
	if !ok || reason != "This deployment has no gfx942 GPU." {
		t.Fatalf("unsupportedLabelReason() = %q, %v", reason, ok)
	}
	if _, ok := config.unsupportedLabelReason([]string{"self-hosted", "linux"}); ok {
		t.Fatal("a subset of an unsupported label set must not match")
	}
	if _, ok := config.unsupportedLabelReason([]string{"self-hosted", "linux", "gfx942", "rocm"}); ok {
		t.Fatal("a superset of an unsupported label set must not match")
	}
}

func TestValidateUnsupportedLabelsRequiresUniqueSetsAndReasons(t *testing.T) {
	tests := []struct {
		name   string
		labels []bbExecutorUnsupportedLabels
	}{
		{name: "missing labels", labels: []bbExecutorUnsupportedLabels{{Reason: "unsupported"}}},
		{name: "missing reason", labels: []bbExecutorUnsupportedLabels{{Labels: []string{"gfx942"}}}},
		{name: "duplicate sets", labels: []bbExecutorUnsupportedLabels{{Labels: []string{"gfx942", "linux"}, Reason: "unsupported"}, {Labels: []string{"Linux", "GFX942"}, Reason: "still unsupported"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := (bbExecutorConfig{UnsupportedLabels: test.labels}).validateUnsupportedLabels(); err == nil {
				t.Fatal("validateUnsupportedLabels() succeeded for invalid config")
			}
		})
	}
	if err := (bbExecutorConfig{UnsupportedLabels: []bbExecutorUnsupportedLabels{{Labels: []string{"gfx942"}, Reason: "no compatible hardware"}}}).validateUnsupportedLabels(); err != nil {
		t.Fatalf("valid unsupported-label config failed: %v", err)
	}
}
