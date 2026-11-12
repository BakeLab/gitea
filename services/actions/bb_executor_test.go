// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitea.dev/modules/json"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBBExecutorSchedulableCapacity(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/scheduler/capacity" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Breadboard-Project") != "prj_test" {
			t.Errorf("project header = %q", r.Header.Get("X-Breadboard-Project"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schedulable":2,"max_count":4,"as_of_ms":1731000000000}`))
	}))
	defer server.Close()

	profile := bbExecutorProfile{
		Name:    "gpu",
		Sandbox: map[string]any{"runtime": "sandbox-vm", "accelerator": map[string]any{"vendor": "nvidia"}},
	}
	config := bbExecutorConfig{
		Endpoint:    server.URL,
		Project:     "prj_test",
		ProfileAuth: &bbExecutorProfileAuth{},
		client:      server.Client(),
	}
	if err := config.initAPI(); err != nil {
		t.Fatal(err)
	}
	capacity, err := config.schedulableCapacity(context.Background(), profile, 4)
	if err != nil {
		t.Fatalf("schedulableCapacity() error = %v", err)
	}
	if capacity != 2 {
		t.Fatalf("schedulableCapacity() = %d, want 2", capacity)
	}
	requestSpec, ok := got["spec"].(map[string]any)
	if !ok || requestSpec["runtime"] != "sandbox-vm" {
		t.Fatalf("request spec = %#v", got["spec"])
	}
	if got["max_count"] != float64(4) {
		t.Fatalf("request max_count = %#v, want 4", got["max_count"])
	}
}

func TestBBExecutorSchedulableCapacityFailsClosed(t *testing.T) {
	for name, response := range map[string]string{
		"missing timestamp": `{"schedulable":2,"max_count":4}`,
		"negative count":    `{"schedulable":-1,"max_count":4,"as_of_ms":1731000000000}`,
		"over max":          `{"schedulable":5,"max_count":4,"as_of_ms":1731000000000}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(response))
			}))
			defer server.Close()
			config := bbExecutorConfig{Endpoint: server.URL, Project: "prj_test", ProfileAuth: &bbExecutorProfileAuth{}, client: server.Client()}
			if err := config.initAPI(); err != nil {
				t.Fatal(err)
			}
			if _, err := config.schedulableCapacity(context.Background(), bbExecutorProfile{Sandbox: map[string]any{}}, 4); err == nil {
				t.Fatal("schedulableCapacity() succeeded for invalid response")
			}
		})
	}
}

func TestBBExecutorReconcileStateUpdateUnmatchedSummary(t *testing.T) {
	state := &bbExecutorReconcileState{}

	changed, previouslyUnmatched := state.updateUnmatchedSummary("1 jobs for [\"macos-latest\"]")
	if !changed || previouslyUnmatched {
		t.Fatalf("first unmatched summary: changed=%t previouslyUnmatched=%t", changed, previouslyUnmatched)
	}

	changed, previouslyUnmatched = state.updateUnmatchedSummary("1 jobs for [\"macos-latest\"]")
	if changed || !previouslyUnmatched {
		t.Fatalf("unchanged unmatched summary: changed=%t previouslyUnmatched=%t", changed, previouslyUnmatched)
	}

	changed, previouslyUnmatched = state.updateUnmatchedSummary("")
	if !changed || !previouslyUnmatched {
		t.Fatalf("cleared unmatched summary: changed=%t previouslyUnmatched=%t", changed, previouslyUnmatched)
	}
}

func TestBBProfileExtends(t *testing.T) {
	load := func(t *testing.T, raw string) ([]bbExecutorProfile, error) {
		var profiles []bbExecutorProfile
		require.NoError(t, json.Unmarshal([]byte(raw), &profiles))
		return resolveBBProfileExtends(profiles)
	}
	const base = `{"name":"base","labels":["linux:host"],"runner_url":"https://x/amd64","runner_sha256":"aa","package_sets":["git-runner-docker"],"exec_user":"u",
		"sandbox":{"runtime":"sandbox-vm","cpu":{"arch":"amd64","vcpus":4},"allowed_hosts":["a","b"]}}`

	t.Run("merge", func(t *testing.T) {
		profiles, err := load(t, `[`+base+`,{"name":"arm","extends":"base","labels":["arm64:host"],"runner_url":"https://x/arm64","runner_sha256":"bb",
			"sandbox":{"cpu":{"arch":"arm64"},"allowed_hosts":["c"]}}]`)
		require.NoError(t, err)
		arm := profiles[1]
		assert.Equal(t, []string{"arm64:host"}, arm.Labels)
		assert.Equal(t, "https://x/arm64", arm.RunnerURL)
		assert.Equal(t, "bb", arm.RunnerSHA256)
		assert.Equal(t, []string{"git-runner-docker"}, arm.PackageSets)
		assert.Equal(t, "u", arm.ExecUser)
		assert.Equal(t, "sandbox-vm", arm.Sandbox["runtime"])
		assert.Equal(t, map[string]any{"arch": "arm64", "vcpus": float64(4)}, arm.Sandbox["cpu"])
		assert.Equal(t, []any{"c"}, arm.Sandbox["allowed_hosts"])
		parentCPU, ok := profiles[0].Sandbox["cpu"].(map[string]any)
		require.True(t, ok, "parent CPU settings must remain a map")
		assert.Equal(t, "amd64", parentCPU["arch"], "parent must not be mutated")
	})
	t.Run("repository scope and cpu override", func(t *testing.T) {
		profiles, err := load(t, `[ `+base+`,{"name":"rustfs-sm2","extends":"base","labels":["sm-standard-2:host"],
			"repositories":["BakeLab/rustfs"],"sandbox":{"cpu":{"vcpus":2}}}]`)
		require.NoError(t, err)
		profile := profiles[1]
		assert.Equal(t, []string{"BakeLab/rustfs"}, profile.Repositories)
		assert.Equal(t, map[string]any{"arch": "amd64", "vcpus": float64(2)}, profile.Sandbox["cpu"])
	})
	t.Run("multi level", func(t *testing.T) {
		profiles, err := load(t, `[{"name":"c","extends":"b","labels":["c:host"]},{"name":"b","extends":"base","labels":["b:host"],"sandbox":{"cpu":{"vcpus":8}}},`+base+`]`)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"arch": "amd64", "vcpus": float64(8)}, profiles[0].Sandbox["cpu"])
		assert.Equal(t, "https://x/amd64", profiles[0].RunnerURL)
	})
	t.Run("labels are not inherited", func(t *testing.T) {
		_, err := load(t, `[`+base+`,{"name":"arm","extends":"base"}]`)
		require.ErrorContains(t, err, "own labels")
	})
	t.Run("unknown parent", func(t *testing.T) {
		_, err := load(t, `[{"name":"arm","extends":"nope","labels":["a:host"]}]`)
		require.ErrorContains(t, err, "unknown")
	})
	t.Run("cycle", func(t *testing.T) {
		_, err := load(t, `[{"name":"a","extends":"b","labels":["a:host"]},{"name":"b","extends":"a","labels":["b:host"]}]`)
		require.ErrorContains(t, err, "cyclic")
	})
	t.Run("duplicate child name", func(t *testing.T) {
		_, err := load(t, `[`+base+`,{"name":"arm","extends":"base","labels":["a:host"]},{"name":"arm","labels":["b:host"]}]`)
		require.ErrorContains(t, err, "unique name")
	})
}
