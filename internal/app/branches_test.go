// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBranchNames(t *testing.T) {
	for _, name := range []string{"main", "feature/next", "release-1.0", "topic.locked"} {
		require.NoError(t, validateBranch(name), name)
	}
	for _, name := range []string{"", "@", "-flag", "/root", "trailing/", "trailing.", "two..dots", "ref@{1}", "a//b", "a/.hidden", "topic.lock", "space here", "bad\x00ref", "bad\x7fref"} {
		require.Error(t, validateBranch(name), name)
	}
}

func TestBulkBranchProtectionContinuesAfterFailure(t *testing.T) {
	var names []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/owner/repo/branch_protections/")
		names = append(names, name)
		if name == "failed" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"branch", "unprotect", "failed", "succeeded", "-R", "owner/repo", "--no-input"})
	require.Error(t, err)
	require.Empty(t, names)
	err = Execute(context.Background(), deps, []string{"branch", "unprotect", "failed", "succeeded", "-R", "owner/repo", "--yes"})
	require.ErrorContains(t, err, "unprotect failed")
	require.Equal(t, []string{"failed", "succeeded"}, names)
	require.Empty(t, out.String())
	require.Equal(t, "✓ Unprotected succeeded in owner/repo\n", diagnostics.String())
}

func TestBranchProtectChecksTheBranchAndIsIdempotent(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/x/branches/main", "GET /api/v1/repos/alice/x/branches/dev":
			_, _ = w.Write([]byte(`{"name":"main"}`))
		case "POST /api/v1/repos/alice/x/branch_protections":
			var rule struct {
				Name string `json:"rule_name"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&rule))
			if rule.Name == "main" {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"message":"Branch protection already exist"}`))
				return
			}
			_, _ = w.Write([]byte(`{"rule_name":"dev"}`))
		case "DELETE /api/v1/repos/alice/x/branch_protections/dev":
			w.WriteHeader(http.StatusNotFound)
		case "GET /api/v1/repos/alice/x":
			_, _ = w.Write([]byte(`{"full_name":"alice/x"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, diagnostics := repoTTYDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"branch", "protect", "nope", "-R", "alice/x"})
	require.EqualError(t, err, `protect nope: branch "nope" not found in alice/x`)
	require.Equal(t, []string{"GET /api/v1/repos/alice/x/branches/nope", "GET /api/v1/repos/alice/x"}, requests)
	require.NoError(t, Execute(context.Background(), deps, []string{"branch", "protect", "main", "dev", "-R", "alice/x"}))
	require.Equal(t, "! main is already protected in alice/x; change its rule on the web\n"+
		"✓ Protected dev in alice/x (1 approval required, no direct pushes)\n", diagnostics.String())
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"branch", "unprotect", "dev", "-R", "alice/x", "--yes"}))
	require.Equal(t, "! dev is not protected in alice/x\n", diagnostics.String())
}

func TestBranchCreateFromATag(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requirePayload(t, `{"new_branch_name":"hotfix","old_ref_name":"v1.0.0"}`, r.Body)
		_, _ = w.Write([]byte(`{"name":"hotfix","commit":{"id":"abcdef123"}}`))
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"branch", "create", "hotfix", "--source", "v1.0.0", "-R", "alice/x"}))
	require.Empty(t, out.String())
	require.Equal(t, "✓ Created branch hotfix from v1.0.0 in alice/x at abcdef1\n", diagnostics.String())
}

func TestBranchDeleteTakesSeveralBranches(t *testing.T) {
	var deleted []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		deleted = append(deleted, strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/x/branches/"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	deps, _, diagnostics := repoTTYDeps(t, server.URL)
	prompt := &scriptedPrompter{accept: true}
	deps.Prompter, deps.Streams.InTTY, deps.Streams.ErrTTY = prompt, true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"branch", "rm", "old-a", "old-b", "-R", "alice/x"}))
	require.Equal(t, []string{"Delete 2 branches (old-a, old-b) from alice/x?"}, prompt.confirmations)
	require.Equal(t, []string{"old-a", "old-b"}, deleted)
	require.Equal(t, "✓ Deleted branch old-a from alice/x\n✓ Deleted branch old-b from alice/x\n", diagnostics.String())
}

func TestBranchListPipedKeepsPlainNamesAndFullCommits(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/x":
			_, _ = w.Write([]byte(`{"full_name":"alice/x","default_branch":"main"}`))
		default:
			_, _ = w.Write([]byte(`[{"name":"main","commit":{"id":"0123456789abcdef"}}]`))
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"branch", "list", "-R", "alice/x"}))
	require.Contains(t, out.String(), "main\t0123456789abcdef\t")
	require.NotContains(t, out.String(), "(default)")
	deps, out, _ = repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"branch", "list", "-R", "alice/x"}))
	require.Contains(t, out.String(), "main (default)")
	require.Contains(t, out.String(), "0123456")
	require.NotContains(t, out.String(), "0123456789")
}

func TestBranchCreateWithAnUnknownSourceIsNotFound(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"object does not exist [id: nope, rel_path: ]"}`))
	}))
	defer server.Close()
	deps, _, _ := repoTTYDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"branch", "create", "hotfix", "--source", "nope", "-R", "alice/x"})
	require.EqualError(t, err, `branch, tag or commit "nope" not found in alice/x`)
}

func TestBranchDeleteAndProtectNameAMissingRepository(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	deps, _, _ := repoTTYDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"branch", "delete", "old", "-R", "alice/typo", "--yes"})
	require.EqualError(t, err, "delete branch old: repository alice/typo not found")
	err = Execute(context.Background(), deps, []string{"branch", "protect", "old", "-R", "alice/typo"})
	require.EqualError(t, err, "protect old: repository alice/typo not found")
}

func TestBranchProtectRejectsATeamOfAnotherOrganization(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	deps, _, _ := repoTTYDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"branch", "protect", "main", "--allow-push", "other/devs", "-R", "acme/x"})
	require.EqualError(t, err, "team other/devs is not in acme, the owner of acme/x")
}
