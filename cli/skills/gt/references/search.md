# Finding issues, pull requests and your work

## Search or list?

- `gt issue list` and `gt pr list` read one repository and filter by state, labels, people and
  milestone. `-S` searches titles and bodies in that repository.
- `gt search issue`, `gt search pr` and `gt search repo` search the whole selected instance,
  or one owner with `--owner`, or one repository with `-R`.
- gt has no code search; to find code, clone or read files with `gt api` ([api.md](api.md)).

```sh
gt issue list --label bug --milestone v1.0 --state all -L 100 --no-input --json number,title,state
gt issue list -S "panic on start" --no-input --json number,title,url
gt pr list --author @me --state open --no-input --json number,title,head,review_decision
gt pr list --head feature/login --no-input --json number,url
gt search issue "login error" --owner OWNER --no-input --json repository,number,title,url
gt search pr --review-requested --no-input --json repository,number,title,url
gt search repo cli --owner gitea --no-input --json full_name,description
```

- The query is a plain search string. Filter with flags (`--label`, `--author`, `--state`)
  rather than GitHub-style qualifiers such as `is:open`; Gitea's search API takes these
  filters as separate parameters.
- `@me` stands for the signed-in user in `--author`, `--assignee` and, on issues only,
  `gt issue list --mentions @me`. `gt search issue` and `gt search pr` have switches
  instead: `gt search issue --assigned --mentioned --no-input`.
- Results stop at `-L` (default 30). An empty result exits 0; check the count, not the exit
  code, before saying nothing exists.
- `--since` and `--until` take a date, an RFC3339 timestamp or a duration ago (`24h`).

## Your work

```sh
gt status --no-input --json pull_requests,assigned_issues,mentions,failing_runs
gt pr status --no-input --json current_branch,authored,review_requested
gt notification list --no-input --json id,repository,type,number,title,unread
gt notification list --type pull --this-repo --no-input --json id,number,title
```

- `gt status` gathers your open pull requests, assigned issues, mentions and the failing
  Actions runs of the current repository.
- Notification IDs are thread IDs, not issue numbers; `number` and `repository` name the issue
  or pull request. `gt notification view` marks a notification as read, so prefer
  `gt notification list` for reading.
