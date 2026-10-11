// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArtifactDownloadsValidateDestinationAndKeepBinaryOutputClean(t *testing.T) {
	requests := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/actions/artifacts/9/zip":
			require.Equal(t, "token token", r.Header.Get("Authorization"))
			http.Redirect(w, r, "/signed?signature=private", http.StatusFound)
		case "/signed":
			require.Empty(t, r.Header.Get("Authorization"))
			_, _ = w.Write([]byte{0x50, 0x4b, 0, 0xff})
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	args := []string{"run", "artifact", "download", "9", "-R", "owner/repo", "--output-file", "-"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, []byte{0x50, 0x4b, 0, 0xff}, out.Bytes())
	require.Empty(t, diagnostics.String())
	output := filepath.Join(t.TempDir(), "artifact.zip")
	args[len(args)-1] = output
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, args))
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Equal(t, []byte{0x50, 0x4b, 0, 0xff}, data)
	require.Empty(t, out.String())
	before := requests
	err = Execute(context.Background(), deps, args)
	require.ErrorContains(t, err, "already exists")
	require.Equal(t, before, requests)
	args[len(args)-1] = "-"
	err = Execute(context.Background(), deps, append(args, "--json"))
	require.ErrorContains(t, err, "binary")
	require.Equal(t, before, requests)
}
