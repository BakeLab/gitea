# Gitea Actions workflows

Select a repository explicitly for scripts and supply a token scoped to its instance:

```sh
export TEA_HOST=https://gitea.example.org
export TEA_REPO=owner/repository
# Supply GITEA_TOKEN through your CI secret environment.
tea workflow list --no-input --json
tea workflow view deploy.yml --no-input --json
tea workflow run deploy.yml --ref main --field env=staging --field version=1.2.3 --no-input
tea workflow disable deploy.yml --yes --no-input
tea workflow enable deploy.yml --no-input
```

Workflow dispatch requires a `workflow_dispatch` trigger in the workflow. Dispatch
and watching are separate commands; find the run with `tea run list`, then use
`tea run watch RUN_ID --timeout 10m --no-input`. Unsuccessful completed runs return
a nonzero exit code.

For an explicitly authorized merge in CI:

```sh
tea pr merge 123 --repo owner/repository --method rebase --no-input
```

Keep credentials in the secret environment or credential store. Never write a token
into `config.yml` or `hosts.yml`. Run `tea help <command>`, or see the command
reference in each release's docs archive, for supported flags and the
[README](../README.md) for fresh authentication.
