// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

func TestUsageErrorsPrecedeRequests(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"admin", "user", "create", "account", "--email", "invalid", "--password", "local-password"}, "--email: enter one email address"},
		{[]string{"admin", "user", "create", "account", "--email", "account@example.test", "--password", "one", "--password-file", "-"}, "--password and --password-file are mutually exclusive"},
		{[]string{"admin", "user", "create", "account", "--email", "account@example.test", "--password-file", ""}, "--password-file must name a file or -"},
		{[]string{"admin", "user", "create", "account", "--email", "account@example.test", "--password", "local-password", "--visibility", "invalid"}, "--visibility must be public, limited, or private"},
		{[]string{"admin", "user", "create", "account", "--username", "different"}, "supply username as an argument or --username, not both"},
		{[]string{"admin", "user", "edit", "account"}, "supply account fields to edit"},
		{[]string{"admin", "user", "edit", "account", "--email="}, "--email: enter one email address"},
		{[]string{"admin", "user", "edit", "account", "--max-repo-creation", "-2"}, "--max-repo-creation must be -1 or nonnegative"},
		{[]string{"admin", "user", "edit", "account", "--password="}, "missing password: supply --password-file or --password"},
		{[]string{"admin", "user", "view", "bad/account"}, "invalid account username"},
		{[]string{"api", "user", "--input", "-", "-f", "x=y"}, "--input conflicts with API fields"},
		{[]string{"api", "user", "-f", "x=y", "-F", "x=2"}, "API field names must be unique"},
		{[]string{"api", "user", "-f", "missing-equals"}, "API fields require name=value"},
		{[]string{"api", "user", "--paginate", "-X", "POST"}, "--paginate requires a body-free GET request"},
		{[]string{"api", "user", "--input="}, "--input requires a file path or -"},
		{[]string{"api", "user", "-X", "invalid/method"}, "invalid HTTP method"},
		{[]string{"alias", "delete", "co", "--json"}, "alias deletion does not return a resource"},
		{[]string{"auth", "status", "--json=token"}, "unknown JSON field \"token\""},
		{[]string{"config", "set", "prompt", "invalid"}, "prompt must be enabled or disabled"},
		{[]string{"doctor", "--json=secret"}, "unknown JSON field \"secret\""},
		{[]string{"issue", "attachment", "download", "1", "1", "--json"}, "attachment downloads are binary"},
		{[]string{"issue", "attachment", "download", "1", "1", "--output-file", "-", "--json=id"}, "attachment downloads are binary"},
		{[]string{"issue", "close", "1", "bad"}, `invalid issue "bad": use a number, #number, OWNER/REPO#number or URL`},
		{[]string{"issue", "close", "1", "#1", "--no-input"}, "#1 is selected more than once"},
		{[]string{"issue", "reopen", "1", "01", "--no-input"}, "#1 is selected more than once"},
		{[]string{"issue", "comment", "create", "1", "--body="}, "comment body cannot be empty"},
		{[]string{"issue", "comment", "create", "1", "--body", "text", "--json=title"}, "unknown JSON field \"title\""},
		{[]string{"issue", "create", "--no-input"}, "missing required input: supply --title"},
		{[]string{"issue", "create", "--title", "safe", "--json=unknown"}, "unknown JSON field \"unknown\""},
		{[]string{"issue", "create", "--title", "safe", "--json", "--jq", "["}, "invalid --jq expression"},
		{[]string{"issue", "create", "--milestone="}, "--milestone needs a title or ID; use --remove-milestone to clear it"},
		{[]string{"issue", "create", "--assignee", "alice, "}, "assignee names cannot be empty"},
		{[]string{"issue", "create", "--template="}, "--template cannot be empty"},
		{[]string{"issue", "create", "--title", "x", "--attachment", "."}, ". is not a regular file"},
		{[]string{"issue", "create", "--milestone-name="}, "--milestone needs a title or ID"},
		{[]string{"issue", "edit", "1", "--milestone", "v1", "--remove-milestone"}, "--milestone and --remove-milestone cannot be combined"},
		{[]string{"issue", "edit", "1"}, "nothing to edit; pass a flag such as --title (see --help)"},
		{[]string{"issue", "edit", "1", "--editor=false"}, "nothing to edit"},
		{[]string{"issue", "edit", "1", "--add-labels="}, "nothing to edit"},
		{[]string{"issue", "edit", "1", "--title="}, "issue title cannot be empty"},
		{[]string{"issue", "edit", "1", "--set-assignees", "alice", "--add-assignees", "bob"}, "--set-assignees cannot be combined with --add-assignee or --remove-assignee"},
		{[]string{"issue", "edit", "1", "--set-labels", "bug", "--add-label", "help"}, "--set-labels cannot be combined with --add-label or --remove-label"},
		{[]string{"issue", "edit", "1", "--add-assignees", " "}, "assignee names cannot be empty"},
		{[]string{"issue", "edit", "1", "invalid", "--title", "Updated"}, `invalid issue "invalid"`},
		{[]string{"issue", "edit", "1", "2", "--editor"}, "--editor requires one issue"},
		{[]string{"issue", "edit", "1", "01", "--title", "Updated", "--no-input"}, "#1 is selected more than once"},
		{[]string{"issue", "list", "--limit", "0"}, `"-L, --limit" flag: must be 1..1000`},
		{[]string{"issue", "list", "--page", "0"}, `"--page" flag: must be a positive integer`},
		{[]string{"issue", "list", "--label", "bug, "}, "label names cannot be empty"},
		{[]string{"label", "create", "--name", "bug", "--color", "wrong"}, "--color requires six hexadecimal digits"},
		{[]string{"label", "edit", "1"}, "nothing to edit; pass a flag such as --title (see --help)"},
		{[]string{"label", "list", "--org", "team", "-R", "a/b"}, "--org and --repo are mutually exclusive"},
		{[]string{"milestone", "close", " ", "--by-name"}, "milestone title or ID cannot be empty"},
		{[]string{"milestone", "create", "--title", "v1", "--deadline", "invalid"}, "invalid --deadline"},
		{[]string{"milestone", "create", "--title", "x", "--state", "merged", "--no-input"}, "--state must be open or closed"},
		{[]string{"milestone", "edit", "1"}, "nothing to edit; pass a flag such as --title (see --help)"},
		{[]string{"milestone", "edit", "9", "--title", ""}, "milestone title cannot be empty"},
		{[]string{"milestone", "edit", "9", "--title", " \t "}, "milestone title cannot be empty"},
		{[]string{"milestone", "issue", "add", "7", "1", "01", "--no-input"}, "#1 is selected more than once"},
		{[]string{"milestone", "issue", "remove", "7", "1", "1", "--no-input"}, "#1 is selected more than once"},
		{[]string{"milestone", "list", "--limit", "1001"}, `"-L, --limit" flag: must be 1..1000`},
		{[]string{"milestone", "list", "--state", "merged"}, "--state must be open, closed, or all"},
		{[]string{"notification", "list", "--status", "invalid"}, "--status must be unread, read, or pinned"},
		{[]string{"notification", "list", "--type", "invalid"}, "--type must be issue, pull, commit, or repository"},
		{[]string{"notification", "list", "--limit", "many"}, `"-L, --limit" flag: must be 1..1000`},
		{[]string{"notification", "list", "--since="}, "invalid --since: use a date, RFC3339 timestamp, or duration such as 24h"},
		{[]string{"notification", "list", "--since=1h", "--before=2h"}, "--since must not be later than --before"},
		{[]string{"notification", "mark-read"}, "supply notification IDs or --all"},
		{[]string{"notification", "mark-read", "1", "--all"}, "notification IDs and --all are mutually exclusive"},
		{[]string{"notification", "mark-read", "1", "--status", "unread"}, "--status and --before require --all"},
		{[]string{"notification", "mark-read", "--all", "--before="}, "invalid --before: use a date, RFC3339 timestamp, or duration such as 24h"},
		{[]string{"notification", "mark-read", "9", "09"}, "resource 9 is selected more than once"},
		{[]string{"notification", "watch", "--interval", "0s"}, "--interval must be positive and --timeout not negative"},
		{[]string{"notification", "watch", "--timeout", "-1s"}, "--interval must be positive and --timeout not negative"},
		{[]string{"notification", "watch", "--before=-1h"}, "invalid --before: relative duration cannot be negative"},
		{[]string{"org", "create", "team", "--visibility", "unknown"}, "--visibility must be public, limited, or private"},
		{[]string{"org", "edit", "team"}, "supply organization fields to edit"},
		{[]string{"org", "list", "--all", "--username", "alice"}, "--all and --username are mutually exclusive"},
		{[]string{"org", "member", "list", "team", "--limit", "0"}, `"-L, --limit" flag: must be 1..1000`},
		{[]string{"pr", "attachment", "download", "1", "1", "--json"}, "attachment downloads are binary"},
		{[]string{"pr", "attachment", "download", "1", "1", "--output-file", "-", "--json=id"}, "attachment downloads are binary"},
		{[]string{"pr", "checkout", "7", "--detach", "--branch", "name"}, "--detach and --branch/--update are mutually exclusive"},
		{[]string{"pr", "create", "--milestone=-1"}, "--milestone must be a milestone title or a positive ID"},
		{[]string{"pr", "create", "--assignee", "alice, "}, "assignee names cannot be empty"},
		{[]string{"pr", "create", "--label", "bug, "}, "label names cannot be empty"},
		{[]string{"pr", "create", "--agit", "--json", "--title", "x", "--no-input"}, "--json/--jq cannot be combined with --agit"},
		{[]string{"pr", "create", "--topic", "x", "--title", "x", "--no-input"}, "--topic requires --agit"},
		{[]string{"pr", "create", "--reviewer", "bob, "}, "reviewer names cannot be empty"},
		{[]string{"pr", "create", "--reviewer-team", " "}, "team names cannot be empty"},
		{[]string{"pr", "create", "--reviewer", "bob,Bob"}, `reviewer "Bob" is duplicated`},
		{[]string{"pr", "create", "--agit", "--remote", "origin", "--reviewer", "bob"}, "--reviewer cannot be combined with --agit"},
		{[]string{"pr", "create", "--agit", "--remote", "origin", "--reviewer-team", "core"}, "--reviewer-team cannot be combined with --agit"},
		{[]string{"pr", "create", "--agit", "--remote", "origin", "--attachment", "log.txt"}, "--attachment cannot be combined with --agit"},
		{[]string{"pr", "create", "--attachment", "."}, ". is not a regular file"},
		{[]string{"pr", "edit", "7"}, "nothing to edit; pass a flag such as --title (see gt pr edit --help)"},
		{[]string{"pr", "edit", "7", "--remove-assignees="}, "nothing to edit"},
		{[]string{"pr", "edit", "7", "--milestone", "1", "--remove-milestone"}, "--milestone and --remove-milestone are mutually exclusive"},
		{[]string{"pr", "edit", "7", "--draft", "--ready"}, "--draft and --ready are mutually exclusive"},
		{[]string{"pr", "edit", "7", "--title="}, "pull request title cannot be empty"},
		{[]string{"pr", "edit", "7", "--base="}, "--base cannot be empty"},
		{[]string{"pr", "edit", "7", "--add-reviewers=alice", "--remove-reviewers=Alice"}, "reviewer \"Alice\" is duplicated or present in both add and remove"},
		{[]string{"pr", "edit", "7", "--add-reviewer-teams="}, "supply at least one reviewer or team"},
		{[]string{"pr", "list", "--sort", "unknown"}, "--sort must be oldest, recentupdate, recentclose, leastupdate, mostcomment, leastcomment, or priority"},
		{[]string{"pr", "list", "--milestone", "-1"}, "--milestone must be a milestone title or a positive ID"},
		{[]string{"pr", "list", "--state", "unknown"}, "--state must be open, closed, merged, or all"},
		{[]string{"pr", "list", "--limit", "0"}, `"-L, --limit" flag: must be 1..1000`},
		{[]string{"pr", "review", "1", "--no-input"}, "choose a review type: --approve, --request-changes, or --comment"},
		{[]string{"pr", "review", "1", "--comment", "--body="}, "a comment needs a body; pass --body, --body-file or --editor"},
		{[]string{"pr", "review", "1", "--request-changes", "--no-input"}, "a change request needs a body"},
		{[]string{"pr", "review", "1", "--event=unknown"}, "--event must be approve, approved, request-changes, request_changes, comment, or pending"},
		{[]string{"pr", "review", "request", "1", "--team", " "}, "team names cannot be empty"},
		{[]string{"pr", "review", "1", "--comments-file=-"}, "--comments-file requires a file"},
		{[]string{"pr", "review", "submit", "1", "2", "--event=PENDING"}, "--event must be approve, approved, request-changes, request_changes, or comment"},
		{[]string{"pr", "review", "submit", "1", "2", "--event=REQUEST_CHANGES", "--body="}, "a change request needs a body"},
		{[]string{"pr", "review", "dismiss", "1", "2", "--body="}, "a dismissal needs a reason"},
		{[]string{"pr", "review", "dismiss", "1", "2", "--no-input", "--yes"}, "missing required input: supply --body"},
		{[]string{"pr", "review", "delete", "1", "2", "--json"}, "review deletion does not return a resource"},
		{[]string{"pr", "status", "--limit=101"}, `"--limit" flag: must be 1..100`},
		{[]string{"pr", "status", "--json=title"}, "unknown JSON field \"title\""},
		{[]string{"release", "asset", "download", "v1", "1", "--json"}, "asset downloads are files"},
		{[]string{"release", "asset", "download", "v1", "1", "--archive", "zip"}, "--archive downloads the source code only"},
		{[]string{"release", "create", "v1", "--tag", "v2"}, "supply a tag argument or --tag, not both"},
		{[]string{"release", "create", "--body-file", "-"}, "--notes-file - requires a tag"},
		{[]string{"release", "edit", "v1"}, "supply release fields to edit"},
		{[]string{"release", "edit", "v1", "v2", "--tag", "v3"}, "--tag requires one release and a nonempty new tag"},
		{[]string{"repo", "create"}, "missing required input: supply --name"},
		{[]string{"repo", "create", "acme/bad/name"}, "repository name must be nonempty, at most 100 bytes, and contain no path separators or whitespace"},
		{[]string{"repo", "create", "name", "--name", "other"}, "supply name as an argument or --name, not both"},
		{[]string{"repo", "create", "name", "--default-branch", "bad..branch", "--auto-init"}, "invalid branch name"},
		{[]string{"repo", "create", "name", "--license", "MIT"}, "initial content and default branch options require --auto-init"},
		{[]string{"repo", "create", "name", "--trust-model", "unknown"}, "--trust-model must be default, collaborator, committer, or collaboratorcommitter"},
		{[]string{"repo", "create", "name", "--object-format", "unknown"}, "--object-format must be sha1 or sha256"},
		{[]string{"repo", "create", "name", "--team", "developers"}, "--team requires --org and a team name"},
		{[]string{"repo", "create", "name", "--org", "acme", "--team="}, "--team requires --org and a team name"},
		{[]string{"repo", "create", "name", "--copy-content=false"}, "copy options require --template owner/repo"},
		{[]string{"repo", "create", "name", "--template="}, "--template requires owner/repo or a repository URL"},
		{[]string{"repo", "create", "name", "--template", "owner/source", "--auto-init"}, "repository initialization options cannot be combined with --template"},
		{[]string{"repo", "create", "name", "--template", "owner/source", "-R", "owner/other"}, "--repo is not used by repo create; did you mean: gt repo create owner/name"},
		{[]string{"repo", "edit", "--name="}, "--name cannot be empty"},
		{[]string{"repo", "edit", "--default-branch="}, "--default-branch cannot be empty"},
		{[]string{"repo", "archive", "alice/project", "-R", "alice/other", "--yes"}, "supply the repository as an argument or --repo, not both"},
		{[]string{"repo", "view", "alice/project", "--repo", "alice/other"}, "supply the repository as an argument or --repo, not both"},
		{[]string{"repo", "view", "alice/project", "alice/other"}, "too many arguments\nUsage: gt repo view [<repository>] [flags]"},
		{[]string{"repo", "list", "--owner", "account", "--starred"}, "an owner, --starred and --watched select different collections; pass one"},
		{[]string{"repo", "list", "--starred", "--watched"}, "an owner, --starred and --watched select different collections; pass one"},
		{[]string{"repo", "list", "--type", "invalid"}, "--type must be fork, source, or mirror"},
		{[]string{"repo", "set-default", "--json"}, "set-default configures Git"},
		{[]string{"repo", "sync", "--branch="}, "--branch cannot be empty"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--clone-url", "https://other.test/repo"}, "supply clone URL as an argument or --clone-url, not both"},
		{[]string{"repo", "migrate", "https://user:secret@source.test/repo", "--name", "new"}, "clone URL must be an HTTP(S) or git URL without embedded credentials"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--service", "unknown"}, "--service must be git, gitea, gitlab, gogs, or github"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--issues"}, "plain Git migration cannot copy forge metadata"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--mirror-interval", "8h"}, "--mirror-interval requires --mirror and a positive duration"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--mirror", "--issues"}, "mirrors cannot copy issues, milestones, labels, pull requests, or releases"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--lfs-endpoint", "https://lfs.test"}, "--lfs-endpoint requires --lfs"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--auth-password", "secret"}, "--auth-password requires --auth-user"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--auth-token", "one", "--auth-token-file", "-"}, "--auth-token and --auth-token-file are mutually exclusive"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--auth-password-file", "-", "--auth-token-file", "-"}, "stdin can supply only one source credential"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--timeout", "0s"}, "--timeout must be positive"},
		{[]string{"repo", "migrate", "--server-path", "relative/path", "--name", "new"}, "--server-path must be an absolute path on the Gitea server"},
		{[]string{"repo", "migrate", "--server-path", "/server/repo", "--name", "new", "--auth-user", "account"}, "server path imports use --service git without remote credentials"},
		{[]string{"run", "list", "--since=-1h"}, "invalid --since: relative duration cannot be negative"},
		{[]string{"run", "list", "--until="}, "invalid --until: use a date, RFC3339 timestamp, or duration such as 24h"},
		{[]string{"run", "list", "--since=1h", "--until=2h"}, "--since must not be later than --until"},
		{[]string{"run", "list", "--limit", "0"}, `"-L, --limit" flag: must be 1..1000`},
		{[]string{"search", "issue", "query", "--state", "invalid"}, "--state must be open, closed, or all"},
		{[]string{"search", "issue", "query", "--author", "alice", "--authored"}, "--author and --authored are mutually exclusive"},
		{[]string{"search", "issue", "--label", ","}, "label names cannot be empty"},
		{[]string{"search", "pr", "query", "--since", "invalid"}, "--since: use a date, RFC3339 timestamp, or duration such as 24h"},
		{[]string{"search", "pr", "query", "--since", "2026-01-02T00:00:00Z", "--before", "2026-01-01T00:00:00Z"}, "--since must not be later than --until"},
		{[]string{"search", "issue", "--since="}, "invalid --since: use a date"},
		{[]string{"search", "repo", "query", "--sort", "invalid"}, "--sort must be alpha, created, updated, size, or id"},
		{[]string{"search", "repo", "query", "--order", "invalid"}, "--order must be asc or desc"},
		{[]string{"secret", "set", "BUILD_TOKEN", "--value", "text", "--value-file", "-"}, "--body and --body-file are mutually exclusive"},
		{[]string{"secret", "set", "BUILD_TOKEN", "--value= ", "--json=value"}, "unknown JSON field \"value\""},
		{[]string{"secret", "set", "BUILD_TOKEN", "--no-input"}, "missing value: pass --body, --body-file or standard input"},
		{[]string{"secret", "set", "CI", "--value", "text"}, "CI and names starting with GITEA_ or GITHUB_ are reserved"},
		{[]string{"status", "--limit=0"}, `"--limit" flag: must be 1..100`},
		{[]string{"status", "--json=token"}, "unknown JSON field \"token\""},
		{[]string{"team", "create", "org", "--name", "Developers", "--permission", "owner"}, "--permission must be read, write, or admin"},
		{[]string{"team", "create", "org", "--name", "Developers", "--unit", "repo.unknown=write"}, `unknown unit "repo.unknown"; use one of repo.code`},
		{[]string{"team", "create", "org", "--name", "Developers", "--unit", "repo.ext_wiki=write"}, "--unit repo.ext_wiki=write: access must be none, read"},
		{[]string{"team", "create", "org/Developers", "--name", "Developers"}, "supply the team name once"},
		{[]string{"team", "create", "org", "--name", "Developers", "--unit", "repo.code=write", "--permission", "admin"}, "--unit cannot restrict administrator access"},
		{[]string{"team", "edit", "1"}, "supply team fields to edit"},
		{[]string{"team", "edit", "1", "--unit="}, "must be formatted as key=value"},
		{[]string{"team", "member", "add", "invalid", "alice"}, `invalid team "invalid": use ORG/TEAM, such as acme/developers, or a team ID`},
		{[]string{"team", "view", "acme/"}, `invalid team "acme/"`},
		{[]string{"team", "repo", "add", "2", "service", "-R", "acme/service"}, "--repo is not used by gt team repo add"},
		{[]string{"time", "create", "1", "0s"}, `invalid duration "0s": time spent must be positive`},
		{[]string{"time", "create", "1", "1ms"}, `invalid duration "1ms": use whole seconds`},
		{[]string{"time", "list", "--issue", "0"}, `invalid issue "0"`},
		{[]string{"time", "list", "--mine", "--issue", "1"}, "--mine, --issue and --username select mutually exclusive scopes"},
		{[]string{"time", "list", "--from", "2026-10-02", "--until", "2026-10-01"}, "--since must not be later than --until"},
		{[]string{"time", "list", "--since", "1h", "--until", "2h"}, "--since must not be later than --until"},
		{[]string{"time", "list", "--total", "--json"}, "--total is a human-output summary"},
		{[]string{"variable", "set", "BUILD_MODE", "--value="}, "the value of BUILD_MODE is empty"},
		{[]string{"variable", "set", "GITEA_RESERVED", "--value", "text"}, "CI and names starting with GITEA_ or GITHUB_ are reserved"},
		{[]string{"variable", "list", "--org", "team", "--account"}, "--org, --account and --repo select different scopes; pass one"},
		{[]string{"variable", "list", "--org", "team", "--repo", "alice/project"}, "--org, --account and --repo select different scopes; pass one"},
		{[]string{"version", "--json"}, "version prints text"},
		{[]string{"webhook", "edit", "1", "--secret", "private", "--clear-authorization"}, "webhook secret editing is unsupported"},
		{[]string{"webhook", "create", "ftp://host/path"}, "webhook URL must be an absolute HTTP(S) URL without user information or fragment"},
		{[]string{"webhook", "create", "https://user:password@host/path"}, "webhook URL must be an absolute HTTP(S) URL without user information or fragment"},
		{[]string{"webhook", "create", "https://host/path#fragment"}, "webhook URL must be an absolute HTTP(S) URL without user information or fragment"},
		{[]string{"webhook", "create", "https://host/path", "--url", "https://other/path"}, "supply the URL as an argument or --url, not both"},
		{[]string{"webhook", "create", "https://host/path", "--secret", "one", "--secret-file", "-"}, "--secret and --secret-file are mutually exclusive"},
		{[]string{"webhook", "create", "https://host/path", "--secret-file", "-", "--authorization-header-file", "-"}, "stdin can supply only one webhook credential"},
		{[]string{"webhook", "create", "https://host/path", "--authorization-header", "one", "--clear-authorization"}, "choose one authorization header source or --clear-authorization"},
		{[]string{"webhook", "create", "https://host/path", "--authorization-header", "first\nsecond"}, "authorization header must be a single line"},
		{[]string{"webhook", "create", "https://host/path", "--events", ""}, "--events needs at least one trigger"},
		{[]string{"webhook", "create", "https://host/path", "--config", "secret=private"}, "--config cannot override the URL, secret, or webhook scope"},
		{[]string{"webhook", "edit", "1", "--active=false"}, "editing a webhook drops its stored authorization header unless it is sent again, and Gitea never returns it: pass --authorization-header-file or --authorization-header (use --clear-authorization only for a hook without one)"},
		{[]string{"webhook", "delete", "not-an-id"}, "invalid resource number \"not-an-id\""},
		{[]string{"webhook", "list", "--org", "organization", "--account"}, "--org, --account, --global, and --repo select mutually exclusive scopes"},
		{[]string{"webhook", "list", "--global", "-R", "owner/repo"}, "--org, --account, --global, and --repo select mutually exclusive scopes"},
		{[]string{"wiki", "create", "--body-file", "-"}, "--body-file - requires a title"},
		{[]string{"wiki", "create", "--title", "Home", "--body", "text", "--json=created_at"}, "unknown JSON field \"created_at\""},
		{[]string{"wiki", "edit", "Home", "--body="}, "wiki content cannot be empty"},
		{[]string{"wiki", "revisions", "Home", "--page", "0"}, `"--page" flag: must be a positive integer`},
		{[]string{"workflow", "run", "ci.yml", "-r", "main", "-f", "a=1", "-i", "a=2"}, "input a is given more than once"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			requireUsageBeforeRequests(t, tc.want, tc.args...)
		})
	}
}

