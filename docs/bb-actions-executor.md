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

After resolving profile inheritance and capability overlays, Gitea adds the
hostname from `ROOT_URL` to `allowed_hosts` and its host and effective HTTPS port
to `required_internal_services`. The runner registration uses that same URL.
Existing entries are preserved and the derived entries are added only if absent.
Both capacity queries and task creation use the resulting sandbox specification.
Profiles need only declare their other network dependencies; eligible BB nodes
must still advertise the Gitea route.

Example configuration using the previously authorized human profile (replace
the project, token record path and SHA-256):

```json
{
  "endpoint": "https://breadboard.int.bb.pub",
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
      "allowed_hosts": ["*"]
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
independent store, such as a local BB CLI login; authorize a dedicated session
for Gitea with `bb auth login --device --credential-store file`. Profile
authentication is the only supported BB credential mode; it uses the configured
issuer and resource and does not require a client secret. All BB executor
credential files (`profile_auth.token_file` and `github_token_file`) must be
absolute paths to regular
files with no group or other permissions. Gitea rejects files larger than 64
KiB. Create them as mode `0600` and keep them owned by the Gitea service
account.

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

A profile may set `"extends": "<profile name>"` to inherit another profile and
override only some fields. Inheritance is resolved once at load time, before the
normal validation, which then runs on the resolved profile. Multi-level chains
are allowed; an unknown or ambiguous parent, a cycle, or a child without a unique
name is a load error. The child must define its own non-empty `labels` (labels
are never inherited). `runner_url`, `runner_sha256`, `package_sets`, `platform`
`max_duration_s` and `exec_user` replace the parent value when set. `repositories` optionally
limits a profile to exact `owner/repository` names; a child value replaces the
parent list, and an omitted value inherits it. This keeps repository-specific
runner aliases from claiming similarly named labels in other repositories.
`sandbox` is deep-merged:
child keys override, nested objects such as `cpu` merge by key, and lists such as
`allowed_hosts` and `required_internal_services` are replaced.

```json
{
  "name": "linux-arm64",
  "extends": "linux-ubuntu-latest",
  "labels": ["self-hosted:host", "linux:host", "arm64:host", "cpu:host", "ubuntu-24.04-arm:host"],
  "runner_url": "https://github.com/BakeLab/runner/releases/download/node-runtime-4af8cd2b/gitea-runner-4af8cd2b-linux-arm64",
  "runner_sha256": "107ecd5e76ea7e830a19a35e461a320b7d44a3a09fee03b89b2c74f179a5c1c3",
  "sandbox": {"cpu": {"arch": "arm64"}}
}
```

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

## Gitea-only workflow exclusions

The Gitea BB dispatcher skips two workflows that rely on GitHub-only
integrations: `BakeLab/intel-xpu-backend-for-triton` / `codeql.yml` and
`BakeLab/rustfs` / `functional-chain-health.yml`. Matching is exact on repository
and workflow filename. A matching job is marked Skipped with the reason in its
job summary before BB dispatch. These rules apply only to Gitea; the repositories'
GitHub Actions workflows are unchanged.

### Long CPU builds

A trusted profile can set `max_duration_s` (60–604800 seconds) to override the
global default for its BB task, command and runner timeout together. Omission
inherits the parent profile's value, then falls back to the global default.
Workflow `timeout-minutes` alone cannot extend the BB task lifetime.

For TheRock's SDK build, apply a 24-hour limit (`86400`) only to its trusted
build configuration. Reuse the existing CPU, Linux and architecture labels;
no SDK-specific runner label is needed. The build needs CPU memory of `65536`
MiB and disk of `512000` MiB on a verified znver4-compatible node with sufficient
available host capacity, without an accelerator runtime. A disk request does
not establish free host storage.

The current `capability_overlays` only compose accelerator requests. They do
not configure memory, disk, CPU features or time limits. Scope any additional
resource configuration to the SDK build; do not raise global CPU defaults.
