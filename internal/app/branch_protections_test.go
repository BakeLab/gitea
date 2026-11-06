// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	mainRuleJSON    = `{"rule_name":"main","branch_name":"main","enable_push":true,"enable_push_whitelist":true,"push_whitelist_usernames":["alice"],"push_whitelist_teams":["release"],"enable_status_check":true,"status_check_contexts":["ci/build","ci/test"],"required_approvals":2,"block_on_rejected_reviews":true,"dismiss_stale_approvals":true,"enable_merge_whitelist":true,"merge_whitelist_usernames":["bob"],"created_at":"2026-10-01T10:00:00Z","updated_at":"2026-10-02T10:00:00Z"}`
	releaseRuleJSON = `{"rule_name":"release/*","enable_push":false,"enable_status_check":false,"status_check_contexts":["old/check"],"required_approvals":0,"enable_force_push":true,"require_signed_commits":true}`
	openRuleJSON    = `{"rule_name":"dev","enable_push":true,"enable_push_whitelist":false,"required_approvals":1,"enable_status_check":true,"enable_force_push":true,"enable_force_push_allowlist":true,"force_push_allowlist_usernames":["carol"],"force_push_allowlist_deploy_keys":true,"ignore_stale_approvals":true}`
)

func TestBranchProtections(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		mu.Unlock()
		switch path := strings.TrimPrefix(r.URL.EscapedPath(), "/api/v1/repos/alice/project"); path {
		case "":
			_, _ = w.Write([]byte(`{"full_name":"alice/project"}`))
		case "/branch_protections": // every rule at once, whatever the page
			_, _ = w.Write([]byte(`[` + mainRuleJSON + `,` + releaseRuleJSON + `,` + openRuleJSON + `]`))
		case "/branch_protections/dev":
			_, _ = w.Write([]byte(openRuleJSON))
		case "/branch_protections/main":
			_, _ = w.Write([]byte(mainRuleJSON))
		case "/branch_protections/release%2F%2A", "/branch_protections/release%2F*", "/branch_protections/release/*":
			_, _ = w.Write([]byte(releaseRuleJSON))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}
	}))
	defer server.Close()
	run := func(args ...string) (string, error) {
		deps, out, _ := testDeps(t)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
		err := Execute(context.Background(), deps, append([]string{"branch", "protection"}, args...))
		return out.String(), err
	}

	out, err := run()
	require.NoError(t, err)
	require.Contains(t, out, "list")
	require.Contains(t, out, "view")
	require.Empty(t, requests, "a bare group only shows its help")

	out, err = run("list")
	require.NoError(t, err)
	require.Equal(t, "main\t2\talice, team release\tci/build, ci/test\tblocks on rejected reviews, dismisses stale approvals\n"+
		"release/*\t0\tnobody\t\tsigned commits\n"+
		"dev\t1\teveryone with write access\tall reported checks\tforce push allowed, ignores stale approvals\n", out, "a rule that requires no checks lists none, whatever contexts it holds; nor force push when nobody may push")

	out, err = run("list", "--json", "rule,push_enabled,push_users,status_check_required,status_checks")
	require.NoError(t, err)
	require.JSONEq(t, `[{"rule":"main","push_enabled":true,"push_users":["alice"],"status_check_required":true,"status_checks":["ci/build","ci/test"]},
		{"rule":"release/*","push_enabled":false,"push_users":[],"status_check_required":false,"status_checks":[]},
		{"rule":"dev","push_enabled":true,"push_users":[],"status_check_required":true,"status_checks":[]}]`, out)

	out, err = run("view", "main")
	require.NoError(t, err)
	for _, want := range []string{"title:\tmain", "required approvals:\t2", "may push:\talice, team release", "may merge:\tbob", "status checks:\tci/build, ci/test"} {
		require.Contains(t, out, want)
	}
	out, err = run("view", "dev")
	require.NoError(t, err)
	for _, want := range []string{"status checks:\tall reported checks", "may force push:\tcarol, deploy keys", "also:\tforce push allowed, ignores stale approvals"} {
		require.Contains(t, out, want)
	}
	out, err = run("view", "dev", "--json", "force_push_users,force_push_deploy_keys")
	require.NoError(t, err)
	require.JSONEq(t, `{"force_push_users":["carol"],"force_push_deploy_keys":true}`, out)
	out, err = run("view", "release/*")
	require.NoError(t, err)
	require.NotContains(t, out, "force push", "Gitea checks that one may push before force pushing")
	out, err = run("view", "release/*", "--json", "rule,force_push,require_signed_commits")
	require.NoError(t, err)
	require.JSONEq(t, `{"rule":"release/*","force_push":true,"require_signed_commits":true}`, out)
	_, err = run("view", "nothing")
	require.ErrorContains(t, err, `protection rule "nothing"`)
}