func TestConfirmationsNameResolvedValues(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		inputs map[string]string
		want   string
	}{
		{[]string{"release", "create"}, map[string]string{"Tag": "v1.0.0"}, "Create release v1.0.0 in alice/project?"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusTeapot)
			}))
			defer server.Close()
			deps, _, _ := testDeps(t)
			deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
			prompt := &scriptedPrompter{inputs: tc.inputs}
			deps.Prompter = prompt
			deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
			err := Execute(context.Background(), deps, tc.args)
			require.Equal(t, clierr.Canceled, clierr.ExitCode(err), "%v", err)
			require.Len(t, prompt.confirmations, 1)
			require.Contains(t, prompt.confirmations[0], tc.want)
		})
	}
}

func TestPromptedCreateSubmitsWithoutConfirmation(t *testing.T) {
	for _, tc := range []struct {
		args          []string
		inputs        map[string]string
		secret, write string
	}{
		{[]string{"secret", "set", "TOKEN"}, nil, "private", "PUT /api/v1/repos/alice/project/actions/secrets/TOKEN"},
		{[]string{"variable", "set", "MODE"}, map[string]string{"Value of MODE:": "fast"}, "", "POST /api/v1/repos/alice/project/actions/variables/MODE"},
		{[]string{"webhook", "create"}, map[string]string{"Payload URL": "https://hooks.test/path"}, "", "POST /api/v1/repos/alice/project/hooks"},
		{[]string{"team", "create", "acme"}, map[string]string{"Team name": "Developers"}, "", "POST /api/v1/orgs/acme/teams"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var writes []string
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				writes = append(writes, r.Method+" "+r.URL.Path)
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":1}`))
			}))
			defer server.Close()
			deps, _, _ := testDeps(t)
			deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
			prompt := &scriptedPrompter{inputs: tc.inputs, secret: tc.secret}
			deps.Prompter = prompt
			deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
			require.NoError(t, Execute(context.Background(), deps, tc.args))
			require.Empty(t, prompt.confirmations)
			require.Equal(t, []string{tc.write}, writes)
		})
	}
}

