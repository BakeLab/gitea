// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package notifications

import (
	stdctx "context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gitea.dev/tea/modules/config"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestRunNotificationsListMineDoesNotProbeGitRepository(t *testing.T) {
	gitPath := filepath.Join(t.TempDir(), "git")
	gitScript := "#!/bin/sh\necho 'git should not be called' >&2\nexit 1\n"
	if runtime.GOOS == "windows" {
		gitPath += ".bat"
		gitScript = "@echo git should not be called 1>&2\r\nexit /b 1\r\n"
	}
	require.NoError(t, os.WriteFile(gitPath, []byte(gitScript), 0o755))
	t.Setenv("PATH", filepath.Dir(gitPath))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/notifications", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	config.SetConfigForTesting(config.LocalConfig{
		Logins: []config.Login{{
			Name:    "default",
			URL:     server.URL,
			Token:   "token",
			User:    "user",
			Default: true,
		}},
	})

	cmd := cli.Command{
		Name:  CmdNotificationsList.Name,
		Flags: CmdNotificationsList.Flags,
	}
	require.NoError(t, cmd.Set("mine", "true"))
	require.NoError(t, cmd.Set("output", "json"))

	require.NoError(t, RunNotificationsList(stdctx.Background(), &cmd))
}
