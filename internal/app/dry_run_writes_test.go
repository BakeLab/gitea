// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"

	"github.com/stretchr/testify/require"
)

// dryRunWriteCases are the commands of dryRunCommands, each with arguments that make it write, and
// the method of its first write. {file} stands for a file with some text, {pubkey} for one with an
// SSH public key, {labels} for one with a label, and {gpgkey} and {gpgsignature} for armored GPG blocks.
var dryRunWriteCases = []struct {
	args []string
	want string
}{
	{[]string{"admin", "cron", "run", "update_mirrors"}, "POST"},
	{[]string{"admin", "org", "create", "acme", "--owner", "alice"}, "POST"},
	{[]string{"admin", "user", "badge", "add", "bob", "champion"}, "POST"},
	{[]string{"admin", "user", "badge", "delete", "bob", "champion"}, "DELETE"},
	{[]string{"admin", "user", "create", "bob", "--email", "bob@example.com", "--password", "hunter2"}, "POST"},
	{[]string{"admin", "user", "delete", "alice"}, "DELETE"},
	{[]string{"admin", "user", "edit", "alice", "--email", "new@example.com"}, "PATCH"},
	{[]string{"admin", "user", "key", "create", "bob", "{pubkey}"}, "POST"},
	{[]string{"admin", "user", "key", "delete", "bob", "3"}, "DELETE"},
	{[]string{"admin", "user", "rename", "bob", "robert"}, "POST"},
	{[]string{"api", "repos/alice/project/issues", "-X", "POST", "-f", "title=x"}, "POST"},
	{[]string{"branch", "create", "feature"}, "POST"},
	{[]string{"branch", "protect", "main"}, ""},
	{[]string{"branch", "rename", "old", "newer"}, "PATCH"},
	{[]string{"branch", "unprotect", "main"}, "DELETE"},
	{[]string{"issue", "attachment", "delete", "5", "1"}, "DELETE"},
	{[]string{"issue", "attachment", "edit", "5", "1", "--name", "new.txt"}, "PATCH"},
	{[]string{"issue", "attachment", "upload", "5", "{file}"}, "POST"},
	{[]string{"issue", "comment", "attachment", "delete", "1", "1"}, "DELETE"},
	{[]string{"issue", "comment", "attachment", "edit", "1", "1", "--name", "new.txt"}, "PATCH"},
	{[]string{"issue", "comment", "attachment", "upload", "1", "{file}"}, "POST"},
	{[]string{"issue", "comment", "create", "5", "--body", "hi"}, "POST"},
	{[]string{"issue", "comment", "delete", "1"}, "DELETE"},
	{[]string{"issue", "comment", "edit", "1", "--body", "edited"}, "PATCH"},
	{[]string{"issue", "create", "--title", "Bug", "--body", "text"}, "POST"},
	{[]string{"issue", "delete", "5"}, "DELETE"},
	{[]string{"issue", "edit", "5", "--title", "New"}, "PATCH"},
	{[]string{"issue", "lock", "5", "--reason", "Resolved"}, "PUT"},
	{[]string{"issue", "move", "9", "Done"}, ""},
	{[]string{"issue", "pin", "5"}, "POST"},
	{[]string{"issue", "unlock", "5"}, "DELETE"},
	{[]string{"issue", "unpin", "5"}, "DELETE"},
	{[]string{"label", "create", "newlabel", "--color", "ff0000"}, "POST"},
	{[]string{"label", "edit", "bug", "--color", "00ff00"}, "PATCH"},
	{[]string{"label", "import", "{labels}"}, "POST"},
	{[]string{"milestone", "close", "Bug"}, "PATCH"},
	{[]string{"milestone", "create", "v2"}, "POST"},
	{[]string{"milestone", "delete", "Bug"}, "DELETE"},
	{[]string{"milestone", "edit", "Bug", "--title", "v1.1"}, "PATCH"},
	{[]string{"milestone", "issue", "add", "Bug", "5"}, "PATCH"},
	{[]string{"milestone", "issue", "remove", "Bug", "8"}, "PATCH"},
	{[]string{"milestone", "reopen", "Done"}, "PATCH"},
	{[]string{"notification", "mark-read", "1"}, "PATCH"},
	{[]string{"notification", "mark-unread", "2"}, "PATCH"},
	{[]string{"notification", "pin", "1"}, "PATCH"},
	{[]string{"notification", "unpin", "3"}, "PATCH"},
	{[]string{"org", "create", "acme"}, "POST"},
	{[]string{"org", "delete", "acme"}, "DELETE"},
	{[]string{"org", "edit", "acme", "--description", "d"}, "PATCH"},
	{[]string{"org", "member", "conceal", "acme", "alice"}, "DELETE"},
	{[]string{"org", "member", "publicize", "acme", "bob"}, "PUT"},
	{[]string{"org", "member", "remove", "acme", "alice"}, "DELETE"},
	{[]string{"package", "delete", "generic", "pkg", "1.0"}, "DELETE"},
	{[]string{"package", "link", "generic", "pkg", "alice/project"}, "POST"},
	{[]string{"package", "unlink", "generic", "pkg"}, "POST"},
	{[]string{"package", "upload", "pkg", "1.0", "{file}"}, "PUT"},
	{[]string{"pr", "attachment", "delete", "7", "1"}, "DELETE"},
	{[]string{"pr", "attachment", "edit", "7", "1", "--name", "new.txt"}, "PATCH"},
	{[]string{"pr", "attachment", "upload", "7", "{file}"}, "POST"},
	{[]string{"pr", "comment", "attachment", "delete", "1", "1"}, "DELETE"},
	{[]string{"pr", "comment", "attachment", "edit", "1", "1", "--name", "new.txt"}, "PATCH"},
	{[]string{"pr", "comment", "attachment", "upload", "1", "{file}"}, "POST"},
	{[]string{"pr", "comment", "create", "7", "--body", "hi"}, "POST"},
	{[]string{"pr", "comment", "delete", "1"}, "DELETE"},
	{[]string{"pr", "comment", "edit", "1", "--body", "edited"}, "PATCH"},
	{[]string{"pr", "edit", "7", "--title", "New"}, "PATCH"},
	{[]string{"pr", "ready", "10"}, ""},
	{[]string{"pr", "lock", "7", "--reason", "Resolved"}, "PUT"},
	{[]string{"pr", "unlock", "7"}, "DELETE"},
	{[]string{"pr", "pin", "7"}, "POST"},
	{[]string{"pr", "unpin", "7"}, "DELETE"},
	{[]string{"pr", "delete", "7"}, "DELETE"},
	{[]string{"pr", "review", "cancel-request", "7", "--reviewer", "bob"}, "DELETE"},
	{[]string{"pr", "review", "create", "7", "--approve"}, "POST"},
	{[]string{"pr", "review", "delete", "7", "1"}, "DELETE"},
	{[]string{"pr", "review", "dismiss", "7", "1", "--body", "stale"}, "POST"},
	{[]string{"pr", "review", "reply", "7", "1", "--body", "ok"}, "POST"},
	{[]string{"pr", "review", "request", "7", "--reviewer", "bob"}, "POST"},
	{[]string{"pr", "review", "resolve", "7", "1"}, "POST"},
	{[]string{"pr", "review", "submit", "7", "3", "--approve"}, "POST"},
	{[]string{"pr", "review", "undismiss", "7", "2"}, "POST"},
	{[]string{"pr", "review", "unresolve", "7", "1"}, "POST"},
	{[]string{"pr", "update-branch", "7"}, "POST"},
	{[]string{"project", "close", "Bug"}, ""},
	{[]string{"project", "create", "NewProject"}, "POST"},
	{[]string{"project", "delete", "Bug"}, "DELETE"},
	{[]string{"project", "edit", "Bug", "--title", "Plan"}, ""},
	{[]string{"project", "reopen", "Done"}, ""},
	{[]string{"project", "workflow", "create", "Bug", "--event", "item_added_to_project", "--issue-state", "close"}, "POST"},
	{[]string{"project", "workflow", "delete", "Bug", "1"}, "DELETE"},
	{[]string{"project", "workflow", "disable", "Bug", "1"}, ""},
	{[]string{"project", "workflow", "edit", "Bug", "1", "--issue-state", "close"}, ""},
	{[]string{"project", "workflow", "enable", "Bug", "2"}, ""},
	{[]string{"release", "asset", "delete", "v1", "1"}, "DELETE"},
	{[]string{"release", "asset", "edit", "v1", "1", "--name", "new.txt"}, "PATCH"},
	{[]string{"release", "asset", "upload", "v1", "{file}"}, "POST"},
	{[]string{"release", "create", "v2", "--title", "Two", "--notes", "n"}, "POST"},
	{[]string{"release", "edit", "v1", "--title", "One"}, "PATCH"},
	{[]string{"release", "publish", "vd"}, "PATCH"},
	{[]string{"release", "upload", "v1", "{file}"}, "POST"},
	{[]string{"repo", "archive", "alice/project"}, "PATCH"},
	{[]string{"repo", "create", "newrepo"}, "POST"},
	{[]string{"repo", "deploy-key", "add", "{pubkey}", "--title", "ci"}, "POST"},
	{[]string{"repo", "deploy-key", "delete", "3"}, "DELETE"},
	{[]string{"repo", "edit", "alice/project", "--description", "d"}, "PATCH"},
	{[]string{"repo", "migrate", "https://example.com/x/y.git"}, "POST"},
	{[]string{"repo", "rename", "renamed"}, "PATCH"},
	{[]string{"repo", "transfer", "bob"}, "POST"},
	{[]string{"repo", "unarchive", "alice/archived"}, "PATCH"},
	{[]string{"run", "cancel", "6"}, "POST"},
	{[]string{"run", "delete", "5"}, "DELETE"},
	{[]string{"run", "rerun", "5"}, "POST"},
	{[]string{"runner", "delete", "3"}, "DELETE"},
	{[]string{"runner", "disable", "3"}, "PATCH"},
	{[]string{"runner", "enable", "4"}, "PATCH"},
	{[]string{"runner", "token"}, "POST"},
	{[]string{"secret", "delete", "DEPLOY"}, "DELETE"},
	{[]string{"secret", "set", "DEPLOY", "--body", "s3cret"}, "PUT"},
	{[]string{"ssh-key", "create", "{pubkey}", "--title", "laptop"}, "POST"},
	{[]string{"ssh-key", "delete", "3"}, "DELETE"},
	{[]string{"tag", "create", "v2.0.0", "--target", "main", "--message", "Two"}, "POST"},
	{[]string{"tag", "delete", "v1"}, "DELETE"},
	{[]string{"tag", "protect", "rel-*", "--allow-user", "bob"}, "POST"},
	{[]string{"tag", "unprotect", "v*"}, "DELETE"},
	{[]string{"team", "create", "acme", "devs"}, "POST"},
	{[]string{"team", "delete", "1"}, "DELETE"},
	{[]string{"team", "edit", "1", "--description", "d"}, "PATCH"},
	{[]string{"team", "member", "add", "1", "bob"}, "PUT"},
	{[]string{"team", "member", "remove", "1", "bob"}, "DELETE"},
	{[]string{"team", "repo", "add", "1", "acme/project"}, "PUT"},
	{[]string{"team", "repo", "remove", "1", "acme/project"}, "DELETE"},
	{[]string{"time", "cancel", "5"}, "DELETE"},
	{[]string{"time", "create", "5", "1h"}, "POST"},
	{[]string{"time", "delete", "5", "1"}, "DELETE"},
	{[]string{"time", "reset", "5"}, "DELETE"},
	{[]string{"time", "start", "5"}, "POST"},
	{[]string{"time", "stop", "5"}, "POST"},
	{[]string{"variable", "delete", "V"}, "DELETE"},
	{[]string{"variable", "set", "V", "--body", "value"}, "PUT"},
	{[]string{"webhook", "create", "https://example.com/hook", "--events", "push"}, "POST"},
	{[]string{"webhook", "delete", "3"}, "DELETE"},
	{[]string{"webhook", "edit", "3", "--active=false", "--authorization-header", "token abc"}, "PATCH"},
	{[]string{"webhook", "test", "3"}, "POST"},
	{[]string{"gpg-key", "create", "{gpgkey}"}, "POST"},
	{[]string{"gpg-key", "delete", "4"}, "DELETE"},
	{[]string{"gpg-key", "verify", "3AA5C34371567BD2", "{gpgsignature}"}, "POST"},
	{[]string{"issue", "comment", "5", "--body", "hi"}, "POST"},
	{[]string{"pr", "comment", "7", "--body", "hi"}, "POST"},
	{[]string{"pr", "review", "7", "--approve"}, "POST"},
	{[]string{"wiki", "create", "NewPage", "--body", "hi"}, "POST"},
	{[]string{"wiki", "delete", "Home"}, "DELETE"},
	{[]string{"wiki", "edit", "Home", "--body", "hi"}, "PATCH"},
	{[]string{"workflow", "disable", "ci.yml"}, "PUT"},
	{[]string{"workflow", "enable", "off.yml"}, "PUT"},
	{[]string{"workflow", "run", "ci.yml"}, "POST"},
}

