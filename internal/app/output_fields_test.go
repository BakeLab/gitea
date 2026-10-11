// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitea.dev/tea/internal/clierr"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestCommandsWithoutOutputRejectJSON runs every leaf command with --json against a server
// that accepts everything. A command that then succeeds without printing returns no resource
// and must reject --json up front through textOutput.
func TestCommandsWithoutOutputRejectJSON(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(server.Close)
	deps, _, _ := testDeps(t)
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		for _, sub := range command.Commands() {
			walk(sub)
		}
		if command.HasSubCommands() || command.Annotations["tea.text"] != "" {
			return
		}
		path := strings.Fields(command.CommandPath())[1:]
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			t.Parallel()
			// Numeric and name-shaped positionals reach different validators.
			for _, prefix := range []string{"", "x"} {
				var args []string
				for command.Args != nil && command.Args(command, args) != nil && len(args) < 3 {
					args = append(args, prefix+strconv.Itoa(len(args)+1))
				}
				deps, out, _ := testDeps(t)
				deps.Getenv = serverEnv(server.URL, map[string]string{"TEA_REPO": "alice/project"})
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				err := Execute(ctx, deps, slices.Concat(path, args, []string{"--json", "--yes", "--no-input"}))
				cancel()
				if err == nil && out.Len() == 0 {
					t.Errorf("%v succeeds with --json but prints nothing; annotate it with textOutput", args)
				}
			}
		})
	}
	walk(NewRoot(deps))
}

func TestJSONAllowlistsAreNeverEmpty(t *testing.T) {
	deps, _, _ := testDeps(t)
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if fields := commandOutputFields(cmd); fields != nil {
			require.NotEmpty(t, fields, cmd.CommandPath())
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(NewRoot(deps))
}

func TestOutputFieldAllowlists(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		fields []string
		bad    string
	}{
		{[]string{"config", "list"}, []string{"git_protocol", "ssh_key_fingerprint"}, "not_a_preference"},
		{[]string{"pr", "checkout", "1"}, []string{"branch", "commit", "detached", "remote", "tracking"}, "id"},
		{[]string{"pr", "view", "1"}, []string{"head_repo", "head_remote_deleted", "mergeable"}, "unknown"},
		{[]string{"repo", "sync"}, []string{"branch", "commit", "remote"}, "id"},
		{[]string{"repo", "view"}, []string{"parent"}, "unknown"},
		{[]string{"run", "view", "1"}, []string{"event", "actor", "commit", "attempt", "url"}, "unknown"},
		{[]string{"workflow", "run", "ci.yml"}, []string{"id", "url"}, "name"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			deps, out, _ := testDeps(t)
			command, _, err := NewRoot(deps).Find(tc.args)
			require.NoError(t, err)
			fields := commandOutputFields(command)
			for _, name := range tc.fields {
				require.Contains(t, fields, name)
			}
			err = Execute(context.Background(), deps, append(tc.args, "--json="+tc.bad, "--no-input"))
			require.Equal(t, clierr.Usage, clierr.ExitCode(err))
			require.ErrorContains(t, err, `"`+tc.bad+`"`)
			require.Empty(t, out.String())
		})
	}
}

func TestJSONFieldsFollowTags(t *testing.T) {
	type item struct {
		ID       int64  `json:"id"`
		Optional string `json:"optional,omitempty"`
		Hidden   string `json:"-"`
		Untagged string
	}
	require.Equal(t, []string{"id", "optional"}, jsonFields[item]())
}
