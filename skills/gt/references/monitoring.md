# CI: checks, runs and logs

Gitea reports CI on a pull request as commit statuses ("checks") of its head commit. Gitea
Actions runs post one check per job, linked to the job.

## Is CI green?

```sh
gt pr checks 12 --no-input
echo $?   # 0 passed, 1 a check failed, 8 checks still pending
gt pr checks 12 --no-input --json name,state,description,url
```

- Without a number, `gt pr checks` takes the pull request of the current branch.
- `state` is `success`, `skipped` or `pending`, and anything else is a failure, such as
  `failure` or `error`.
- Checks belong to the head commit. After a push, re-read them; old results do not carry over.
- `gt pr view 12 --no-input --json head_sha,mergeable,review_decision` tells whether the pull
  request is mergeable once checks pass.
- `gt status --no-input --json failing_runs` lists failing runs of the current repository
  alongside your pull requests and assigned issues.

## Waiting

```sh
gt pr checks 12 --watch --no-input              # stops at the first failure
gt pr checks 12 --watch --timeout 20m --no-input
gt run watch 34 --timeout 10m --no-input        # fails when the run does not succeed
```

Both poll through an outage of the instance of up to 5 minutes. Prefer waiting over a loop
of repeated calls. A run whose jobs never start is waiting for a runner, or, with status
`waiting`, for approval in the web interface (a first-time contributor's fork); report that to
the user instead of waiting longer.

## Finding the run

```sh
gt run list --branch feature/login -L 5 --no-input --json id,workflow,status,conclusion,commit
gt run list --commit "$(git rev-parse HEAD)" --no-input --json id,workflow,conclusion
gt run list --workflow ci.yml --status failure --since 24h --no-input --json id,title,branch
gt run view 34 --no-input --json status,conclusion,jobs --jq '.jobs[] | {id, name, status, conclusion}'
gt run jobs 34 --no-input --json id,name,conclusion,steps
```

- `--status` takes a status or a conclusion: `queued`, `in_progress`, `waiting`, `completed`,
  `success`, `failure`, `cancelled`, `skipped` and others.
- `--commit` takes a full SHA or a revision of the local clone.
- A run is given by its ID (`34`, `#34`) or its URL; a job URL selects one job.

## Reading logs

```sh
gt run logs 34 --failed --no-input                     # every failed or canceled job
gt run logs 34 --job build --no-input                  # one job, by name or ID
gt run logs https://gitea.example.com/OWNER/REPO/actions/runs/34/jobs/71 --no-input
gt run logs 34 --job build --follow --no-input         # stream until the job completes
gt run logs 34 --output logs.zip --no-input            # whole run as a ZIP archive
```

- The log goes to stdout and each job's name to stderr, so `2>/dev/null` leaves pure log.
- Logs can be long. Search them (`| grep -n -i -E 'error|fail|panic'`) or read the end
  (`| tail -n 200`) before loading everything.
- `gt run view 34 --log --no-input` prints the logs of every job instead of the run summary.
- Steps are in `gt run jobs 34 --json steps`: a failed step's name narrows where to look.

From a failing check straight to its log:

```sh
gt pr checks 12 --no-input --json state,url --jq '.[] | select(.state != "success" and .state != "skipped" and .state != "pending") | .url'
gt run logs https://gitea.example.com/OWNER/REPO/actions/runs/34/jobs/71 --no-input
```

A check whose `url` points elsewhere comes from an external CI; gt cannot read its logs.

## Fixing and retrying

- A real failure: reproduce locally when possible, fix, commit and push, then wait on the new
  checks with `gt pr checks --watch`.
- A flaky failure: `gt run rerun 34 --failed --no-input` reruns the failed or canceled jobs,
  or one job with `--job build`. Rerun only completed runs; cancel a stuck one first with
  `gt run cancel 34 --no-input`.
- Ask the user before rerunning repeatedly or cancelling someone else's run.

## Artifacts

```sh
gt run artifact list 34 --no-input --json name,size_in_bytes,expired
gt run download 34 --name build --dir dist --no-input
gt run download 34 --zip --no-input
```
