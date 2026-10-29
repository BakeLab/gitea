---
name: gt
description: >-
  Drives Gitea (gitea.com or self-hosted) through gt, the official Gitea CLI: checks whether a
  pull request's CI passed and reads the logs of the jobs that failed, reruns Actions jobs,
  reads an issue and opens a pull request for it, comments on issues and pull requests,
  reviews with inline comments, merges, searches issues and pull requests, manages releases
  and wiki pages, dispatches workflows, scripts gt in CI with GITEA_TOKEN, and makes raw Gitea
  API calls with gt api. Use for any Gitea task, or when the user mentions Gitea Actions,
  .gitea/workflows, or the older tea CLI.
license: MIT
---

# gt: the Gitea CLI for agents

`gt` drives a Gitea instance from the shell. Its commands follow `gh`, but flags,
JSON fields and defaults differ: check a command's `--help` (it lists `JSON FIELDS`)
instead of assuming `gh` behaviour.

## Contents

- [Rules for agents](#rules-for-agents)
- [Quick start](#quick-start): CI failures, issue to pull request, comments and reviews
- [Task reference](#task-reference)
- [Authentication and targeting](#authentication-and-targeting)
- [Exit codes](#exit-codes)
- [Validation checklist](#validation-checklist)
- [When not to use](#when-not-to-use)

## Rules for agents

- Pass `--no-input` on every command so gt never waits for a prompt or opens an editor on its
  own, and never pass `--editor` or `--web`, which open one even then.
- Read with `--json` and filter with `--jq`; the text output is for people and may change.
  `--jq` runs inside gt, so `jq` need not be installed, and strings print without quotes.
- An unknown `--json` field fails and names the available ones; `gt help formatting`
  explains fields that cost extra requests.
- Without a terminal, confirmations fail with exit code 2. Pass `--yes` only after the user
  agreed to that action: deletes, merges, branch cleanup, pushing a new branch from
  `gt pr create`. Closing an issue or pull request asks no confirmation, so ask the user first.
- Pass multi-line bodies on stdin with `-F -` (`--body-file -`) and a quoted heredoc rather
  than `\n` inside `--body`.
- Lists stop at `-L 30` by default; raise `-L` (up to 1000) or page with `--page` before
  concluding something is missing.
- Pull request commands without a number act on the current branch's pull request.
- Unlike `gh`, the `comments` JSON field of issues and pull requests is a count. Read the
  comment text with `gt issue comment list` or `gt pr comment list`.

## Quick start

### Did CI pass, and why did it fail?

```sh
gt pr checks 12 --no-input                     # exit 0 passed, 1 failed, 8 still pending
gt pr checks 12 --no-input --json name,state,url --jq '.[] | select(.state != "success" and .state != "skipped" and .state != "pending")'
gt run logs https://gitea.example.com/OWNER/REPO/actions/runs/34/jobs/71 --no-input
```

An Actions check's `url` is its job URL, and `gt run logs` accepts that URL to print just
that job. From a run ID instead, print the logs of every failed or canceled job:

```sh
gt run list --branch feature/login --status failure -L 5 --no-input --json id,workflow,title,conclusion
gt run view 34 --no-input --json jobs --jq '.jobs[] | {name, conclusion}'
gt run logs 34 --failed --no-input
```

Wait for checks instead of polling: `gt pr checks 12 --watch --no-input`, or
`gt run watch 34 --no-input`. To retry a flaky job without a new commit,
`gt run rerun 34 --failed --no-input`.
More in [monitoring.md](references/monitoring.md).

### From an issue to a pull request

```sh
gt issue view 17 --no-input --json number,title,body,labels,state
gt issue comment list 17 -L 100 --no-input --json author,body,created_at
git switch -c fix/issue-17
# edit, test and commit
gt pr create --no-input --yes --title "fix: describe the change" --body-file - <<'EOF'
Explain what changed and why.

Closes #17
EOF
gt pr checks --watch --no-input
```

`gt pr create` pushes a branch that is not on the remote yet, after a confirmation that
`--yes` gives; ask the user before pushing. Details, forks and AGit in
[resources.md](references/resources.md).

### Comment and review

```sh
gt issue comment 17 --no-input --body-file - <<'EOF'
Reproduced on main; the fix is in #18.
EOF
gt pr comment 18 --edit-last --no-input --body "Updated after review."
gt pr review 18 --approve --no-input --body "Looks good."
gt pr review 18 --request-changes --no-input --comments-file inline.json --body "See the inline comments."
```

`inline.json` is an array of `{"path", "body", "new_position"}` (or `old_position`) on a line
of the diff; gt refuses lines outside the diff. See [resources.md](references/resources.md).

## Task reference

| Task | Command | Reference |
| --- | --- | --- |
| Checks of a pull request | `gt pr checks 12` | [monitoring.md](references/monitoring.md) |
| Wait for checks | `gt pr checks 12 --watch` | [monitoring.md](references/monitoring.md) |
| Logs of failed or canceled jobs | `gt run logs 34 --failed` | [monitoring.md](references/monitoring.md) |
| Follow a running job | `gt run logs 34 --job build --follow` | [monitoring.md](references/monitoring.md) |
| Rerun failed or canceled jobs | `gt run rerun 34 --failed` | [monitoring.md](references/monitoring.md) |
| Download artifacts | `gt run download 34 --name build` | [monitoring.md](references/monitoring.md) |
| Read an issue with comments | `gt issue view 17 --comments` | [resources.md](references/resources.md) |
| Open an issue | `gt issue create --title T --body-file -` | [resources.md](references/resources.md) |
| Close or reopen an issue | `gt issue close 17 --comment C` | [resources.md](references/resources.md) |
| Open a pull request | `gt pr create --title T --body-file -` | [resources.md](references/resources.md) |
| Read a pull request's diff | `gt pr diff 18` | [resources.md](references/resources.md) |
| Comment | `gt pr comment 18 --body-file -` | [resources.md](references/resources.md) |
| Review with inline comments | `gt pr review 18 --comment --comments-file inline.json` | [resources.md](references/resources.md) |
| Reply to an inline comment | `gt pr review reply 18 1234 --body-file -` | [resources.md](references/resources.md) |
| Merge | `gt pr merge 18 --squash --delete-branch` | [resources.md](references/resources.md) |
| Edit or delete a wiki page | `gt wiki edit Home --body-file -` | [resources.md](references/resources.md) |
| View or change a repository | `gt repo view --json full_name` | [resources.md](references/resources.md) |
| Find issues or pull requests | `gt search issue crash --owner OWNER` | [search.md](references/search.md) |
| My open work | `gt status` | [search.md](references/search.md) |
| Run gt in CI or a script | `GITEA_TOKEN` and `GITEA_CLI_HOST` | [automation.md](references/automation.md) |
| Dispatch a workflow | `gt workflow run ci.yml --ref main` | [automation.md](references/automation.md) |
| Anything without a command | `gt api 'repos/{owner}/{repo}/...'` | [api.md](references/api.md) |
| Write a Gitea Actions workflow | `.gitea/workflows/*.yaml` | [workflow-authoring.md](references/workflow-authoring.md) |

## Authentication and targeting

- `gt auth status --no-input --json` reports each account; read `credential_available` and
  `verified` (add `--verify` to check against the server). With `--json` it exits 0 even when
  something is wrong; without it, missing or rejected credentials exit 4.
- In CI, set `GITEA_TOKEN` with `GITEA_CLI_HOST` (or `GITEA_INSTANCE_URL`); environment
  credentials are never saved. Logging in interactively (`gt auth login`) is for the user to
  run, not the agent.
- gt infers the repository from Git remotes and prefers `upstream` over `origin`, so a fork
  checkout targets the parent repository. Pass `-R OWNER/REPO` when that is not what you want.
- `gt help environment` lists the global flags and the selection order of repository and
  instance.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success, including an empty list |
| 1 | The command failed, `gt pr checks` found a failed check, or the instance did not respond within `http_timeout` |
| 2 | Usage error, or a confirmation that needs `--yes` |
| 3 | Canceled: a declined confirmation or prompt, or a wait's `--timeout` expired |
| 4 | Authentication missing or rejected |
| 5 | `gt run watch` and `gt workflow run --follow`: the run completed without success |
| 6 | `gt run watch --expect-head`: the run is for another commit |
| 8 | `gt pr checks`: checks still pending |
| 130 | Interrupted with Ctrl-C |

`gt help exit-codes` has the full list. Branch on the code instead of parsing messages.

## Validation checklist

1. `gt --version` works and `gt auth status --no-input --json` shows a credential.
2. `gt repo view --no-input --json full_name` names the intended repository.
3. Reads use `--json`; every write was approved by the user.
4. After `gt pr create`, `gt pr view --no-input --json url` gives the link to report.

## When not to use

- Local Git work (commits, rebases, branches) needs `git`, not gt.
- GitHub, GitLab or other forges: gt only talks to Gitea instances.
- Administering the Gitea server itself (`app.ini`, runners) is outside gt, except the API
  that `gt admin` and `gt api` expose.