type unexpectedForms struct{ t *testing.T }

func (f unexpectedForms) CreateIssue(context.Context, terminal.IssueFormChoices) (terminal.IssueFormValues, error) {
	f.t.Error("unexpected issue form")
	return terminal.IssueFormValues{}, nil
}

func (f unexpectedForms) EditIssue(context.Context, terminal.IssueEditFormChoices) (terminal.IssueEditFormValues, error) {
	f.t.Error("unexpected issue edit form")
	return terminal.IssueEditFormValues{}, nil
}

func (f unexpectedForms) CreatePull(context.Context, terminal.PullFormChoices) (terminal.PullFormValues, error) {
	f.t.Error("unexpected pull request form")
	return terminal.PullFormValues{}, nil
}

func (f unexpectedForms) CreateReview(context.Context, terminal.ReviewFormChoices) (terminal.ReviewFormValues, error) {
	f.t.Error("unexpected review form")
	return terminal.ReviewFormValues{}, nil
}

func TestExplicitValuesSkipInteractiveForms(t *testing.T) {
	for _, tc := range []struct {
		args                []string
		method, path, field string
	}{
		{[]string{"issue", "create", "--title", "Explicit"}, http.MethodPost, "/api/v1/repos/alice/project/issues", "number"},
		{[]string{"issue", "edit", "1", "--title", "Explicit"}, http.MethodPatch, "/api/v1/repos/alice/project/issues/1", "number"},
		{[]string{"pr", "create", "--title", "Explicit", "--head", "feature", "--base", "main"}, http.MethodPost, "/api/v1/repos/alice/project/pulls", "number"},
		{[]string{"pr", "review", "1", "--comment", "--body", "Explicit"}, http.MethodPost, "/api/v1/repos/alice/project/pulls/1/reviews", "id"},
	} {
		t.Run(strings.Join(tc.args[:2], " "), func(t *testing.T) {
			mutations := 0
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project":
					_, _ = w.Write([]byte(`{"full_name":"alice/project","default_branch":"main"}`))
					return
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/1":
					_, _ = w.Write([]byte(`{"number":1,"user":{"login":"bob"}}`))
					return
				}
				require.Equal(t, tc.method, r.Method)
				require.Equal(t, tc.path, r.URL.Path)
				mutations++
				_, _ = fmt.Fprintf(w, `{%q:1}`, tc.field)
			}))
			defer server.Close()
			deps, out, _ := serverDeps(t, server.URL)
			forms := unexpectedForms{t}
			deps.IssueForm, deps.IssueEditForm, deps.PullForm, deps.ReviewForm = forms, forms, forms, forms
			deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
			deps.Git = &fakeGit{results: map[string]string{
				"rev-parse --is-inside-work-tree":                                 "true",
				"rev-parse --verify refs/heads/feature^{commit}":                  "localsha",
				"for-each-ref --format=%(objectname) refs/remotes/origin/feature": "localsha",
				"remote":                             "origin",
				"remote get-url origin":              server.URL + "/alice/project.git",
				"config --get branch.feature.remote": "origin",
			}}
			require.NoError(t, Execute(context.Background(), deps, append(tc.args, "-R", "alice/project", "--json="+tc.field, "--interactive")))
			require.Equal(t, 1, mutations)
			require.JSONEq(t, fmt.Sprintf(`{%q:1}`, tc.field), out.String())
		})
	}
}
