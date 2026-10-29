# Writing Gitea Actions workflows

Gitea Actions aims to be compatible with GitHub Actions, so most workflow syntax carries over.
This page lists what differs. Sources: Gitea's
[quick start](https://docs.gitea.com/usage/actions/quickstart),
[comparison with GitHub Actions](https://docs.gitea.com/usage/actions/comparison) and
[job token permissions](https://docs.gitea.com/usage/actions/token-permissions/); check them
when a detail matters, as they change with Gitea releases.

## Before the first workflow

- Workflows are `.yaml` files in `.gitea/workflows/`.
- Actions must be enabled on the instance. New repositories then have it on unless the
  administrator changed the default repository units; otherwise enable it in the repository
  settings ("Enable Repository Actions"). `gt repo view --no-input --json actions_enabled` tells.
- Jobs run on a registered Gitea Runner; `runs-on` labels must match the labels of a runner
  of the instance. A run that waits forever usually lacks a matching runner
  ([monitoring.md](monitoring.md#waiting)).

## Differences from GitHub Actions

- Gitea's examples use the `gitea.*` context (`gitea.repository`, `gitea.ref`,
  `gitea.server_url`, …) where GitHub uses `github.*`.
- `uses:` accepts an absolute URL to any Git host, such as
  `uses: https://gitea.com/actions/checkout@v4`. Without a host, actions come from
  `https://github.com`, or from the instance itself when the administrator set
  `[actions].DEFAULT_ACTIONS_URL` to `self`.
- `uses: self:owner/repo@v1` refers to an action on the same instance, and `$/path` to an
  action next to the workflow without a checkout.
- `schedule` also accepts `@yearly`, `@monthly`, `@weekly`, `@daily` and `@hourly`.
- Ignored: `jobs.<job_id>.environment`, problem matchers and error annotations.
- The job token is `${{ secrets.GITEA_TOKEN }}`. `permissions:` takes `contents`, `code`,
  `releases`, `issues`, `pull-requests`, `actions`, `wiki`, `projects` and `packages`, with
  `read`, `write` or `none`; GitHub-only scopes such as `statuses`, `checks` and `id-token`
  are not supported. Pull requests from forks always get read-only contents.
- `GITEA_TOKEN` cannot publish to the package registry of its repository, such as pushing
  OCI images; use a personal access token in a secret for that.

## Trying a workflow

```sh
gt workflow list --no-input --json name,path,state
gt workflow view ci.yml --no-input --json name,path,state
gt workflow run ci.yml --ref feature/ci --follow --no-input   # needs a workflow_dispatch trigger
gt run list --workflow ci.yml -L 5 --no-input --json id,status,conclusion,branch
```

Push the workflow on a branch and open a pull request to see it run on the pull request event;
then read failures as in [monitoring.md](monitoring.md).