// expandDryRunArgs replaces the placeholders of dryRunWriteCases with files.
func expandDryRunArgs(t *testing.T, args []string) []string {
	t.Helper()
	files := map[string]string{
		"{file}":         "some text\n",
		"{labels}":       "#ff0000 newlabel\n",
		"{gpgkey}":       testGPGPublicKey,
		"{gpgsignature}": testGPGSignature,
	}
	key, _ := testPublicKey(t, "alice@laptop")
	files["{pubkey}"] = key + "\n"
	expanded := make([]string, len(args))
	for i, arg := range args {
		content, ok := files[arg]
		if !ok {
			expanded[i] = arg
			continue
		}
		path := filepath.Join(t.TempDir(), "input.txt")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		expanded[i] = path
	}
	return expanded
}

// dryRunLists are the last segments of the Gitea API paths that answer a list.
var dryRunLists = []string{"tag_protections", "columns", "files", "milestones", "projects", "pulls", "repos", "reviews", "times", "users"}

// dryRunGeneric is a resource with the fields that commands read from the many kinds of resource, so
// that one answer serves a command whatever it asks for.
const dryRunGeneric = `{"id":1,"number":5,"index":5,"name":"bug","title":"Bug","body":"text","state":"open","login":"alice","username":"alice",
"full_name":"alice/project","description":"d","color":"ff0000","tag_name":"v1","key":"MIT","url":"http://x","html_url":"http://x",
"default_branch":"main","has_issues":true,"has_wiki":true,"has_projects":true,"has_pull_requests":true,"has_actions":true,
"email":"alice@example.com","slug":"champion","type":"generic","version":"1.0","status":"online","event":"item_added_to_project",
"organization":{"username":"acme","name":"acme"},"owner":{"login":"alice","id":1,"username":"alice"},"user":{"login":"alice","id":1},"repository":{"full_name":"alice/project"},
"permissions":{"admin":true,"push":true,"pull":true},"commit":{"id":"abc1234"},"sha":"abc1234","active":true,"events":["push"],
"config":{"url":"http://x","content_type":"json"},"labels":[],"assignees":[],"is_draft":false,"draft":false,"head":{"ref":"fix"},
"user_id":1,"issue_url":"http://x","pull_request_url":"http://x","browser_download_url":"http://x","size":1,"download_count":0,
"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","due_on":null,"closed_at":null,"submitted_at":"2026-01-01T00:00:00Z"}`

