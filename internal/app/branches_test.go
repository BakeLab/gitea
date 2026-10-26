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
		case "PATCH /api/v1/repos/alice/x/branch_protections/main":
			// Only the settings the flags name change.
			requirePayload(t, `{"required_approvals":3,"enable_push":true,"enable_push_whitelist":true,"push_whitelist_usernames":["bob"],"push_whitelist_teams":[]}`, r.Body)
			_, _ = w.Write([]byte(`{"rule_name":"main","push_whitelist_usernames":["bob"]}`))
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
	require.Equal(t, "! main is already protected in alice/x; pass the settings to change\n"+
		"✓ Protected dev in alice/x (1 approval required, no direct pushes)\n", diagnostics.String())
	diagnostics.Reset()
	requests = nil
	require.NoError(t, Execute(context.Background(), deps, []string{"branch", "protect", "main", "--required-approvals", "3", "--allow-push", "bob", "-R", "alice/x"}))
	require.Equal(t, "✓ Updated the protection of main in alice/x\n", diagnostics.String())
	require.Contains(t, requests, "PATCH /api/v1/repos/alice/x/branch_protections/main")
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"branch", "unprotect", "dev", "-R", "alice/x", "--yes"}))
	require.Equal(t, "! dev is not protected in alice/x\n", diagnostics.String())
}

func TestBranchProtectionOfANameWithGlobCharacters(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.EscapedPath())
		switch r.Method + " " + r.URL.EscapedPath() {
		case "GET /api/v1/repos/alice/x/branches/x%7By%7D":
			_, _ = w.Write([]byte(`{"name":"x{y}"}`))
		case "GET /api/v1/repos/alice/x/branches/release/1":
			_, _ = w.Write([]byte(`{"name":"release/1","protected":true}`))
		case "POST /api/v1/repos/alice/x/branch_protections":
			requirePayload(t, `{"rule_name":"x\\{y\\}","required_approvals":1,"enable_push":false,"enable_push_whitelist":false,"enable_status_check":false,"block_on_rejected_reviews":false,"dismiss_stale_approvals":false}`, r.Body)
			_, _ = w.Write([]byte(`{"rule_name":"x\\{y\\}"}`))
		case "DELETE /api/v1/repos/alice/x/branch_protections/x%5C%7By%5C%7D":
			w.WriteHeader(http.StatusNoContent)
		case "GET /api/v1/repos/alice/x":
			_, _ = w.Write([]byte(`{"full_name":"alice/x"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, diagnostics := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"branch", "protect", "x{y}", "-R", "alice/x"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"branch", "unprotect", "x{y}", "release/1", "-R", "alice/x", "--yes"}))
	require.Contains(t, requests, "DELETE /api/v1/repos/alice/x/branch_protections/x%5C%7By%5C%7D")
	require.Equal(t, "✓ Protected x{y} in alice/x (1 approval required, no direct pushes)\n"+
		"✓ Unprotected x{y} in alice/x\n"+
		"! release/1 has no protection rule of its own in alice/x; a pattern rule still protects it\n", diagnostics.String())
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
		name := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/x/branches/")
		if r.Method == http.MethodGet {
			if name == "nope" || name == "gone" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"name":"` + name + `"}`))
			return
		}
		require.Equal(t, http.MethodDelete, r.Method)
		deleted = append(deleted, name)
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
	// A missing branch among several deletes none of them.
	deleted = nil
	err := Execute(context.Background(), deps, []string{"branch", "rm", "old-a", "nope", "gone", "-R", "alice/x", "--yes"})
	require.EqualError(t, err, "branch \"nope\" not found in alice/x\nbranch \"gone\" not found in alice/x")
	require.Empty(t, deleted)
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

func TestBranchProtectRejectsEmptyNameLists(t *testing.T) {
	for _, tc := range []struct{ flag, want string }{
		{"--status-check=", "status check names cannot be empty"},
		{"--status-check=ci,", "status check names cannot be empty"},
		{"--allow-push=", "pusher names cannot be empty"},
	} {
		requireUsageBeforeRequests(t, tc.want, "branch", "protect", "main", tc.flag, "-R", "acme/x")
	}
}

func TestBranchProtectFailsWhenGiteaDropsPushers(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/acme/x/branches/main":
			_, _ = w.Write([]byte(`{"name":"main"}`))
		case "POST /api/v1/repos/acme/x/branch_protections":
			// Gitea leaves out users and teams that cannot write to the repository.
			_, _ = w.Write([]byte(`{"rule_name":"main","push_whitelist_usernames":["alice"],"push_whitelist_teams":["devs"]}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, diagnostics := repoTTYDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"branch", "protect", "main", "--allow-push", "Alice,bob,acme/devs,acme/ops", "-R", "acme/x"})
	require.EqualError(t, err, "protect main: the rule was created without bob, team acme/ops among those who may push, as only users and teams with write access to acme/x can push; "+
		"to retry, grant them write access and protect again with the same --allow-push")
	require.Empty(t, diagnostics.String())
}

func TestBranchProtectSaysWhenNobodyCanPush(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/repos/acme/x/branch_protections":
			_, _ = w.Write([]byte(`{"rule_name":"release/*","enable_push":true,"enable_push_whitelist":true}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, _ := repoTTYDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"branch", "protect", "--pattern", "release/*", "--allow-push", "bob", "-R", "acme/x"})
	require.EqualError(t, err, "protect release/*: the rule was created without bob among those who may push, as only users and teams with write access to acme/x can push; "+
		"nobody can push to release/* now; to retry, grant them write access and protect again with the same --allow-push")
}
