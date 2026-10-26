# gt api: the fallback

Use a typed command when one exists; it validates input and maps fields. Reach for `gt api`
when no command covers the request. It authenticates like every other command and sends
requests to `/api/v1` of the selected instance.

```sh
gt api user --jq .login
gt api 'repos/{owner}/{repo}' --jq .default_branch
gt api 'repos/{owner}/{repo}/issues' -f title="Crash on start" -F labels='[3]'
gt api -X PATCH 'repos/{owner}/{repo}/issues/17' -f state=closed
gt api 'repos/{owner}/{repo}/issues/17/reactions' -f content=+1
```

- `{owner}` and `{repo}` come from the current repository or `-R`, `{branch}` from the
  checked-out branch. Write them literally when the target must not depend on the directory.
- GET is the default, POST once fields or `--input` supply a body; `-X` picks another method.
- `-f key=value` sends a string, `-F key=value` a typed value (`true`, numbers, JSON arrays and
  objects), and `-F key=@file` or `@-` reads a file or stdin. For GET, fields become query
  parameters.
- `--input body.json` (or `-`) sends a prepared JSON body.
- `--paginate` follows the pages of a list and prints each page on its own line; add `--jq`
  to flatten (`--jq '.[]'`). `--max-pages` limits it.
- A response outside 2xx prints its body and fails; read the message before retrying.
- `--include` prints the status and headers to stderr, `--silent` drops the body.

## Useful endpoints without a dedicated command

Gitea's API reference is at `/api/swagger` on the instance.

```sh
# A file of the default branch, or of a ref
gt api 'repos/{owner}/{repo}/raw/README.md'
gt api 'repos/{owner}/{repo}/contents/go.mod?ref=main' --jq .content
# Files changed by a pull request, with additions and deletions
gt api 'repos/{owner}/{repo}/pulls/18/files' --paginate --jq '.[] | "\(.filename)\t\(.additions)\t\(.deletions)"'
# Combined commit status of a branch, including external CI
gt api 'repos/{owner}/{repo}/commits/{branch}/status' --jq '.state'
# Logs of one Actions job by ID
gt api 'repos/{owner}/{repo}/actions/jobs/71/logs'
```

`contents` returns the file base64-encoded in `content`; `raw` returns it as is.

## Safety

- Writes through `gt api` skip gt's confirmations and checks. Show the user the request and
  get approval before any POST, PATCH, PUT or DELETE.
- `--sudo USER` acts as another user and needs an administrator token; do not use it unless
  the user asks.
