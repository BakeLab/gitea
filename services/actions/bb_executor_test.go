// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitea.dev/modules/json"
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
				_, _ = w.Write([]byte(response))
			}))
			defer server.Close()
			config := bbExecutorConfig{Endpoint: server.URL, Project: "prj_test", ProfileAuth: &bbExecutorProfileAuth{}, client: server.Client()}
			if _, err := config.schedulableCapacity(context.Background(), bbExecutorProfile{Sandbox: map[string]any{}}, 4); err == nil {
				t.Fatal("schedulableCapacity() succeeded for invalid response")
			}
		})
	}
}
