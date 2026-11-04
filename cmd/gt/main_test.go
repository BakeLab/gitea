// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/base64"
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
	"gitea.dev/cli/internal/clierr"
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
	require.Equal(t, 3, run(context.Background(), deps, []string{"auth", "logout"}, app.Execute))
	require.Equal(t, "Canceled.\n", stderr.String())

	stderr.Reset()
	deps.Getenv = func(key string) string {
		return map[string]string{"GITEA_TOKEN": "token", "GITEA_CLI_HOST": server.URL}[key]
	}
	require.Equal(t, 1, run(context.Background(), deps, []string{"issue", "view", "9", "-R", "alice/project", "--no-input"}, app.Execute))
	require.Equal(t, "Error: issue #9 not found in alice/project\n", stderr.String())

	stderr.Reset()
	deps.Getenv = func(key string) string {
		return map[string]string{"GITEA_TOKEN": "token", "GITEA_CLI_HOST": server.URL, "GITEA_CLI_ERROR_FORMAT": "json"}[key]
	}
	require.Equal(t, 3, run(context.Background(), deps, []string{"repo", "archive", "-R", "alice/project", "--interactive"}, app.Execute))
	require.JSONEq(t, `{"error":{"code":3,"kind":"canceled","message":"canceled"}}`, stderr.String())
}

type reviewFormStub struct{}

func (reviewFormStub) CreateReview(context.Context, terminal.ReviewFormChoices) (terminal.ReviewFormValues, error) {
	return terminal.ReviewFormValues{Event: "COMMENT", Body: "Self note"}, nil
}

func TestRunReportsWhereADeclinedReviewWasSaved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/user" {
			_, _ = w.Write([]byte(`{"login":"alice"}`))
			return
		}
		if r.URL.Path == "/api/v1/repos/alice/project/pulls/3/reviews" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`{"number":3,"title":"Add one","user":{"login":"alice"},"head":{"ref":"feat/one"},"base":{"ref":"main"}}`))
	}))
	defer server.Close()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp) // Windows reads TMP rather than TMPDIR
	var stderr bytes.Buffer
	store := settings.New(t.TempDir())
	deps := app.Dependencies{
		Settings: store, Credentials: &auth.Store{Dir: store.Dir}, Client: transport.New, Prompter: decliningPrompter{}, ReviewForm: reviewFormStub{},
		Streams: terminal.Streams{In: &bytes.Buffer{}, Out: &bytes.Buffer{}, ErrOut: &stderr, InTTY: true, ErrTTY: true},
		Getenv: func(key string) string {
			return map[string]string{"GITEA_TOKEN": "token", "GITEA_CLI_HOST": server.URL}[key]
		},
	}
	require.Equal(t, 3, run(context.Background(), deps, []string{"pr", "review", "3", "-R", "alice/project"}, app.Execute))
	files, err := filepath.Glob(filepath.Join(tmp, "gt-draft-*.md"))
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "Canceled.\nDraft saved at "+files[0]+"\n", stderr.String())
}

func TestSecondInterruptStopsACommandBlockedOnStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt delivery is not supported by Windows processes")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "gt")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "build: %s", output)
	command := exec.CommandContext(ctx, binary, "auth", "login", "--host", "http://127.0.0.1:1", "--with-token", "--no-input")
	command.Env = append(os.Environ(), "GITEA_CLI_CONFIG_DIR="+t.TempDir(), "GITEA_TOKEN=")
	stdin, err := command.StdinPipe()
	require.NoError(t, err)
	defer stdin.Close()
	require.NoError(t, command.Start())
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	// Reading the token from stdin ignores the canceled context, so only a second signal ends gt.
	time.Sleep(time.Second)
	require.NoError(t, command.Process.Signal(os.Interrupt))
	time.Sleep(500 * time.Millisecond)
	require.NoError(t, command.Process.Signal(os.Interrupt))
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("gt ignored the second interrupt while waiting on stdin")
	}
}

