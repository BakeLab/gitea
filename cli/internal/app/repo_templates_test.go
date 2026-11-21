// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepoLicenseAndGitignoreTemplates(t *testing.T) {
	var paths []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, strings.TrimPrefix(r.URL.Path, "/api/v1"))
		switch r.URL.Path {
		case "/api/v1/licenses":
			_, _ = w.Write([]byte(`[{"key":"Apache-2.0","name":"Apache-2.0","url":"u1"},{"key":"MIT","name":"MIT","url":"u2"}]`))
		case "/api/v1/licenses/MIT":
			_, _ = w.Write([]byte(`{"key":"MIT","name":"MIT","url":"u2","implementation":"Create a LICENSE file","body":"MIT License\n\nCopyright (c) <year>"}`))
		case "/api/v1/gitignore/templates":
			_, _ = w.Write([]byte(`["Go","Python"]`))
		case "/api/v1/gitignore/templates/Raw":
			_, _ = w.Write([]byte(`{"name":"Raw","source":"Icon\r\r\n\f\u001b[31mred\n"}`))
		case "/api/v1/gitignore/templates/Go":
			_, _ = w.Write([]byte("{\"name\":\"Go\",\"source\":\"*.exe\\n*.test\\n\"}"))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}
	}))
	defer server.Close()
	run := func(args ...string) (string, error) {
		paths = nil
		deps, out, _ := serverDeps(t, server.URL)
		err := Execute(context.Background(), deps, append([]string{"repo"}, args...))
		return out.String(), err
	}

	out, err := run("license", "list")
	require.NoError(t, err)
	require.Equal(t, "Apache-2.0\tApache-2.0\nMIT\tMIT\n", out)
	out, err = run("license", "list", "--json", "key")
	require.NoError(t, err)
	require.JSONEq(t, `[{"key":"Apache-2.0"},{"key":"MIT"}]`, out)

	out, err = run("license", "view", "MIT")
	require.NoError(t, err)
	require.Equal(t, "MIT License\n\nCopyright (c) <year>\n", out, "only the text, so that it can be redirected to a file")
	out, err = run("license", "view", "mit", "--json", "key,implementation")
	require.NoError(t, err)
	require.JSONEq(t, `{"key":"MIT","implementation":"Create a LICENSE file"}`, out)
	require.Equal(t, []string{"/licenses/mit", "/licenses", "/licenses/MIT"}, paths, "a key is matched without regard to case")
	_, err = run("license", "view", "nope")
	require.ErrorContains(t, err, "license nope not found")

	out, err = run("gitignore", "list")
	require.NoError(t, err)
	require.Equal(t, "Go\nPython\n", out)
	out, err = run("gitignore", "list", "--json", "name")
	require.NoError(t, err)
	require.JSONEq(t, `[{"name":"Go"},{"name":"Python"}]`, out)
	out, err = run("gitignore", "view", "go")
	require.NoError(t, err)
	require.Equal(t, "*.exe\n*.test\n", out)
	out, err = run("gitignore", "view", "Go", "--json")
	require.NoError(t, err)
	require.JSONEq(t, `{"name":"Go","source":"*.exe\n*.test\n"}`, out)
	_, err = run("gitignore", "view", "nope")
	require.ErrorContains(t, err, ".gitignore template nope not found")

	out, err = run("gitignore", "view", "Raw")
	require.NoError(t, err)
	require.Equal(t, "Icon\r\r\n\f\x1b[31mred\n", out, "a file gets the bytes of the template")
	deps, terminalOut, _ := serverDeps(t, server.URL)
	deps.Streams.OutTTY = true
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "gitignore", "view", "Raw"}))
	require.NotContains(t, terminalOut.String(), "\x1b", "a terminal gets no control sequences from the server")
}
