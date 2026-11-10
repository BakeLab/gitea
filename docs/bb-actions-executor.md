# Breadboard Actions executor

Set `[actions] BB_EXECUTOR_CONFIG = /etc/gitea/bb-actions.json` to let the Gitea
server create durable Breadboard command tasks for matching waiting jobs. Each
task prepares a VM, starts one official ephemeral Gitea Runner and releases its
sandbox when the runner exits. Gitea continues to receive job outputs, step
statuses and logs through its existing runner API.

The runner reaches Gitea through BB's existing exact internal-service route.
Every eligible BB node must declare that route and be able to reach its upstream.
Sandbox placement requires the declaration; a missing route keeps the sandbox
queued. No SSH forwarding or runner NetBird identity is used. The route declaration
does not establish upstream connectivity, so verify it before enabling profiles.

Example configuration using the previously authorized human profile (replace
the project, token record path and SHA-256):

```json
{
  "endpoint": "https://bmetal.int.bb.pub:18092",
  "project": "prj_example",
  "profile_auth": {
    "issuer": "https://id.bb.pub",
    "client_id": "breadboard-cli",
    "resource": "https://breadboard.int.bb.pub/api",
    "scopes": ["openid", "profile", "groups", "offline_access", "breadboard:read", "breadboard:operate"],
    "token_file": "/etc/gitea/bb-profile/token.json"
  },
  "max_concurrent": 4,
  "auto_concurrency": true,
  "max_duration_s": 25200,
  "profiles": [{
    "name": "linux-amd64",
    "labels": ["self-hosted:host", "linux:host", "x64:host", "cpu:host", "on-demand:host"],
    "runner_url": "https://dl.gitea.com/gitea-runner/5.2.0/gitea-runner-5.2.0-linux-amd64",
    "runner_sha256": "REPLACE_WITH_OFFICIAL_64_CHARACTER_SHA256",
    "package_sets": ["git-runner-docker"],
    "sandbox": {
      "runtime": "sandbox-vm",
      "network": "gateway",
      "fabric": false,
      "cpu": {"arch": "amd64", "vcpus": 4, "memory_mb": 32768},
      "disk_mb": 102400,
      "allowed_hosts": ["*", "blackthorn-rtx5080.int.bb.pub"],
      "required_internal_services": ["blackthorn-rtx5080.int.bb.pub:28300"]
    }
  }]
}
```

`max_concurrent` is always the hard upper limit across all BB-managed jobs.
With `auto_concurrency` omitted or false, dispatch retains the fixed limit and
does not query BB capacity. When enabled, Gitea asks
`POST /v1/scheduler/capacity` for each profile's exact `sandbox` spec and uses
the returned `schedulable` count as that profile's additional admission limit.
The endpoint must return `schedulable`, `max_count` and `as_of_ms`; project
scope comes from the configured `X-Breadboard-Project` header. Capacity checks
are read-only and do not reserve resources. If any profile query fails or
returns invalid data, the dispatcher starts no new BB tasks during that
reconciliation; already active tasks continue to be reconciled normally.

`profile_auth.token_file` uses the BB CLI token-record format (`access_token`,
`refresh_token`, `expires_at` as a Unix timestamp). Provision the already
authorized profile's token record to this path without printing it; create the
parent directory as mode `0700` and the token file as mode `0600`, owned by the
Gitea service account. Gitea refreshes access tokens and atomically saves token
rotation. Do not configure the same rotating refresh token in another
independent store. Profile authentication uses the configured issuer and
resource and does not require a client secret. Alternatively, the OAuth
client-credentials mode remains available for a dedicated machine identity;
the legacy `token_file` mode continues to reread a raw bearer token for each
request.

For native macOS jobs, set `platform` to `darwin`, use an official Darwin runner
binary, and pin `sandbox.node_name` to the macOS node (for example,
`apricot-mac-mini-1.int.bb.pub`). Use only `:host` labels such as
`macos-latest:host`, `self-hosted:host`, `macos:host` and `arm64:host`.
Leave `package_sets` empty: macOS guests use their image tools rather than Linux
package sets. The execution user defaults to `breadboard` on macOS and `root`
on Linux; `exec_user` can select the configured guest account. The macOS task
uses a private temporary work directory and the native `shasum` checksum tool.
The guest image needs Git, curl, Node action support and Xcode command line tools;
individual workflows may install Go, Rust or Homebrew LLVM themselves.
The macOS node must declare the exact Gitea internal-service route required by
the profile; the guest uses BB gateway networking without its own NetBird identity.