// dryRunGenericResponse answers a read of a path that dryRunFixture has no resource for: a list of
// one resource, only on its first page, for a path that ends in a collection, else the resource.
func dryRunGenericResponse(path, page string) string {
	last := path[strings.LastIndex(path, "/")+1:]
	if slices.Contains(dryRunLists, last) {
		if page != "" && page != "1" {
			return "[]"
		}
		return "[" + dryRunGeneric + "]"
	}
	return dryRunGeneric
}

// dryRunFixture answers the reads of dryRunWriteCases that need a particular answer: a membership
// check, a pull request that is a draft, a review in a state, a name that is free. ok is false for
// every other read.
func dryRunFixture(path, page string) (status int, body string, ok bool) {
	last := path[strings.LastIndex(path, "/")+1:]
	switch {
	case strings.HasSuffix(path, "/public_members/bob"):
		return http.StatusNotFound, `{"message":"not found"}`, true
	case strings.Contains(path, "/orgs/") && (strings.Contains(path, "/members/") || strings.Contains(path, "/public_members/")):
		return http.StatusNoContent, "", true
	case strings.HasSuffix(path, "/repos/acme/project"):
		return 200, `{"id":2,"full_name":"acme/project","name":"project","owner":{"login":"acme","type":"Organization"},"default_branch":"main"}`, true
	case strings.HasSuffix(path, "/repos/alice/archived"):
		return 200, `{"id":3,"full_name":"alice/archived","name":"archived","owner":{"login":"alice"},"default_branch":"main","archived":true}`, true
	case strings.HasSuffix(path, "/contents/.gitea/workflows"):
		return 200, `[{"name":"ci.yml","path":".gitea/workflows/ci.yml","type":"file","size":80}]`, true
	case strings.HasSuffix(path, "/contents/.gitea/workflows/ci.yml"):
		return 200, `{"name":"ci.yml","path":".gitea/workflows/ci.yml","type":"file","encoding":"base64","size":80,"content":"b246CiAgd29ya2Zsb3dfZGlzcGF0Y2g6CmpvYnM6CiAgYnVpbGQ6CiAgICBydW5zLW9uOiB1YnVudHUtbGF0ZXN0CiAgICBzdGVwczogW10K"}`, true
	case strings.HasSuffix(path, "/projects/1/columns") && (page == "" || page == "1"):
		return 200, `[{"id":1,"title":"Todo"},{"id":2,"title":"Done"}]`, true
	case strings.Contains(path, "/projects/1/columns/") && strings.HasSuffix(path, "/issues"):
		if strings.Contains(path, "/columns/1/") && (page == "" || page == "1") {
			return 200, `[{"id":1,"number":9,"title":"P","state":"open"}]`, true
		}
		return 200, `[]`, true
	case strings.HasSuffix(path, "/projects/1/workflows/1"):
		return 200, `{"id":1,"event":"item_added_to_project","enabled":true}`, true
	case strings.HasSuffix(path, "/projects/1/workflows/2"):
		return 200, `{"id":2,"event":"item_closed","enabled":false}`, true
	case strings.HasSuffix(path, "/tag_protections"):
		return 200, `[{"id":4,"name_pattern":"v*","whitelist_usernames":["alice"],"whitelist_teams":[]}]`, true
	case strings.HasSuffix(path, "/user/stopwatches"):
		if page != "" && page != "1" {
			return 200, `[]`, true
		}
		return 200, `[{"seconds":3661,"duration":"1h1m1s","issue_index":5,"issue_title":"Bug","repo_owner_name":"alice","repo_name":"project","created":"2026-01-01T00:00:00Z"}]`, true
	case strings.HasSuffix(path, "/actions/workflows/ci.yml"):
		return 200, `{"id":"ci.yml","name":"ci","path":".gitea/workflows/ci.yml","state":"active"}`, true
	case strings.HasSuffix(path, "/actions/workflows/off.yml"):
		return 200, `{"id":"off.yml","name":"off","path":".gitea/workflows/off.yml","state":"disabled_manually"}`, true
	case strings.HasSuffix(path, "/actions/runs/5/jobs"):
		return 200, `{"total_count":1,"jobs":[{"id":11,"name":"build","status":"completed","conclusion":"failure","run_id":5}]}`, true
	case strings.HasSuffix(path, "/projects/1/workflows"):
		return 200, `[{"id":1,"event":"item_added_to_project","enabled":true},{"id":2,"event":"item_closed","enabled":false}]`, true
	case strings.HasSuffix(path, "/projects") && (page == "" || page == "1"):
		return 200, `[{"id":1,"title":"Bug","state":"open"},{"id":2,"title":"Done","state":"closed","is_closed":true}]`, true
	case strings.HasSuffix(path, "/actions/runs"):
		return 200, `{"total_count":2,"workflow_runs":[{"id":5,"run_number":5,"status":"completed","conclusion":"success","head_branch":"main","head_sha":"abc1234","event":"push","name":"ci","display_title":"ci"},{"id":6,"run_number":6,"status":"in_progress","head_branch":"main","head_sha":"abc1234","event":"push","name":"ci","display_title":"ci"}]}`, true
	case strings.Contains(path, "/actions/runs/5") || strings.HasSuffix(path, "/actions/runs/5"):
		return 200, `{"id":5,"run_number":5,"status":"completed","conclusion":"success","head_branch":"main","head_sha":"abc1234","event":"push","name":"ci","display_title":"ci"}`, true
	case strings.Contains(path, "/actions/runs/6"):
		return 200, `{"id":6,"run_number":6,"status":"in_progress","head_branch":"main","head_sha":"abc1234","event":"push","name":"ci","display_title":"ci"}`, true
	case strings.HasSuffix(path, "/actions/runners/4"):
		return 200, `{"id":4,"name":"builder-2","status":"offline","disabled":true,"labels":[]}`, true
	case strings.HasSuffix(path, "/actions/runners/3"):
		return 200, `{"id":3,"name":"builder-1","status":"online","disabled":false,"labels":[]}`, true
	case strings.HasSuffix(path, "/actions/workflows"):
		return 200, `{"workflows":[{"id":"ci.yml","name":"ci","path":".gitea/workflows/ci.yml","state":"active"},{"id":"off.yml","name":"off","path":".gitea/workflows/off.yml","state":"disabled_manually"}]}`, true
	case strings.HasSuffix(path, "/milestones") && (page == "" || page == "1"):
		return 200, `[{"id":1,"title":"Bug","state":"open"},{"id":2,"title":"Done","state":"closed"}]`, true
	case strings.HasSuffix(path, "/issues/8"):
		return 200, `{"number":8,"title":"M","state":"open","milestone":{"id":1,"title":"Bug"},"repository":{"full_name":"alice/project"}}`, true
	case strings.HasSuffix(path, "/issues/9"):
		return 200, `{"id":1,"number":9,"title":"P","state":"open","projects":[{"id":1,"is_closed":false}],"repository":{"full_name":"alice/project"}}`, true
	case strings.HasSuffix(path, "/issues/7") || strings.HasSuffix(path, "/issues/10"):
		return 200, `{"number":` + last + `,"title":"Fix","state":"open","pull_request":{"merged":false},"repository":{"full_name":"alice/project"}}`, true
	case strings.HasSuffix(path, "/notifications/threads/2"):
		return 200, `{"id":2,"unread":false,"pinned":false,"subject":{"title":"t","type":"Issue","url":"http://x"},"repository":{"full_name":"alice/project"}}`, true
	case strings.HasSuffix(path, "/notifications/threads/3"):
		return 200, `{"id":3,"unread":false,"pinned":true,"subject":{"title":"t","type":"Issue","url":"http://x"},"repository":{"full_name":"alice/project"}}`, true
	case strings.Contains(path, "/notifications/threads/"):
		return 200, `{"id":1,"unread":true,"pinned":false,"subject":{"title":"t","type":"Issue","url":"http://x"},"repository":{"full_name":"alice/project"}}`, true
	case strings.HasSuffix(path, "/pulls/10"):
		return 200, `{"number":10,"title":"WIP: Draft","state":"open","draft":true,"mergeable":true,"head":{"ref":"fix","label":"fix","sha":"abc1234","repo":{"full_name":"alice/project"}},"base":{"ref":"main","repo":{"full_name":"alice/project"}}}`, true
	case strings.HasSuffix(path, "/pulls/7/reviews/1"):
		return 200, `{"id":1,"state":"APPROVED","user":{"login":"bob"},"body":"ok","submitted_at":"2026-01-01T00:00:00Z"}`, true
	case strings.HasSuffix(path, "/pulls/7/reviews/2"):
		return 200, `{"id":2,"state":"DISMISSED","dismissed":true,"user":{"login":"bob"},"body":"ok","submitted_at":"2026-01-01T00:00:00Z"}`, true
	case strings.HasSuffix(path, "/pulls/7/reviews/3"):
		return 200, `{"id":3,"state":"PENDING","user":{"login":"alice"},"body":"wip","submitted_at":"2026-01-01T00:00:00Z"}`, true
	case strings.HasSuffix(path, "/releases/tags/vd"):
		return 200, `{"id":4,"tag_name":"vd","name":"Draft","draft":true,"assets":[]}`, true
	case slices.Contains([]string{"newrepo", "renamed", "feature", "v2", "NewPage", "newer", "NewProject", "newlabel"}, last) && !strings.Contains(path, "/search"):
		return http.StatusNotFound, `{"message":"not found"}`, true
	}
	return 0, "", false
}

