# Gitea Actions workflows

Select a repository explicitly for scripts and supply a token scoped to its instance:

```sh
export GITEA_CLI_HOST=https://gitea.example.org
export GITEA_CLI_REPO=owner/repository
# Supply GITEA_TOKEN through your CI secret environment.
gt workflow list --no-input --json
gt workflow view deploy.yml --no-input --json
gt workflow run deploy.yml --ref main --field env=staging --field version=1.2.3 --no-input
gt workflow disable deploy.yml --yes --no-input
gt workflow enable deploy.yml --no-input
```

Workflow dispatch requires a `workflow_dispatch` trigger in the workflow. Dispatch
and watching are separate commands; find the run with `gt run list`, then use
`gt run watch RUN_ID --timeout 10m --no-input`. Unsuccessful completed runs return
exit code 5, and a failure of gt itself, such as a rejected request, exits with 1.

For an explicitly authorized merge in CI:

```sh
gt pr merge 123 --repo owner/repository --rebase --yes --no-input
```

Keep credentials in the secret environment or credential store. Never write a token
into `config.yml` or `hosts.yml`. Run `gt help <command>`, or see the command
reference in each release's docs archive, for supported flags and the
[README](../README.md) for fresh authentication.