func TestRunDropsTerminalControlsFromErrors(t *testing.T) {
	definition := base64.StdEncoding.EncodeToString([]byte("on:\n  workflow_dispatch:\n    inputs:\n      target:\n        required: true\n"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/user":
			_, _ = w.Write([]byte(`{"login":"alice"}`))
		case "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_issues":true}`))
		case "/api/v1/repos/alice/project/actions/workflows":
			_, _ = w.Write([]byte(`{"workflows":[{"id":"ci.yml","name":"CI\u001b]0;PWNED\u0007","path":".gitea/workflows/ci.yml","state":"active"}]}`))
		case "/api/v1/repos/alice/project/contents/.gitea/workflows":
			_, _ = w.Write([]byte(`[{"name":"ci.yml","path":".gitea/workflows/ci.yml","type":"file"}]`))
		case "/api/v1/repos/alice/project/contents/.gitea/workflows/ci.yml":
			_, _ = w.Write([]byte(`{"type":"file","encoding":"base64","content":"` + definition + `"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	var stderr bytes.Buffer
	deps := app.Dependencies{
		Settings: settings.New(t.TempDir()), Credentials: &auth.Store{Dir: t.TempDir()}, Client: transport.New, Prompter: decliningPrompter{},
		Streams: terminal.Streams{In: &bytes.Buffer{}, Out: &bytes.Buffer{}, ErrOut: &stderr},
		Getenv: func(key string) string {
			return map[string]string{"GITEA_TOKEN": "token", "GITEA_CLI_HOST": server.URL}[key]
		},
	}
	require.Equal(t, 2, run(context.Background(), deps, []string{"workflow", "run", "ci.yml", "--ref", "main", "-R", "alice/project", "--no-input"}, app.Execute))
	require.Contains(t, stderr.String(), "(ci.yml) requires input target; pass -f target=<value>")
	require.NotContains(t, stderr.String(), "\x1b")
	require.NotContains(t, stderr.String(), "\x07")
}

func TestRunKeepsTheExitCodeOfAnInterruptedExtension(t *testing.T) {
	var stderr bytes.Buffer
	deps := app.Dependencies{Streams: terminal.Streams{ErrOut: &stderr}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Equal(t, 7, run(ctx, deps, nil, func(context.Context, app.Dependencies, []string) error { return clierr.Quiet(7) }))
	require.Empty(t, stderr.String())
	require.Equal(t, clierr.Interrupted, run(ctx, deps, nil, func(context.Context, app.Dependencies, []string) error { return errors.New("read failed") }))
}

func TestRunReportsFailuresInTheConfiguredFormat(t *testing.T) {
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
	var stderr bytes.Buffer
	deps := app.Dependencies{
		Settings: settings.New(t.TempDir()), Credentials: &auth.Store{Dir: t.TempDir()}, Client: transport.New, Prompter: decliningPrompter{},
		Streams: terminal.Streams{In: &bytes.Buffer{}, Out: &bytes.Buffer{}, ErrOut: &stderr},
		Getenv: func(key string) string {
			return map[string]string{"GITEA_TOKEN": "token", "GITEA_CLI_HOST": server.URL}[key]
		},
	}
	// --json alone keeps text errors, as gh does.
	require.Equal(t, 1, run(context.Background(), deps, []string{"issue", "view", "9", "-R", "alice/project", "--no-input", "--json"}, app.Execute))
	require.Equal(t, "Error: issue #9 not found in alice/project\n", stderr.String())

	env := map[string]string{"GITEA_TOKEN": "token", "GITEA_CLI_HOST": server.URL, "GITEA_CLI_ERROR_FORMAT": "json"}
	deps.Getenv = func(key string) string { return env[key] }
	stderr.Reset()
	require.Equal(t, 1, run(context.Background(), deps, []string{"issue", "view", "9", "-R", "alice/project", "--no-input"}, app.Execute))
	require.JSONEq(t, `{"error":{"code":1,"kind":"failure","message":"issue #9 not found in alice/project","status":404}}`, stderr.String())
	require.Equal(t, 1, strings.Count(stderr.String(), "\n"))

	stderr.Reset()
	require.Equal(t, 2, run(context.Background(), deps, []string{"issue", "view", "--bogus"}, app.Execute))
	require.Contains(t, stderr.String(), `"kind":"usage"`)
	require.NotContains(t, stderr.String(), "Error:")

	env["GITEA_CLI_ERROR_FORMAT"] = "yaml"
	stderr.Reset()
	require.Equal(t, 2, run(context.Background(), deps, []string{"issue", "view", "9", "-R", "alice/project", "--no-input"}, app.Execute))
	require.True(t, strings.HasPrefix(stderr.String(), "Error: "))
}