// TestDryRunLeavesWhatItDoesNotWithholdAlone covers what the cases of dryRunWriteCases do not: a
// rename that would update a Git remote, output that would be false, options that reach outside
// Gitea, and values that must not appear in the report.
func TestDryRunLeavesWhatItDoesNotWithholdAlone(t *testing.T) {
	server := dryRunServer(t)

	t.Run("a rename does not update the remote", func(t *testing.T) {
		deps, out, _ := testDeps(t)
		deps.Getenv = serverEnv(server.URL, nil)
		fake := &fakeGit{results: map[string]string{
			"symbolic-ref --quiet HEAD":       "main",
			"config --get branch.main.remote": "origin",
			"remote get-url origin":           server.URL + "/alice/project.git",
			"config --get remote.origin.url":  server.URL + "/alice/project.git",
		}}
		deps.Git = fake
		require.NoError(t, Execute(context.Background(), deps, []string{"repo", "rename", "renamed", "--dry-run"}))
		require.Contains(t, out.String(), "Dry run: would send PATCH "+server.URL+"/api/v1/repos/alice/project ")
		for _, call := range fake.calls {
			require.NotContains(t, call, "set-url")
			require.NotContains(t, call, "--local gt.repository")
		}
	})

	t.Run("what a command prints about the write it did not make is held back", func(t *testing.T) {
		out, _, err := dryRun(t, server, "issue", "create", "--title", "Bug", "--body", "text")
		require.NoError(t, err)
		require.Equal(t, 1, strings.Count(out, "\n"), out)
		require.True(t, strings.HasPrefix(out, "Dry run: would send POST "), out)
	})

	t.Run("a command that writes nothing prints what it read", func(t *testing.T) {
		out, _, err := dryRun(t, server, "api", "repos/alice/project")
		require.NoError(t, err)
		require.Contains(t, withoutANSI(out), `"full_name": "alice/project"`)
		require.NotContains(t, out, "Dry run")
	})

	t.Run("options that reach outside Gitea", func(t *testing.T) {
		for _, args := range [][]string{
			{"issue", "create", "--web"},
			{"repo", "create", "x", "--source", "."},
			{"repo", "create", "x", "--clone"},
		} {
			_, _, err := dryRun(t, server, args...)
			require.Error(t, err, "%v", args)
			require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v: %v", args, err)
		}
	})

	t.Run("the report keeps secrets out", func(t *testing.T) {
		for _, args := range [][]string{
			{"secret", "set", "DEPLOY", "--body", "s3cret-value"},
			{"admin", "user", "create", "bob", "--email", "bob@example.com", "--password", "hunter2-value"},
			{"webhook", "create", "https://example.com/hook", "--events", "push", "--secret", "signing-value"},
		} {
			out, _, err := dryRun(t, server, args...)
			require.NoError(t, err, "%v", args)
			require.Contains(t, out, "[REDACTED]", "%v", args)
			for _, secret := range []string{"s3cret-value", "hunter2-value", "signing-value"} {
				require.NotContains(t, out, secret, "%v", args)
			}
		}
	})
}

