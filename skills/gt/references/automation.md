# Scripts and CI

## Non-interactive defaults

- `--no-input` disables prompts and the implicit editor and browser. Without a terminal gt
  already does not prompt, but `--no-input` makes the intent explicit and fails fast. An
  explicit `--editor` or `--web` still opens the editor or browser, so do not pass them.
- A confirmation that cannot be asked fails with exit code 2 and
  `confirmation required…; rerun with --yes`. Add `--yes` only for actions that were approved.
- `--json` and `--jq` give stable output; text output may change between releases.
- Branch on exit codes ([SKILL.md](../SKILL.md#exit-codes)), not on messages.

## Credentials without a login

```sh
export GITEA_CLI_HOST=https://gitea.example.org   # or GITEA_INSTANCE_URL
export GITEA_TOKEN=...                            # from the CI secret store
export GITEA_CLI_REPO=OWNER/REPO                  # optional, like --repo
gt auth status --no-input --verify --json host,user,verified,error
```

- Environment credentials are never written to `config.yml` or `hosts.yml`. Never put a token
  in a file, a command line that gets logged, or a comment body.
- `GITEA_INSTANCE_INSECURE=true` skips TLS verification for that token's instance; use it
  only for test instances the user names.
- `gt help environment` lists every variable and the order in which gt picks the instance
  and repository.

## In a Gitea Actions workflow

Every job gets a token as `${{ secrets.GITEA_TOKEN }}`; its permissions follow the
workflow's `permissions:` and the repository settings, and pull requests from forks get
read-only access, so the job below skips them.

```yaml
jobs:
  report:
    if: gitea.event.pull_request.head.repo.full_name == gitea.repository
    runs-on: ubuntu-latest
    permissions:
      pull-requests: write
    env:
      GITEA_TOKEN: ${{ secrets.GITEA_TOKEN }}
      GITEA_CLI_HOST: ${{ gitea.server_url }}
      GITEA_CLI_REPO: ${{ gitea.repository }}
    steps:
      - run: gt pr comment ${{ gitea.event.pull_request.number }} --no-input --body "Build passed."
```

Install gt in the job first (see the README's installation section); the runner image does
not include it.

## Dispatching workflows

```sh
gt workflow list --no-input --json id,name,path,state
gt workflow run ci.yml --ref main -f target=staging -f dry_run=true --no-input --json id,url
gt workflow run nightly --follow --no-input       # dispatch, then follow the job logs
gt run watch 34 --timeout 30m --no-input
```

The workflow needs a `workflow_dispatch` trigger. `--ref` defaults to the current or the
default branch; inputs are `-f key=value`, repeated.

## Aliases

```sh
gt alias set failing 'run list --status failure -L 10'
gt alias list --no-input
```

Aliases are the user's configuration; create one only when asked.

## Patterns

```sh
# Wait for CI, then merge. --yes makes the merge final: run this only on the user's explicit request
gt pr checks 18 --watch --no-input && gt pr merge 18 --squash --yes --no-input

# Open pull requests waiting for review, one per line
gt pr list --no-input --json number,title,review_decision --jq '.[] | select(.review_decision == "review_required") | "\(.number)\t\(.title)"'

# Fail a script when the last run on main failed
gt run list --branch main -L 1 --no-input --json conclusion --jq '.[0].conclusion' | grep -qx success
```