Profiles are considered in order. All job `runs-on` labels must match the label
names before `:host` or `:docker://IMAGE`. Give ARM64 profiles an ARM64 sandbox
and an ARM64 runner binary. GPU profiles must explicitly request the appropriate
BB accelerator and package sets. Do not alias unavailable hardware to another
GPU class. Task requests use the BB sandbox API shape, so other immutable
placement constraints are available inside `sandbox`.

The dispatcher records the job, runner identity and immutable task request before
submitting work. The runner identity is encrypted with Gitea's `SECRET_KEY` and
sent through a per-task BB secret; no credential is embedded in the durable task
command. Secrets are refreshed while tasks are active, restoring them after a BB
restart. BB can fail preparation if a secret is unavailable when its command
starts; that failure is reported to the Gitea job rather than leaving it queued.

## Explicitly unsupported runner labels

Jobs whose `runs-on` labels do not match any BB profile normally remain queued;
a missing profile can be a configuration mistake or temporary capacity issue.
To stop known unsupported hardware jobs from accumulating, an administrator may
add exact label sets to `unsupported_labels` in the executor config:

```json
"unsupported_labels": [{
  "labels": ["linux-gfx942-1gpu-ccs-csp-ossci-rocm"],
  "reason": "This deployment has Radeon AI PRO R9700 (gfx1201), not gfx942 hardware."
}]
```

Label sets are compared case-insensitively and without regard to order; extra or
missing labels do not match. The dispatcher skips a configured job only when no
BB profile matches and no online, enabled Gitea runner can claim it. The skipped
job gets a summary with the labels and reason. Runner lookup or database errors
fail open and leave the job queued. A matching profile with zero current
schedulable capacity also stays queued. Unconfigured labels always remain
queued, so profile omissions are not silently treated as unsupported hardware.

Each managed runner is bound to exactly one job. Concurrent ordinary runners may
claim a waiting job first; the dispatcher then cancels its unused BB task.
Deterministic idempotency keys recover submissions whose responses were lost.
Cancellation and completed job cleanup resume after Gitea restarts. A completed
job allows its runner up to two minutes to finish flushing before forced task
cancellation. BB task exit status is infrastructure state; the native Gitea job
result remains authoritative. Infrastructure failure before a result marks the
job failed.

BB must implement `/v1/tasks` with package-set preparation. Multiple Gitea
instances must share the Redis global-lock backend, as other Gitea background
services do. Drain active tasks before changing the BB endpoint/project or
disabling the dispatcher. Completed task bindings remain as deduplication
records; their encrypted runner identities are cleared after cleanup.

## GitHub Actions duplicate filtering

The BB executor checks GitHub Actions before admitting a waiting job from a
GitHub mirror. Filtering applies only to repositories marked as GitHub mirrors;
ordinary Gitea repositories and Gitea-only workflow directories are unaffected.
The lookup is currently enabled only for `push` events and is scoped to the same
mirror, exact commit SHA, workflow path, and trigger event. Pull request events
fail open because Gitea's merge commit and GitHub's head SHA are not guaranteed
to identify the same commit. The source workflow must be in
`.github/workflows`, and both the workflow source commit recorded by Gitea and
the job commit must exactly match the GitHub run's `head_sha`. Any SHA mismatch
fails open and runs Gitea.

GitHub's workflow-jobs API exposes job display names rather than YAML job IDs.
Gitea therefore matches the exact expanded display name within the workflow
and groups all Gitea matrix siblings for the same job. It skips the Gitea
siblings only when every name maps uniquely and every corresponding GitHub
check succeeded. Missing or ambiguous names, failed or skipped checks, and
unsupported workflow/event mappings run in Gitea. A pending matching GitHub
check holds admission for at most two minutes; after that Gitea runs. Results
are cached per mirror, SHA, workflow, and event for 30 seconds, and paginated
GitHub responses are used to select the latest run/attempt for that key.

Public mirrors use GitHub's unauthenticated API. For private mirrors, filtering
is disabled unless `github_token_file` points to an absolute path containing a
read-only GitHub token with Actions read access. Gitea does not reuse credentials
embedded in mirror remotes. API errors, missing permissions, and rate limits
fail open and allow Gitea to run; this can reduce deduplication but cannot block
the Gitea job. Keep the token file readable only by the Gitea service account.
