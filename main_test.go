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
	require.Equal(t, 1, strings.Count(stderr.String(), "Error:"))
	require.NotContains(t, stderr.String(), "disposable-test-token")
}