// TestDryRunPreviewsDependencyChanges covers the dependency flags of issue edit and pr edit, which
// the one case of each command in dryRunWriteCases does not send.
func TestDryRunPreviewsDependencyChanges(t *testing.T) {
	server := dryRunServer(t)
	repo := server.URL + "/api/v1/repos/alice/project"
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"issue", "edit", "5", "--add-blocks", "9"}, "POST " + repo + "/issues/5/blocks"},
		{[]string{"issue", "edit", "5", "--remove-blocked-by", "9"}, "DELETE " + repo + "/issues/5/dependencies"},
		{[]string{"pr", "edit", "7", "--add-blocked-by", "9"}, "POST " + repo + "/issues/7/dependencies"},
		{[]string{"pr", "edit", "7", "--remove-blocks", "9"}, "DELETE " + repo + "/issues/7/blocks"},
		{[]string{"tag", "protect", "v*", "--allow-user", "bob"}, "PATCH " + repo + "/tag_protections/4"},
	} {
		out, _, err := dryRun(t, server, tc.args...)
		require.NoError(t, err, "%v", tc.args)
		require.Contains(t, out, "Dry run: would send "+tc.want, "%v", tc.args)
	}
}

// TestDryRunPreviewsTheMergeOptions covers the flags of pr merge that the one case of the command in
// dryRunWriteCases does not send.
func TestDryRunPreviewsTheMergeOptions(t *testing.T) {
	server := dryRunServer(t)
	repo := server.URL + "/api/v1/repos/alice/project"
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"pr", "merge", "7", "--disable-auto"}, "DELETE " + repo + "/pulls/7/merge"},
		{[]string{"pr", "merge", "7", "--squash", "--admin"}, "POST " + repo + "/pulls/7/merge"},
	} {
		out, _, err := dryRun(t, server, tc.args...)
		require.NoError(t, err, "%v", tc.args)
		require.Contains(t, out, "Dry run: would send "+tc.want, "%v", tc.args)
	}
}
