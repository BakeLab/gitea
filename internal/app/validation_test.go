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
		{[]string{"admin", "user", "create", "account", "--email", "account@example.test", "--password", "one", "--password-file", "-"}, "--password cannot be combined with --password-file"},
		{[]string{"admin", "user", "create", "account", "--email", "account@example.test", "--password-file", ""}, "--password-file must name a file or -"},
		{[]string{"admin", "user", "create", "account", "--email", "account@example.test", "--password", "local-password", "--visibility", "invalid"}, "--visibility must be public, limited, or private"},
		{[]string{"admin", "user", "create", "account", "--username", "different"}, "a username argument cannot be combined with --username"},
		{[]string{"admin", "user", "create", "", "--username", "bob"}, "username cannot be empty"},
		{[]string{"admin", "user", "edit", "account"}, "supply account fields to edit"},
		{[]string{"admin", "user", "edit", "account", "--email="}, "--email: enter one email address"},
		{[]string{"admin", "user", "edit", "account", "--max-repo-creation", "-2"}, "--max-repo-creation must be -1 or nonnegative"},
		{[]string{"admin", "user", "edit", "account", "--password="}, "missing password: supply --password-file or --password"},
		{[]string{"admin", "user", "view", "bad/account"}, "invalid account username"},
		{[]string{"admin", "user", "rename", "bad/account", "other"}, "invalid account username"},
		{[]string{"admin", "user", "key", "delete", "bob", "x"}, `invalid resource number "x"`},
		{[]string{"admin", "org", "create", "acme"}, "admin org create requires --owner"},
		{[]string{"api", ""}, "endpoint cannot be empty"},
		{[]string{"tag", "protect", " "}, "pattern cannot be empty"},
		{[]string{"tag", "unprotect", "v*", " "}, "pattern cannot be empty"},
		{[]string{"tag", "protect", "v*"}, "tag protect requires --allow-user or --allow-team"},
		{[]string{"tag", "protect", "v*", "--allow-user", "bob,"}, "user names cannot be empty"},
		{[]string{"tag", "protect", "v*", "--allow-team", " "}, "team names cannot be empty"},
		{[]string{"tag", "create", "v1", "--target", " "}, "--target cannot be empty"},
		{[]string{"extension", "install", "alice/hello"}, `extension repository "hello" must be named gt-NAME or gh-NAME`},
		{[]string{"extension", "install", "alice/gt-Bad_Name"}, `invalid extension name "bad_name"`},
		{[]string{"extension", "install", "https://github.com/owner"}, "invalid GitHub repository"},
		{[]string{"extension", "install", ".", "--pin", "v1"}, "--pin cannot be combined with a local extension"},
		{[]string{"extension", "upgrade"}, "extension upgrade requires extension names or --all"},
		{[]string{"extension", "upgrade", "--all", "--no-input"}, "confirmation required to upgrade all extensions; rerun with --yes"},
		{[]string{"extension", "remove", "missing"}, `no extension named "missing"`},
		{[]string{"upgrade", "--nightly", "--version", "1.0.0"}, "--nightly cannot be combined with --version"},
		{[]string{"upgrade", "--version", "latest"}, `--version requires a release such as 1.0.0, not "latest"`},
		{[]string{"upgrade"}, "confirmation required to upgrade gt; rerun with --yes"},
		{[]string{"extension", "exec"}, "extension exec requires an extension name"},
		{[]string{"extension", "create", "Bad_Name"}, `invalid extension name "Bad_Name"`},
		{[]string{"extension", "create", "repo"}, "extension repo cannot be created: gt repo is a built-in command"},
		{[]string{"extension", "install", "alice/gt-pr"}, "extension pr cannot be installed: gt pr is a built-in command"},
		{[]string{"extension", "install", "alice/gt-hello", "--no-input"}, "confirmation required to install extension hello; rerun with --yes"},
		{[]string{"api", "user", "--input", "-", "-f", "x=y"}, "--input cannot be combined with API fields"},
		{[]string{"api", "user", "-f", "x=y", "-F", "x=2"}, "API field names must be unique"},
		{[]string{"api", "user", "-f", "missing-equals"}, "API fields require name=value"},
		{[]string{"api", "user", "--paginate", "-X", "POST"}, "--paginate requires a body-free GET request"},
		{[]string{"api", "user", "--input="}, "--input requires a file path or -"},
		{[]string{"api", "user", "-X", "invalid/method"}, "invalid HTTP method"},
		{[]string{"alias", "delete", "co", "--json"}, "alias deletion does not return a resource"},
		{[]string{"alias", "list", "--json", "foo"}, "--json with field names requires aliases; none are defined"},
		{[]string{"alias", "delete", "co", "--csv"}, "alias deletion does not return a resource; --csv is not supported"},
		{[]string{"alias", "list", "--csv", "foo"}, "--csv with field names requires aliases; none are defined"},
		{[]string{"issue", "list", "--csv", "--json"}, "--csv cannot be combined with --json"},
		{[]string{"issue", "list", "--csv=number", "--jq", "."}, "--csv cannot be combined with --jq"},
		{[]string{"issue", "list", "--csv="}, "--csv field names cannot be empty; use --csv alone for every field"},
		{[]string{"issue", "list", "--csv=number, "}, "--csv field names cannot be empty; use --csv alone for every field"},
		{[]string{"issue", "list", "--csv", "titel"}, "unknown JSON field \"titel\""},
		{[]string{"auth", "status", "--json=token"}, "unknown JSON field \"token\""},
		{[]string{"auth", "logout", "", "--yes"}, "account cannot be empty"},
		{[]string{"auth", "switch", " "}, "account cannot be empty"},
		{[]string{"auth", "logout", "--user", "", "--yes"}, "--user cannot be empty"},
		{[]string{"auth", "switch", "--user", " "}, "--user cannot be empty"},
		{[]string{"issue", "list", "--user="}, "--user cannot be empty"},
		{[]string{"issue", "list", "--cwd", "missing-directory"}, "--cwd missing-directory is not a directory"},
		{[]string{"branch", "unprotect", "", "-y"}, "branch name cannot be empty"},
		{[]string{"branch", "protect", "main", "--allow-push", " "}, "pusher names cannot be empty"},
		{[]string{"branch", "protect", "main", "--status-check", " "}, "status check names cannot be empty"},
		{[]string{"browse", ""}, "number, path or commit SHA cannot be empty"},
		{[]string{"browse", "--branch", "main", "--commit"}, "--branch cannot be combined with --commit"},
		{[]string{"browse", "--wiki", "--releases"}, "--releases cannot be combined with --wiki"},
		{[]string{"browse", "#3", "--branch", "main"}, "--branch cannot be combined with an issue or pull request number"},
		{[]string{"browse", "77507cd", "--commit=1234abc"}, "--commit cannot be combined with a commit SHA"},
		{[]string{"browse", "docs", "--settings"}, "--settings cannot be combined with a path"},
		{[]string{"browse", "--branch="}, "invalid branch name"},
		{[]string{"completion", "bash", "--json"}, "completion prints a shell script; --json is not supported"},
		{[]string{"completion", "zsh", "--jq", "."}, "completion prints a shell script; --jq is not supported"},
		{[]string{"completion", "fish", "extra"}, `unexpected argument "extra"`},
		{[]string{"config", "set", "prompt", "invalid"}, "prompt must be enabled or disabled"},
		{[]string{"doctor", "--json=secret"}, "unknown JSON field \"secret\""},
		{[]string{"issue", "attachment", "download", "1", "1", "--json"}, "attachment downloads are binary"},
		{[]string{"issue", "attachment", "download", "1", "1", "--output", "-", "--json=id"}, "attachment downloads are binary"},
		{[]string{"issue", "attachment", "download", "1", "1", "--output", "-", "--clobber"}, "--clobber cannot be combined with --output -"},
		{[]string{"issue", "attachment", "download", "https://example.test/alice/project/issues/1", "1", "--output", "-", "--skip-existing"}, "--skip-existing cannot be combined with --output -"},
		{[]string{"issue", "close", "1", "bad"}, `invalid issue "bad": use a number, #number, OWNER/REPO#number or URL`},
		{[]string{"issue", "close", "1", "#1", "--no-input"}, "#1 is selected more than once"},
		{[]string{"issue", "close", "bob/tool#1", "2"}, "2 cannot be combined with bob/tool#1; qualify it as bob/tool#2, or run a separate command for alice/project#2"},
		{[]string{"issue", "reopen", "1", "01", "--no-input"}, "#1 is selected more than once"},
		{[]string{"issue", "comment", "create", "1", "--body="}, "comment body cannot be empty"},
		{[]string{"issue", "comment", "create", "1", "--no-input"}, "issue comment create requires --body, --body-file or --editor"},
		{[]string{"issue", "comment", "1", "--edit-last", "--no-input"}, "issue comment requires --body, --body-file or --editor"},
		{[]string{"issue", "comment", "edit", "1", "--no-input"}, "issue comment edit requires --body, --body-file or --editor"},
		{[]string{"issue", "comment", "1", "--body-file", "-"}, "comment body cannot be empty"},
		{[]string{"issue", "comment", "create", "1", "--body", "text", "--json=title"}, "unknown JSON field \"title\""},
		{[]string{"issue", "create", "--no-input"}, "issue create requires --title"},
		{[]string{"issue", "create", "--template", "bug", "--no-input"}, "issue create requires --title"},
		{[]string{"issue", "create", "--template", "bug", "--body-file", "-"}, "--body-file - requires --title"},
		{[]string{"issue", "create", "--title", " "}, "issue title cannot be empty"},
		{[]string{"label", "import", ""}, "file cannot be empty"},
		{[]string{"ssh-key", "create", ""}, "file cannot be empty"},
		{[]string{"repo", "deploy-key", "add", ""}, "file cannot be empty"},
		{[]string{"issue", "attachment", "upload", "1", ""}, "file cannot be empty"},
		{[]string{"issue", "comment", "attachment", "upload", "81", ""}, "file cannot be empty"},
		{[]string{"issue", "comment", "attachment", "view", "81", "x"}, `invalid resource number "x"`},
		{[]string{"pr", "comment", "attachment", "download", "81", "1", "--json"}, "attachment downloads are binary"},
		{[]string{"pr", "comment", "attachment", "edit", "81", "1", "--name="}, "supply a nonempty --name"},
		{[]string{"issue", "move", "1", "to", " "}, "column cannot be empty"},
		{[]string{"issue", "move", "1", "Todo", "--project="}, "--project cannot be empty"},
		{[]string{"issue", "move", "1", "In", "progress"}, "issue move takes one column"},
		{[]string{"issue", "edit", "1", "--add-label", "bug", "--remove-label", "Bug"}, `--add-label "bug" cannot be combined with --remove-label "Bug"`},
		{[]string{"pr", "edit", "1", "--add-assignee", "@me", "--remove-assignee", "@me"}, `--add-assignee "@me" cannot be combined with --remove-assignee "@me"`},
		{[]string{"repo", "edit", "--add-topic", "go", "--remove-topic", "Go"}, `--add-topic "go" cannot be combined with --remove-topic "Go"`},
		{[]string{"issue", "list", "--select"}, "--select requires a terminal"},
		{[]string{"issue", "list", "--select", "--json"}, "--select cannot be combined with --json"},
		{[]string{"issue", "list", "--select", "--web"}, "--select cannot be combined with --web"},
		{[]string{"pr", "list", "--select", "--local"}, "--select cannot be combined with --local"},
		{[]string{"release", "list", "--select", "--csv"}, "--select cannot be combined with --csv"},
		{[]string{"repo", "list", "--select", "--jq", "."}, "--select cannot be combined with --jq"},
		{[]string{"issue", "list", "--select", "--template", "{{.}}"}, "--select cannot be combined with --template"},
		{[]string{"notification", "list", "--all-accounts", "-R", "alice/project"}, "--all-accounts cannot be combined with --repo"},
		{[]string{"notification", "list", "--all-accounts", "--this-repo"}, "--all-accounts cannot be combined with --this-repo"},
		{[]string{"notification", "list", "--all-accounts", "--page", "2"}, "--all-accounts cannot be combined with --page"},
		{[]string{"issue", "create", "--title", "safe", "--json=unknown"}, "unknown JSON field \"unknown\""},
		{[]string{"issue", "create", "--title", "safe", "--json", "--jq", "["}, "invalid --jq expression"},
		{[]string{"issue", "close", "5", "--dry-run", "--jq", "["}, "invalid --jq expression"},
		{[]string{"issue", "close", "5", "--dry-run", "--csv"}, "--dry-run cannot be combined with --csv"},
		{[]string{"api", "user", "--dry-run", "--output-file", "new.json"}, "--dry-run cannot be combined with --output-file"},
		{[]string{"workflow", "run", "ci.yml", "--dry-run", "--follow"}, "--dry-run cannot be combined with --follow"},
		{[]string{"pr", "merge", "5", "--dry-run", "--editor"}, "--dry-run cannot be combined with --editor"},
		{[]string{"pr", "merge", "5", "--dry-run", "--interactive"}, "--dry-run cannot be combined with --interactive"},
		{[]string{"issue", "create", "--milestone="}, "--milestone requires a title or ID; omit --milestone for none"},
		{[]string{"issue", "edit", "1", "--milestone="}, "--milestone requires a title or ID; use --remove-milestone to clear it"},
		{[]string{"issue", "create", "--web", "--label", "bug"}, "--label cannot be combined with --web"},
		{[]string{"issue", "create", "--web", "--deadline", "2026-12-24"}, "--deadline cannot be combined with --web"},
		{[]string{"issue", "create", "--assignee", "alice, "}, "assignee names cannot be empty"},
		{[]string{"issue", "create", "--template="}, "--template cannot be empty"},
		{[]string{"issue", "create", "--title", "x", "--attachment", "."}, ". is not a regular file"},
		{[]string{"issue", "create", "--milestone="}, "--milestone requires a title or ID"},
		{[]string{"issue", "edit", "1", "--milestone", "v1", "--remove-milestone"}, "--milestone cannot be combined with --remove-milestone"},
		{[]string{"issue", "edit", "1"}, "nothing to edit; pass a flag such as --title (see --help)"},
		{[]string{"issue", "edit", "1", "--editor=false"}, "nothing to edit"},
		{[]string{"issue", "edit", "1", "--add-label="}, "nothing to edit"},
		{[]string{"issue", "edit", "1", "--title="}, "issue title cannot be empty"},
		{[]string{"issue", "edit", "1", "--set-assignees", "alice", "--add-assignee", "bob"}, "--set-assignees cannot be combined with --add-assignee or --remove-assignee"},
		{[]string{"issue", "edit", "1", "--set-labels", "bug", "--add-label", "help"}, "--set-labels cannot be combined with --add-label or --remove-label"},
		{[]string{"issue", "edit", "1", "--add-assignee", " "}, "assignee names cannot be empty"},
		{[]string{"pr", "checkout", "feature"}, "pr checkout requires a Git clone"},
		{[]string{"pr", "backport", "7"}, "pr backport requires --to"},
		{[]string{"pr", "backport", "7", "--to", " "}, "branch names cannot be empty"},
		{[]string{"pr", "backport", "7", "--to", "a", "--to", "b", "--branch", "x"}, "--branch cannot be combined with more than one --to"},
		{[]string{"pr", "backport", "7", "--to", "a", "--to", "b", "--keep"}, "--keep cannot be combined with more than one --to"},
		{[]string{"pr", "backport", "7", "--to", "a", "--branch", " "}, "--branch cannot be empty"},
		{[]string{"pr", "backport", "7", "--continue"}, "--continue cannot be combined with a pull request"},
		{[]string{"pr", "backport", "--continue", "--to", "a"}, "--to cannot be combined with --continue"},
		{[]string{"pr", "backport", "7", "--to", "a", "--milestone", "-1"}, "--milestone must be a milestone title or a positive ID"},
		{[]string{"pr", "backport", "7", "--to", "bad..x"}, "invalid branch name"},
		{[]string{"pr", "backport", "7", "--to", "a", "--branch", "bad..x"}, "invalid branch name"},
		{[]string{"pr", "backport", "7", "--to", "a"}, "pr backport requires a Git clone"},
		{[]string{"pr", "checkout", "7", "--branch", "bad..x"}, "invalid branch name"},
		{[]string{"pr", "clean", "feature", "--yes"}, "pr clean requires a Git clone"},
		{[]string{"issue", "edit", "1", "--add-project", "Board", "--remove-project", "Board"}, `--add-project "Board" cannot be combined with --remove-project "Board"`},
		{[]string{"pr", "edit", "1", "--add-project", " Board", "--remove-project", "Board"}, `--add-project "Board" cannot be combined with --remove-project "Board"`},
		{[]string{"issue", "edit", "1", "invalid", "--title", "Updated"}, `invalid issue "invalid"`},
		{[]string{"issue", "edit", "1", "2", "--editor"}, "--editor requires one issue"},
		{[]string{"issue", "edit", "1", "--editor"}, "configure an editor"},
		{[]string{"issue", "comment", "create", "1", "--editor"}, "configure an editor"},
		{[]string{"pr", "review", "1", "--comment", "--editor"}, "configure an editor"},
		{[]string{"pr", "create", "--editor", "--yes"}, "configure an editor"},
		{[]string{"issue", "edit", "1", "01", "--title", "Updated", "--no-input"}, "#1 is selected more than once"},
		{[]string{"issue", "list", "--limit", "0"}, `"-L, --limit" flag: must be 1..1000`},
		{[]string{"issue", "list", "--page", "0"}, `"--page" flag: must be a positive integer`},
		{[]string{"issue", "list", "--label", "bug, "}, "label names cannot be empty"},
		{[]string{"label", "delete", "", "-y"}, "label name cannot be empty"},
		{[]string{"label", "edit", "", "--name", "x"}, "label name cannot be empty"},
		{[]string{"label", "create", "bug", "--color", "wrong"}, "--color requires six hexadecimal digits"},
		{[]string{"label", "edit", "1"}, "nothing to edit; pass a flag such as --name or --color (see --help)"},
		{[]string{"label", "list", "--org", "team", "-R", "a/b"}, "--org cannot be combined with --repo"},
		{[]string{"milestone", "view", ""}, "milestone title or ID cannot be empty"},
		{[]string{"milestone", "issue", "add", "", "1"}, "milestone title or ID cannot be empty"},
		{[]string{"milestone", "close", " ", "--by-name"}, "milestone title or ID cannot be empty"},
		{[]string{"milestone", "close", "m1", "--by-name", "--json", "titel"}, `unknown JSON field "titel"`},
		{[]string{"milestone", "create", "", "--title", "v1"}, "milestone title cannot be empty"},
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
		{[]string{"notification", "list", "--since=1h", "--until=2h"}, "--since must not be later than --until"},
		{[]string{"notification", "watch", "--until=1h", "--before=2h"}, "--until cannot be combined with --before"},
		{[]string{"notification", "mark-read", "--all", "--until=1h", "--before=2h"}, "--until cannot be combined with --before"},
		{[]string{"notification", "mark-read"}, "notification mark-read requires notification IDs or --all"},
		{[]string{"notification", "mark-read", "1", "--all"}, "--all cannot be combined with notification IDs"},
		{[]string{"notification", "mark-read", "1", "--status", "unread"}, "--status and --until require --all"},
		{[]string{"notification", "mark-read", "--all", "--until="}, "invalid --until: use a date, RFC3339 timestamp, or duration such as 24h"},
		{[]string{"notification", "mark-read", "9", "09"}, "resource 9 is selected more than once"},
		{[]string{"notification", "watch", "--interval", "0s"}, "--interval must be positive"},
		{[]string{"notification", "watch", "--timeout", "-1s"}, "--timeout cannot be negative"},
		{[]string{"notification", "watch", "--until=-1h"}, "invalid --until: relative duration cannot be negative"},
		{[]string{"org", "create", "team", "--visibility", "unknown"}, "--visibility must be public, limited, or private"},
		{[]string{"org", "edit", "team"}, "supply organization fields to edit"},
		{[]string{"org", "list", "--all", "--username", "alice"}, "--all cannot be combined with --username"},
		{[]string{"org", "member", "list", "team", "--limit", "0"}, `"-L, --limit" flag: must be 1..1000`},
		{[]string{"org", "view", " "}, "organization name cannot be empty"},
		{[]string{"org", "member", "list", ""}, "organization name cannot be empty"},
		{[]string{"package", "unlink", "generic", "", "--owner", "alice"}, "package name cannot be empty"},
		{[]string{"package", "view", "generic", "", "--owner", "alice"}, "package name cannot be empty"},
		{[]string{"package", "versions", "", "x", "--owner", "alice"}, "package type cannot be empty"},
		{[]string{"package", "delete", "generic", "", "1", "-y", "--owner", "alice"}, "package name cannot be empty"},
		{[]string{"pr", "view", ""}, "pull request cannot be empty"},
		{[]string{"pr", "merge", " ", "-y"}, "pull request cannot be empty"},
		{[]string{"pr", "attachment", "download", "1", "1", "--json"}, "attachment downloads are binary"},
		{[]string{"pr", "attachment", "download", "1", "1", "--output", "-", "--json=id"}, "attachment downloads are binary"},
		{[]string{"pr", "checkout", "7", "--detach", "--branch", "name"}, "--detach cannot be combined with --branch or --update"},
		{[]string{"pr", "create", "--milestone=-1"}, "--milestone must be a milestone title or a positive ID"},
		{[]string{"pr", "create", "--assignee", "alice, "}, "assignee names cannot be empty"},
		{[]string{"pr", "create", "--label", "bug, "}, "label names cannot be empty"},
		{[]string{"pr", "create", "--agit", "--json", "--title", "x", "--no-input"}, "--json, --jq or --csv cannot be combined with --agit"},
		{[]string{"pr", "create", "--topic", "x", "--title", "x", "--no-input"}, "--topic requires --agit"},
		{[]string{"pr", "create", "--agit", "--topic", "main", "--base", "main", "--title", "x", "--no-input"}, "the AGit topic main must differ from the base branch"},
		{[]string{"pr", "checks", "1", "--interval", "1s"}, "--interval requires --watch"},
		{[]string{"pr", "checks", "1", "--timeout", "1m"}, "--timeout requires --watch"},
		{[]string{"pr", "checks", "1", "--web", "--watch"}, "--watch cannot be combined with --web"},
		{[]string{"pr", "create", "--agit", "--web", "--title", "x", "--no-input"}, "--web cannot be combined with --agit"},
		{[]string{"pr", "create", "--web", "--label", "bug", "--head", "feature"}, "--label cannot be combined with --web; the browser only receives --head, --base"},
		{[]string{"pr", "create", "--reviewer", "bob, "}, "reviewer names cannot be empty"},
		{[]string{"pr", "create", "--reviewer-team", " "}, "team names cannot be empty"},
		{[]string{"pr", "create", "--reviewer", "bob,Bob"}, `reviewer "Bob" is duplicated`},
		{[]string{"pr", "create", "--reviewer", "bob, @me"}, "--reviewer cannot include @me"},
		{[]string{"pr", "create", "--agit", "--remote", "origin", "--reviewer", "bob"}, "--reviewer cannot be combined with --agit"},
		{[]string{"pr", "create", "--agit", "--remote", "origin", "--reviewer-team", "core"}, "--reviewer-team cannot be combined with --agit"},
		{[]string{"pr", "create", "--agit", "--remote", "origin", "--attachment", "log.txt"}, "--attachment cannot be combined with --agit"},
		{[]string{"pr", "create", "--agit", "--remote", "origin", "--allow-maintainer-edits=false"}, "--allow-maintainer-edits cannot be combined with --agit"},
		{[]string{"pr", "create", "--attachment", "."}, ". is not a regular file"},
		{[]string{"pr", "edit", "7"}, "nothing to edit; pass a flag such as --title (see gt pr edit --help)"},
		{[]string{"pr", "edit", "7", "--remove-assignee="}, "nothing to edit"},
		{[]string{"pr", "edit", "7", "--milestone", "1", "--remove-milestone"}, "--milestone cannot be combined with --remove-milestone"},
		{[]string{"pr", "edit", "7", "--draft", "--ready"}, "--draft cannot be combined with --ready"},
		{[]string{"pr", "edit", "7", "--title="}, "pull request title cannot be empty"},
		{[]string{"pr", "edit", "7", "--base="}, "--base cannot be empty"},
		{[]string{"pr", "edit", "7", "--add-reviewer=alice", "--remove-reviewer=Alice"}, "reviewer \"Alice\" is duplicated or present in both add and remove"},
		{[]string{"pr", "edit", "7", "--add-reviewer-team="}, "supply at least one reviewer or team"},
		{[]string{"pr", "list", "--sort", "unknown"}, "--sort must be oldest, recentupdate, recentclose, leastupdate, mostcomment, leastcomment, or priority"},
		{[]string{"pr", "list", "--milestone", "-1"}, "--milestone must be a milestone title or a positive ID"},
		{[]string{"pr", "list", "--state", "unknown"}, "--state must be open, closed, merged, or all"},
		{[]string{"pr", "list", "--limit", "0"}, `"-L, --limit" flag: must be 1..1000`},
		{[]string{"pr", "list", "--local", "--state", "closed"}, "--local cannot be combined with --state"},
		{[]string{"pr", "list", "--local", "--author", "@me"}, "--local cannot be combined with --author"},
		{[]string{"pr", "list", "--local", "--limit", "5"}, "--local cannot be combined with --limit"},
		{[]string{"pr", "list", "--local", "--web"}, "--local cannot be combined with --web"},
		{[]string{"pr", "list", "--local", "-R", "alice/project"}, "--local cannot be combined with --repo"},
		{[]string{"pr", "list", "--local"}, "--local requires a Git clone"},
		{[]string{"pr", "list", "--json=branch"}, `unknown JSON field "branch"`},
		{[]string{"pr", "review", "1", "--no-input"}, "choose one review type: --approve, --request-changes, --comment or --pending"},
		{[]string{"pr", "review", "1", "--request-changes", "--no-input"}, "--request-changes requires a body"},
		{[]string{"pr", "review", "reply", "1", "7", "--no-input"}, "pr review reply requires --body, --body-file or --editor"},
		{[]string{"pr", "review", "request", "1", "--team", " "}, "team names cannot be empty"},
		{[]string{"pr", "review", "1", "--comments-file=-"}, "--comments-file requires a file"},
		{[]string{"pr", "review", "submit", "1", "2", "--request-changes", "--body="}, "--request-changes requires a body"},
		{[]string{"pr", "review", "dismiss", "1", "2", "--body="}, "pr review dismiss requires a reason; pass --body, --body-file or --editor"},
		{[]string{"pr", "review", "dismiss", "1", "2", "--no-input", "--yes"}, "pr review dismiss requires a reason; pass --body, --body-file or --editor"},
		{[]string{"pr", "review", "delete", "1", "2", "--json"}, "review deletion does not return a resource"},
		{[]string{"pr", "status", "--limit=101"}, `"--limit" flag: must be 1..100`},
		{[]string{"pr", "status", "--json=title"}, "unknown JSON field \"title\""},
		{[]string{"repo", "deploy-key", "delete"}, "missing key ID"},
		{[]string{"release", "view", ""}, "tag cannot be empty"},
		{[]string{"release", "download", ""}, "tag cannot be empty"},
		{[]string{"release", "delete", "", "-y"}, "tag cannot be empty"},
		{[]string{"release", "edit", "", "--title", "x"}, "tag cannot be empty"},
		{[]string{"release", "asset", "delete", "v1", " ", "-y"}, "asset name cannot be empty"},
		{[]string{"release", "asset", "download", "v1", "1", "--json"}, "asset downloads are files"},
		{[]string{"release", "asset", "download", "v1", "1", "--archive", "zip"}, "--archive downloads the source code only"},
		{[]string{"release", "download", "v1", "--output", "-", "--clobber"}, "--clobber cannot be combined with --output -"},
		{[]string{"release", "download", "v1", "--archive", "zip", "--output", "-", "--skip-existing"}, "--skip-existing cannot be combined with --output -"},
		{[]string{"release", "download", "v1", "--clobber", "--skip-existing"}, "--clobber cannot be combined with --skip-existing"},
		{[]string{"release", "create", "--notes-file", "-"}, "--notes-file - requires a tag"},
		{[]string{"release", "edit", "v1"}, "nothing to edit; pass a flag such as --title or --notes (see --help)"},
		{[]string{"release", "edit", "v1", "--notes", ""}, "release notes cannot be cleared"},
		{[]string{"release", "edit", "v1", "v2", "--tag", "v3"}, "--tag requires one release and a nonempty new tag"},
		{[]string{"release", "edit", "v3", "--tag", "v3.0", "--json", "tag_name"}, `unknown JSON field "tag_name"`},
		{[]string{"release", "publish", "v11", "--json", "tag_name"}, `unknown JSON field "tag_name"`},
		{[]string{"release", "view", "--json", "tag_name", "v3"}, `unknown JSON field "tag_name"`},
		{[]string{"release", "edit", "v1", "--draft", "false"}, "--draft takes no value; use --draft or --draft=false"},
		{[]string{"release", "edit", "v1", "-d", "false", "-p", "False"}, "--draft takes no value; use --draft or --draft=false"},
		{[]string{"release", "edit", "v1", "v2", "--prerelease", "TRUE"}, "--prerelease takes no value; use --prerelease or --prerelease=false"},
		{[]string{"repo", "clone", ""}, "repository cannot be empty"},
		{[]string{"repo", "clone", "alice/project", " "}, "directory cannot be empty"},
		{[]string{"repo", "clone", "alice/project", "--find-instance", "--host", "https://example.test"}, "--find-instance cannot be combined with --host"},
		{[]string{"repo", "clone", "alice/project", "-u", ""}, "--upstream-remote-name requires a name; use --no-upstream"},
		{[]string{"repo", "clone", "alice/project", "-u", "bad name"}, "--upstream-remote-name requires a valid Git remote name"},
		{[]string{"repo", "clone", "alice/project", "-u", "origin"}, "--upstream-remote-name cannot be the fork's remote name origin"},
		{[]string{"repo", "clone", "alice/project", "-u", "fork", "--", "--origin", "fork"}, "--upstream-remote-name cannot be the fork's remote name fork"},
		{[]string{"repo", "clone", "alice/project", "-u", "fork", "--", "-ofork"}, "--upstream-remote-name cannot be the fork's remote name fork"},
		{[]string{"repo", "fork", "alice/project", "--clone", "--", "-o", "upstream"}, "repo fork --clone requires a Git remote name for the fork other than upstream, the name of its parent's remote"},
		{[]string{"repo", "fork", "alice/project", "--clone", "--", "--config=clone.defaultRemoteName=upstream"}, "repo fork --clone requires a Git remote name for the fork other than upstream, the name of its parent's remote"},
		{[]string{"repo", "delete", "", "-y"}, "repository cannot be empty"},
		{[]string{"repo", "view", " "}, "repository cannot be empty"},
		{[]string{"repo", "list", ""}, "owner cannot be empty"},
		{[]string{"repo", "create"}, "repo create requires a repository name argument"},
		{[]string{"repo", "create", "acme/bad/name"}, "repository name must be nonempty, at most 100 bytes, and contain no path separators or whitespace"},
		{[]string{"repo", "create", "name", "--default-branch", "bad..branch", "--auto-init"}, "invalid branch name"},
		{[]string{"repo", "create", "name", "--license", "MIT"}, "--license requires --auto-init"},
		{[]string{"repo", "create", "name", "--default-branch", "trunk"}, "--default-branch requires --auto-init"},
		{[]string{"repo", "create", "name", "--push"}, "--push requires --source"},
		{[]string{"repo", "create", "name", "--push", "--remote-name", "up"}, "--push and --remote-name require --source"},
		{[]string{"repo", "create", "name", "--trust-model", "unknown"}, "--trust-model must be default, collaborator, committer, or collaboratorcommitter"},
		{[]string{"repo", "create", "name", "--object-format", "unknown"}, "--object-format must be sha1 or sha256"},
		{[]string{"repo", "create", "name", "--team", "developers"}, "--team requires --org and a team name"},
		{[]string{"repo", "create", "name", "--org", "acme", "--team="}, "--team requires --org and a team name"},
		{[]string{"repo", "create", "name", "--copy-content=false"}, "copy options require --template owner/repo"},
		{[]string{"repo", "create", "name", "--template="}, "--template requires owner/repo or a repository URL"},
		{[]string{"repo", "create", "name", "--template", "owner/source", "--auto-init"}, "repository initialization options cannot be combined with --template"},
		{[]string{"repo", "create", "name", "--template", "owner/source", "-R", "owner/other"}, "--repo is not used by repo create; did you mean: gt repo create owner/name"},
		{[]string{"repo", "edit", "--default-branch="}, "--default-branch cannot be empty"},
		{[]string{"repo", "edit"}, "nothing to edit; pass a flag such as --description (see --help)"},
		{[]string{"repo", "edit", "-d", strings.Repeat("x", 2049)}, "description cannot exceed 2048 characters"},
		{[]string{"repo", "fork", "alice/project", "--add-remote", "--remote-name", "-x"}, "--remote-name requires a valid Git remote name"},
		{[]string{"repo", "fork", "alice/project", "--fork-name", "a b"}, "repository name must be nonempty, at most 100 bytes, and contain no path separators or whitespace"},
		{[]string{"repo", "transfer", "acme", "--team", "", "--yes"}, "--team requires a team name"},
		{[]string{"repo", "archive", "alice/project", "-R", "alice/other", "--yes"}, "a repository argument cannot be combined with --repo"},
		{[]string{"repo", "view", "alice/project", "--repo", "alice/other"}, "a repository argument cannot be combined with --repo"},
		{[]string{"repo", "view", "alice/project", "alice/other"}, "too many arguments\nUsage: gt repo view [<repository>] [flags]"},
		{[]string{"repo", "list", "account", "--starred"}, "an owner argument cannot be combined with --starred"},
		{[]string{"repo", "list", "--starred", "--watched"}, "--starred cannot be combined with --watched"},
		{[]string{"repo", "set-default", "alice/project", "--view"}, "--view cannot be combined with a repository argument"},
		{[]string{"repo", "set-default", "alice/project", "--remote", "origin"}, "--remote cannot be combined with a repository argument or --repo"},
		{[]string{"repo", "set-default", "--unset", "--remote", "upstream"}, "--remote cannot be combined with --unset"},
		{[]string{"repo", "set-default", "--view", "--remote", "upstream"}, "--remote cannot be combined with --view"},
		{[]string{"repo", "set-default", "--remote", "upstream"}, "--remote cannot be combined with GITEA_CLI_REPO"},
		{[]string{"repo", "list", "--type", "invalid"}, "--type must be fork, source, or mirror"},
		{[]string{"repo", "set-default", "--json"}, "set-default configures Git"},
		{[]string{"repo", "sync", "--branch="}, "--branch cannot be empty"},
		{[]string{"repo", "sync", "--branch", "bad..x"}, "invalid branch name"},
		{[]string{"repo", "migrate", "--name", "new"}, "repo migrate requires a clone URL argument"},
		{[]string{"repo", "migrate", "https://user:secret@source.test/repo", "--name", "new"}, "clone URL must be an HTTP(S) or git URL without embedded credentials"},
		{[]string{"repo", "migrate", "/srv/git/project.git", "--name", "new"}, "for a path on the Gitea server, use --server-path"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--lfs", "--lfs-endpoint", "file:///lfs"}, "--lfs-endpoint must be an HTTP(S) or git URL"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--service", "unknown"}, "--service must be git, gitea, gitlab, gogs, or github"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--issues"}, "plain Git migration cannot copy forge metadata"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--mirror-interval", "8h"}, "--mirror-interval requires --mirror and a duration of 0 or more"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--mirror", "--issues"}, "mirrors cannot copy issues, milestones, labels, pull requests, or releases"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--lfs-endpoint", "https://lfs.test"}, "--lfs-endpoint requires --lfs"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--auth-password", "secret"}, "--auth-password requires --auth-user"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--auth-token", "one", "--auth-token-file", "-"}, "--auth-token cannot be combined with --auth-token-file"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--auth-password-file", "-", "--auth-token-file", "-"}, "stdin can supply only one source credential"},
		{[]string{"repo", "migrate", "https://source.test/repo", "--name", "new", "--timeout", "0s"}, "--timeout must be positive"},
		{[]string{"repo", "migrate", "--server-path", "relative/path", "--name", "new"}, "--server-path must be an absolute path on the Gitea server"},
		{[]string{"repo", "migrate", "--server-path", "/server/repo", "--name", "new", "--auth-user", "account"}, "server path imports use --service git without remote credentials"},
		{[]string{"run", "download", "1", "--zip", "--name", "build", "--output", "-", "--clobber"}, "--clobber cannot be combined with --output -"},
		{[]string{"run", "download", "1", "--clobber", "--skip-existing"}, "--clobber cannot be combined with --skip-existing"},
		{[]string{"run", "watch", "1", "--expect-head", "main"}, "--expect-head requires a commit SHA, or a revision of the local clone"},
		{[]string{"run", "logs", "1", "--tail", "0"}, "--tail must be positive"},
		{[]string{"run", "logs", "1", "--interval", "1s"}, "--interval requires --follow"},
		{[]string{"run", "logs", "1", "--timeout", "1m"}, "--timeout requires --follow"},
		{[]string{"run", "logs", "1", "--tail", "5", "--output", "logs.zip"}, "--output and --dir download the whole run and cannot be combined with --job, --failed, --follow or --tail"},
		{[]string{"run", "list", "--since=-1h"}, "invalid --since: relative duration cannot be negative"},
		{[]string{"run", "list", "--until="}, "invalid --until: use a date, RFC3339 timestamp, or duration such as 24h"},
		{[]string{"run", "list", "--since=1h", "--until=2h"}, "--since must not be later than --until"},
		{[]string{"run", "list", "--limit", "0"}, `"-L, --limit" flag: must be 1..1000`},
		{[]string{"runner", "list", "--org", "acme", "--global"}, "--org cannot be combined with --global"},
		{[]string{"runner", "view", "3", "--account", "--repo", "alice/project"}, "--account cannot be combined with --repo"},
		{[]string{"runner", "list", "--disabled", "--enabled"}, "--disabled cannot be combined with --enabled"},
		{[]string{"runner", "delete", "abc"}, `invalid resource number "abc"`},
		{[]string{"runner", "disable", "0"}, `invalid resource number "0"`},
		{[]string{"search", "issue", "query", "--state", "invalid"}, "--state must be open, closed, or all"},
		{[]string{"search", "issue", "query", "--author", "alice", "--authored"}, "--author cannot be combined with --authored"},
		{[]string{"search", "issue", "--label", ","}, "label names cannot be empty"},
		{[]string{"search", "pr", "query", "--since", "invalid"}, "--since: use a date, RFC3339 timestamp, or duration such as 24h"},
		{[]string{"search", "pr", "query", "--since", "2026-01-02T00:00:00Z", "--until", "2026-01-01T00:00:00Z"}, "--since must not be later than --until"},
		{[]string{"search", "issue", "--since="}, "invalid --since: use a date"},
		{[]string{"search", "repo", "query", "--sort", "invalid"}, "--sort must be alpha, created, updated, size, or id"},
		{[]string{"search", "repo", "query", "--order", "invalid"}, "--order must be asc or desc"},
		{[]string{"search", "repo", "query", "--order", "desc"}, "--order requires --sort"},
		{[]string{"secret", "set", "BUILD_TOKEN", "--body", "text", "--body-file", "-"}, "--body cannot be combined with --body-file"},
		{[]string{"secret", "set", "BUILD_TOKEN", "--body= ", "--json=value"}, "unknown JSON field \"value\""},
		{[]string{"secret", "set", "BUILD_TOKEN", "--no-input"}, "secret set requires --body, --body-file or standard input"},
		{[]string{"secret", "set"}, "secret set requires a name argument or --env-file"},
		{[]string{"secret", "set", "CI", "--body", "text"}, "CI and names starting with GITEA_ or GITHUB_ are reserved"},
		{[]string{"secret", "set", "TOKEN", "--env-file", "secrets.env"}, "--env-file cannot be combined with a secret name"},
		{[]string{"secret", "delete", "", "-y"}, `invalid name ""`},
		{[]string{"secret", "delete", "bad/name", "-y"}, `invalid name "bad/name"`},
		{[]string{"status", "--limit=0"}, `"--limit" flag: must be 1..100`},
		{[]string{"status", "--json=token"}, "unknown JSON field \"token\""},
		{[]string{"team", "create", "org", "--name", "Developers", "--permission", "owner"}, "--permission must be read, write, or admin"},
		{[]string{"team", "create", "org", "--name", "Developers", "--unit", "repo.unknown=write"}, `unknown unit "repo.unknown"; use one of repo.code`},
		{[]string{"team", "create", "org", "--name", "Developers", "--unit", "repo.ext_wiki=write"}, "--unit repo.ext_wiki=write: access must be none, read"},
		{[]string{"team", "create", "org/Developers", "--name", "Developers"}, "supply the team name once"},
		{[]string{"team", "create", "", "x"}, "organization name cannot be empty"},
		{[]string{"team", "create", "/foo"}, "organization name cannot be empty"},
		{[]string{"team", "member", "add", "acme/core", "alice", " "}, "username cannot be empty"},
		{[]string{"team", "create", "org", "--name", "Developers", "--unit", "repo.code=write", "--permission", "admin"}, "--unit cannot restrict administrator access"},
		{[]string{"team", "edit", "1"}, "supply team fields to edit"},
		{[]string{"team", "edit", "1", "--unit="}, "must be formatted as key=value"},
		{[]string{"team", "member", "add", "invalid", "alice"}, `invalid team "invalid": use ORG/TEAM, such as acme/developers, or a team ID`},
		{[]string{"team", "view", "acme/"}, `invalid team "acme/"`},
		{[]string{"team", "repo", "add", "2", "service", "-R", "acme/service"}, "--repo is not used by team repo add"},
		{[]string{"time", "create", "1", "0s"}, `invalid duration "0s": time spent must be positive`},
		{[]string{"time", "create", "1", "1ms"}, `invalid duration "1ms": use whole seconds`},
		{[]string{"time", "list", "--issue", "0"}, `invalid issue "0"`},
		{[]string{"time", "list", "--mine", "--issue", "1"}, "--mine cannot be combined with --issue"},
		{[]string{"time", "list", "--since", "2026-10-02", "--until", "2026-10-01"}, "--since must not be later than --until"},
		{[]string{"time", "list", "--since", "1h", "--until", "2h"}, "--since must not be later than --until"},
		{[]string{"time", "list", "--total", "--json"}, "--total cannot be combined with --json, --jq or --csv"},
		{[]string{"time", "list", "--mine", "-R", "alice/project"}, "--mine cannot be combined with --repo"},
		{[]string{"time", "list", "--mine"}, "--mine cannot be combined with GITEA_CLI_REPO"},
		{[]string{"time", "list", "--mine", "--remote", "origin"}, "--mine cannot be combined with --remote"},
		{[]string{"variable", "set", "BUILD_MODE", "--body="}, "the value of BUILD_MODE is empty"},
		{[]string{"secret", "set", "DEPLOY_KEY", "--body=  "}, "the value of DEPLOY_KEY contains only whitespace"},
		{[]string{"secret", "set", "DEPLOY_KEY", "--body", "x", "--json=created_at"}, "unknown JSON field \"created_at\""},
		{[]string{"secret", "list", "--account"}, "secret list cannot be combined with --account: Gitea cannot list account secrets"},
		{[]string{"secret", "set", "DEPLOY_KEY", "--body=" + strings.Repeat("é", 32769)}, "the value of DEPLOY_KEY exceeds 65536 bytes"},
		{[]string{"variable", "set", "GITEA_RESERVED", "--body", "text"}, "CI and names starting with GITEA_ or GITHUB_ are reserved"},
		{[]string{"variable", "view", "", "--org", "o"}, `invalid name ""`},
		{[]string{"variable", "get", ""}, `invalid name ""`},
		{[]string{"variable", "delete", "", "-y"}, `invalid name ""`},
		{[]string{"variable", "list", "--org", "team", "--account"}, "--org cannot be combined with --account"},
		{[]string{"variable", "list", "--org", "team", "--repo", "alice/project"}, "--org cannot be combined with --repo"},
		{[]string{"version", "--json"}, "version prints text"},
		{[]string{"version", "extra"}, `unexpected argument "extra"`},
		{[]string{"webhook", "edit", "1", "--secret", "private", "--clear-authorization"}, "webhook secret editing is unsupported"},
		{[]string{"webhook", "create", "ftp://host/path"}, "webhook URL must be an absolute HTTP(S) URL without user information or fragment"},
		{[]string{"webhook", "create", "https://user:password@host/path"}, "webhook URL must be an absolute HTTP(S) URL without user information or fragment"},
		{[]string{"webhook", "create", "https://host/path#fragment"}, "webhook URL must be an absolute HTTP(S) URL without user information or fragment"},
		{[]string{"webhook", "create", "https://host/path", "--url", "https://other/path"}, "a URL argument cannot be combined with --url"},
		{[]string{"webhook", "create", " ", "--url", "http://x"}, "URL cannot be empty"},
		{[]string{"webhook", "create", "https://host/path", "--secret", "one", "--secret-file", "-"}, "--secret cannot be combined with --secret-file"},
		{[]string{"webhook", "create", "https://host/path", "--secret-file", "-", "--authorization-header-file", "-"}, "stdin can supply only one webhook credential"},
		{[]string{"webhook", "create", "https://host/path", "--authorization-header", "one", "--clear-authorization"}, "--authorization-header, --authorization-header-file and --clear-authorization cannot be combined"},
		{[]string{"webhook", "create", "https://host/path", "--authorization-header", "first\nsecond"}, "authorization header must be a single line"},
		{[]string{"webhook", "create", "https://host/path", "--events", ""}, "--events requires at least one trigger"},
		{[]string{"webhook", "create", "https://host/path", "--config", "secret=private"}, "--config cannot override the URL, secret, or webhook scope"},
		{[]string{"webhook", "edit", "1"}, "nothing to edit; pass a flag such as --events or --active (see --help)"},
		{[]string{"webhook", "edit", "1", "--active=false"}, "editing a webhook drops its stored authorization header unless it is sent again, and Gitea never returns it: pass --authorization-header-file or --authorization-header (use --clear-authorization only for a hook without one)"},
		{[]string{"webhook", "delete", "not-an-id"}, "invalid resource number \"not-an-id\""},
		{[]string{"webhook", "list", "--org", "organization", "--account"}, "--org cannot be combined with --account"},
		{[]string{"webhook", "list", "--global", "-R", "owner/repo"}, "--global cannot be combined with --repo"},
		{[]string{"wiki", "view", ""}, "page name cannot be empty"},
		{[]string{"wiki", "edit", "", "--body", "x"}, "page name cannot be empty"},
		{[]string{"wiki", "delete", "", "-y"}, "page name cannot be empty"},
		{[]string{"wiki", "create", "--body-file", "-"}, "--body-file - requires a title"},
		{[]string{"wiki", "create", "--title", "Home", "--body", "text", "--json=created_at"}, "unknown JSON field \"created_at\""},
		{[]string{"wiki", "edit", "Home", "--body="}, "wiki content cannot be empty"},
		{[]string{"wiki", "revisions", "Home", "--page", "0"}, `"--page" flag: must be a positive integer`},
		{[]string{"workflow", "run", "ci.yml", "-r", "main", "-f", "a=1", "-f", "a=2"}, "input a is given more than once"},
		{[]string{"workflow", "run", "ci.yml", "--interval", "1s"}, "--interval requires --follow"},
		{[]string{"workflow", "run", "ci.yml", "--follow", "--json"}, "--follow cannot be combined with --json, --jq or --csv"},
		{[]string{"workflow", "view", ""}, "workflow cannot be empty"},
		{[]string{"workflow", "view", "ci", "--ref", "main"}, "--ref requires --yaml"},
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
		{[]string{"release", "create"}, map[string]string{"Tag (required):": "v1.0.0"}, "Create release v1.0.0 in alice/project?"},
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
		{[]string{"webhook", "create"}, map[string]string{"Payload URL (required):": "https://hooks.test/path"}, "", "POST /api/v1/repos/alice/project/hooks"},
		{[]string{"team", "create", "acme"}, map[string]string{"Team name (required):": "Developers"}, "", "POST /api/v1/orgs/acme/teams"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var writes []string
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"variable not found"}`))
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
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/1/reviews":
					_, _ = w.Write([]byte(`[]`))
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
