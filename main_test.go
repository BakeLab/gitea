// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"gitea.dev/cli/internal/app"
	"gitea.dev/cli/internal/auth"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignalInterruptReturns130(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt delivery is not supported by Windows processes")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "gt")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "build: %s", output)
	started := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// require would call FailNow off the test goroutine and leave the test waiting for its deadline.
		assert.Equal(t, "/api/v1/user", r.URL.Path)
		once.Do(func() { close(started) })
		<-r.Context().Done()
	}))
	defer server.Close()
	command := exec.CommandContext(ctx, binary, "api", "version", "--no-input")
	environment := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GITEA_CLI_") || strings.HasPrefix(key, "GITEA_") {
			continue
		}
		environment = append(environment, entry)
	}
	command.Env = append(environment, "GITEA_CLI_CONFIG_DIR="+t.TempDir(), "GITEA_CLI_HOST="+server.URL, "GITEA_TOKEN=disposable-test-token")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	require.NoError(t, command.Start())
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("CLI did not reach the cancellable HTTP request")
	}
	require.NoError(t, command.Process.Signal(os.Interrupt))
	select {
	case err := <-done:
		var exit *exec.ExitError
		require.True(t, errors.As(err, &exit))
		require.Equal(t, 130, exit.ExitCode())
	case <-ctx.Done():
		t.Fatal("CLI did not exit after interrupt")
	}
	require.Empty(t, stdout.String())
	require.Empty(t, stderr.String(), "an interrupt exits silently")
}

type decliningPrompter struct{}

func (decliningPrompter) Ask(context.Context, string, func(string) error) (string, error) {
	return "", nil
}

func (decliningPrompter) AskText(context.Context, string, string) (string, error) {
	return "", nil
}

func (decliningPrompter) SecretInput(context.Context, string) (string, error) {
	return "", nil
}

func (decliningPrompter) ConfirmDefault(context.Context, string, bool) (bool, error) {
	return false, nil
}

func TestRunReportsDeclinedConfirmationsAndExplainedFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/user":
			_, _ = w.Write([]byte(`{"login":"alice"}`))
		case "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_issues":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}
	}))
	defer server.Close()
	store := settings.New(t.TempDir())
	require.NoError(t, store.Update(settings.TargetHosts, func(s *settings.Snapshot) error {
		s.Hosts.Hosts[server.URL] = settings.Host{User: "alice", Users: map[string]settings.Account{"alice": {AuthMethod: settings.AuthToken, CredentialStore: settings.StoreFile}}}
		return nil
	}))
	var stderr bytes.Buffer
	deps := app.Dependencies{
		Settings: store, Credentials: &auth.Store{Dir: store.Dir}, Client: transport.New, Prompter: decliningPrompter{},
		Streams: terminal.Streams{In: &bytes.Buffer{}, Out: &bytes.Buffer{}, ErrOut: &stderr, InTTY: true, ErrTTY: true},
		Getenv:  func(string) string { return "" },
	}
	require.Equal(t, 3, run(context.Background(), deps, []string{"auth", "logout"}))
	require.Equal(t, "Canceled.\n", stderr.String())

	stderr.Reset()
	deps.Getenv = func(key string) string {
		return map[string]string{"GITEA_TOKEN": "token", "GITEA_CLI_HOST": server.URL}[key]
	}
	require.Equal(t, 1, run(context.Background(), deps, []string{"issue", "view", "9", "-R", "alice/project", "--no-input"}))
	require.Equal(t, "Error: issue #9 not found in alice/project\n", stderr.String())
}
