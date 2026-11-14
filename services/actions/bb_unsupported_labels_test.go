// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"slices"
	"testing"

	actions_model "gitea.dev/models/actions"
)

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

func TestBBProfileRepositoryScope(t *testing.T) {
	config := bbExecutorConfig{Profiles: []bbExecutorProfile{
		{Name: "linux-rustfs-sm-standard-2", Repositories: []string{"BakeLab/rustfs"}, Labels: []string{"sm-standard-2:host"}},
	}}
	job := &actions_model.ActionRunJob{RunsOn: []string{"sm-standard-2"}}
	if got := config.matchingProfiles(job, "BakeLab/rustfs"); len(got) != 1 || got[0] != 0 {
		t.Fatalf("RustFS alias match = %v, want [0]", got)
	}
	if got := config.matchingProfiles(job, "BakeLab/other"); len(got) != 0 {
		t.Fatalf("other repository alias match = %v, want none", got)
	}
	if got := config.matchingProfiles(job, ""); len(got) != 0 {
		t.Fatalf("unknown repository alias match = %v, want none", got)
	}
}

func TestValidateBBProfileRepositories(t *testing.T) {
	if err := validateBBProfileRepositories([]string{"BakeLab/rustfs"}); err != nil {
		t.Fatalf("valid repository scope rejected: %v", err)
	}
	for _, repositories := range [][]string{
		{"rustfs"},
		{"BakeLab/"},
		{"BakeLab/rustfs/extra"},
		{"BakeLab/rustfs", "bakelab/RustFS"},
	} {
		if err := validateBBProfileRepositories(repositories); err == nil {
			t.Fatalf("invalid repository scope accepted: %v", repositories)
		}
	}
}

func TestBBProfileSpecificScope(t *testing.T) {
	config := bbExecutorConfig{Profiles: []bbExecutorProfile{
		{Name: "base", Labels: []string{"cpu:host"}},
		{Name: "repository", Labels: []string{"cpu:host"}, Repositories: []string{"BakeLab/TheRock"}},
		{Name: "sdk", Labels: []string{"cpu:host"}, Repositories: []string{"BakeLab/TheRock"}, Workflows: []string{"publish-sdk.yml"}, Jobs: []string{"sdk"}},
		{Name: "sdk-fallback", Labels: []string{"cpu:host"}, Repositories: []string{"BakeLab/TheRock"}, Workflows: []string{"publish-sdk.yml"}, Jobs: []string{"sdk"}},
	}}
	job := &actions_model.ActionRunJob{RunsOn: []string{"cpu"}, JobID: "sdk", Run: &actions_model.ActionRun{WorkflowID: "publish-sdk.yml"}}
	check := func(repository string, expected ...int) {
		t.Helper()
		got := config.matchingProfiles(job, repository)
		if !slices.Equal(got, expected) {
			t.Fatalf("matchingProfiles(%q) = %v, want %v", repository, got, expected)
		}
	}
	check("BakeLab/TheRock", 2, 3)
	check("BakeLab/other", 0)
	job.JobID = "runtimepack"
	check("BakeLab/TheRock", 1)
	job.JobID = "sdk"
	job.Run.WorkflowID = "other.yml"
	check("BakeLab/TheRock", 1)
	check("")
	job.Run = nil
	check("BakeLab/TheRock")
	job.Run = &actions_model.ActionRun{WorkflowID: "publish-sdk.yml"}
	job.JobID = ""
	check("BakeLab/TheRock")
}

func TestBBProfileJobScopeInheritance(t *testing.T) {
	profiles, err := resolveBBProfileExtends([]bbExecutorProfile{
		{Name: "sdk", Labels: []string{"cpu:host"}, Repositories: []string{"BakeLab/TheRock"}, Workflows: []string{"publish-sdk.yml"}, Jobs: []string{"sdk"}},
		{Name: "inherited", Extends: "sdk", Labels: []string{"cpu:host"}},
		{Name: "override", Extends: "sdk", Labels: []string{"cpu:host"}, Jobs: []string{"runtimepack"}},
		{Name: "clear", Extends: "sdk", Labels: []string{"cpu:host"}, Jobs: []string{}, Workflows: []string{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(profiles[1].Workflows, profiles[0].Workflows) || !slices.Equal(profiles[1].Jobs, profiles[0].Jobs) {
		t.Fatal("scope not inherited")
	}
	if !slices.Equal(profiles[2].Jobs, []string{"runtimepack"}) || len(profiles[3].Jobs) != 0 || len(profiles[3].Workflows) != 0 {
		t.Fatal("scope override not applied")
	}
	profiles[1].Jobs[0] = "changed"
	if profiles[0].Jobs[0] != "sdk" {
		t.Fatal("inherited scope aliases parent")
	}
	for _, profile := range profiles {
		if err := validateBBProfileJobScopes(profile); err != nil {
			t.Fatal(err)
		}
	}
	for _, profile := range []bbExecutorProfile{
		{Workflows: []string{"a.yml"}},
		{Repositories: []string{"BakeLab/TheRock"}, Jobs: []string{"sdk"}},
		{Repositories: []string{"BakeLab/TheRock"}, Workflows: []string{"*.yml"}},
		{Repositories: []string{"BakeLab/TheRock"}, Workflows: []string{"a.yml", "a.yml"}},
	} {
		if validateBBProfileJobScopes(profile) == nil {
			t.Fatalf("invalid scope accepted: %+v", profile)
		}
	}
}
