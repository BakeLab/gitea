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

Example configuration (replace the project, OAuth settings and SHA-256):

```json
{
  "endpoint": "https://bmetal.int.bb.pub:18092",
  "project": "prj_example",
  "token_url": "https://id.bb.pub/api/oidc/token",
  "client_id": "gitea-actions",
  "client_secret_file": "/etc/gitea/bb-client-secret",
  "resource": "https://breadboard.int.bb.pub/api",
  "scopes": ["breadboard:operate"],
  "max_concurrent": 4,
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

Use the token endpoint published by your issuer's discovery document. For
Pocket ID, the token endpoint is `https://id.bb.pub/api/oidc/token`. Configure a
dedicated confidential client for Gitea with the client-credentials grant and
operator access to the selected BB project, including its secrets and task
APIs. This does not require a BB CLI profile or keyring. OAuth access tokens
refresh automatically. Alternatively,
replace the OAuth fields with `token_file`; Gitea rereads that bearer-token file
for each request, allowing an external credential refresher to rotate it. Keep
credential files readable only by the Gitea service account.

Profiles are considered in order. All job `runs-on` labels must match the label
names before `:host` or `:docker://IMAGE`. Give ARM64 profiles an ARM64 sandbox
and an ARM64 runner binary. GPU profiles must explicitly request the appropriate
BB accelerator and package sets. Do not alias unavailable hardware to another
GPU class. Task requests use the BB sandbox API shape, so other immutable
placement constraints are available inside `sandbox`.

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

The dispatcher records the job, runner identity and immutable task request before
submitting work. The runner identity is encrypted with Gitea's `SECRET_KEY` and
sent through a per-task BB secret; no credential is embedded in the durable task
command. Secrets are refreshed while tasks are active, restoring them after a BB
restart. BB can fail preparation if a secret is unavailable when its command
starts; that failure is reported to the Gitea job rather than leaving it queued.

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

## Explicitly unsupported runner labels

The optional `unsupported_labels` setting contains exact `runs-on` label sets
that this deployment cannot execute. A matching waiting job is marked Skipped
with its labels and configured reason in the job summary, unless an online,
enabled Gitea runner can match it. This check runs before BB capacity and
concurrency checks, so unsupported jobs do not stay queued when BB is full or
its capacity endpoint is unavailable. Unknown label sets and supported profiles
with no current capacity remain queued. Configure only hardware and operating
systems that are confirmed unavailable; do not map a specific GPU model to a
different accelerator.

```json
{
  "unsupported_labels": [{
    "labels": ["linux-gfx942-1gpu-ccs-csp-ossci-rocm"],
    "reason": "This deployment has no gfx942/MI300 accelerator."
  }]
}
```

Label matching is case-insensitive and order-independent, but requires an exact
set match. Each entry needs a unique non-empty label set and a non-empty reason.
The queue lookup and runner lookup fail open: an error leaves the job queued.
