// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/extensions"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestGiteaVersionRequirements(t *testing.T) {
	var versionRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/version":
			versionRequests.Add(1)
			_, _ = w.Write([]byte(`{"version":"28.2.0"}`))
		case "/api/v1/user":
			_, _ = w.Write([]byte(`{"login":"alice"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	run := func(args ...string) (string, error) {
		versionRequests.Store(0)
		deps, out, _ := serverDeps(t, server.URL)
		deps.ServerVersion = nil
		root, a := newRoot(deps)
		group := &cobra.Command{Use: "probe", Short: "Probe"}
		runE := func(cmd *cobra.Command, _ []string) error {
			_, err := a.resolve(cmd.Context(), false)
			return err
		}
		group.AddCommand(
			&cobra.Command{Use: "plain", Short: "Plain", RunE: runE},
			requireGitea(&cobra.Command{Use: "old", Short: "Old", RunE: runE}, "28.1"),
			requireGitea(&cobra.Command{Use: "new", Short: "New", RunE: runE}, "29"),
		)
		withFlag := &cobra.Command{Use: "flag", Short: "Flag", RunE: runE}
		withFlag.Flags().Bool("fancy", false, "Be fancy")
		group.AddCommand(requireGiteaFlag(withFlag, "fancy", "29.0.1"))
		root.AddCommand(requireGitea(&cobra.Command{Use: "gated", Short: "Gated"}, "30"))
		root.AddCommand(group)
		gated, _, _ := root.Find([]string{"gated"})
		gated.AddCommand(&cobra.Command{Use: "child", Short: "Child", RunE: runE})
		root.SetArgs(args)
		err := root.Execute()
		return out.String(), err
	}

	_, err := run("probe", "plain")
	require.NoError(t, err)
	require.Zero(t, versionRequests.Load(), "commands without a requirement do not ask for the version")

	_, err = run("probe", "old")
	require.NoError(t, err)

	_, err = run("probe", "new")
	require.ErrorContains(t, err, "gt probe new requires Gitea 29 or newer; ")
	require.ErrorContains(t, err, "runs Gitea 28.2.0")
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))

	_, err = run("probe", "flag")
	require.NoError(t, err, "an unset flag needs no version")
	_, err = run("probe", "flag", "--fancy")
	require.ErrorContains(t, err, "--fancy requires Gitea 29.0.1 or newer")

	_, err = run("gated", "child")
	require.ErrorContains(t, err, "gt gated child requires Gitea 30 or newer", "subcommands inherit their group's requirement")

	help, _ := run("probe", "new", "--help")
	require.Contains(t, help, "Requires Gitea 29 or newer.")
	help, _ = run("probe", "flag", "--help")
	require.Contains(t, help, "Be fancy (requires Gitea 29.0.1 or newer)")
}

func TestGiteaVersionComparison(t *testing.T) {
	for _, tc := range []struct {
		version, minimum string
		older            bool
	}{
		{"28.3.0", "29", true},
		{"29.0.0", "29", false},
		{"29.0.0+dev-160-g55186ca39b", "29", false},
		{"11.0.0+gitea-1.22.0", "29", true},
		{"31.0.0+gitea-1.22.0", "29", true},
		{"11.0.0+gitea-29.0.0", "29", false},
	} {
		require.Equal(t, tc.older, extensions.Older(comparableVersion(tc.version), tc.minimum), tc.version)
	}
}

func TestGiteaVersionTablesApply(t *testing.T) {
	t.Cleanup(func() { commandVersions, flagVersions = map[string]string{}, map[string]string{} })
	commandVersions = map[string]string{"issue list": "29"}
	flagVersions = map[string]string{"issue create --title": "30"}
	deps, _, _ := testDeps(t)
	root := NewRoot(deps)
	list, _, err := root.Find([]string{"issue", "list"})
	require.NoError(t, err)
	require.Equal(t, "29", list.Annotations[giteaAnnotation])
	create, _, err := root.Find([]string{"issue", "create"})
	require.NoError(t, err)
	require.Equal(t, []string{"30"}, create.Flags().Lookup("title").Annotations[giteaAnnotation])

	commandVersions = map[string]string{"issue renamed": "29"}
	require.Panics(t, func() { NewRoot(deps) }, "an entry left behind by a rename")
}
