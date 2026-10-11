// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"

	"gitea.dev/tea/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentCredentialReportsActualAccount(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	defer server.Close()
	deps, out, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"TEA_USER": "ALICE"})
	require.NoError(t, Execute(context.Background(), deps, []string{"doctor", "--json"}))
	require.Contains(t, out.String(), `"user":"alice"`)
	require.Contains(t, out.String(), `"user_source":"TEA_USER"`)
}

func TestDoctorDiscoversRepositoryBestEffort(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Git = &fakeGit{results: map[string]string{"remote": "origin", "remote get-url origin": server.URL + "/alice/project.git"}}
	require.NoError(t, Execute(context.Background(), deps, []string{"doctor", "--json=repo,repo_source"}))
	require.JSONEq(t, `{"repo":"alice/project","repo_source":"remote origin"}`, out.String())
	out.Reset()
	deps.Getenv = envFunc(map[string]string{"TEA_HOST": server.URL})
	require.Equal(t, clierr.Authentication, clierr.ExitCode(Execute(context.Background(), deps, []string{"doctor"})))
}
