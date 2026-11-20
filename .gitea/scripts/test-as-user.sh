#!/usr/bin/env bash
set -euo pipefail
# These tests start a fresh Gitea instance. Socket activation and graceful
# restart state belong to the launcher that received the descriptors.
unset LISTEN_FDS LISTEN_PID LISTEN_FDNAMES GITEA_UNLINK_FDS NOTIFY_SOCKET WATCHDOG_USEC WATCHDOG_PID
if (( EUID != 0 )); then
    exec "$@"
fi
id gitea-ci >/dev/null 2>&1 || useradd --create-home --shell /bin/bash gitea-ci
# Runner workspaces and setup-go installations can have root-only parents.
test_root="$(mktemp -d /var/tmp/gitea-ci.XXXXXXXX)"
trap 'rm -rf -- "$test_root"' EXIT
mkdir -p "$test_root/workspace" "$test_root/tmp" "$test_root/goroot"
cp -a "${GITHUB_WORKSPACE:?}/." "$test_root/workspace/"
cp -a "$(go env GOROOT)/." "$test_root/goroot/"
module_cache="$(go env GOMODCACHE)"
if [[ -d "$module_cache" ]]; then
    cp -a "$module_cache" "$test_root/mod"
fi
chown -R gitea-ci:gitea-ci "$test_root"
cd "$test_root/workspace"
runuser -u gitea-ci -- env HOME=/home/gitea-ci USER=gitea-ci LOGNAME=gitea-ci \
    PATH="$test_root/goroot/bin:$PATH" GOROOT="$test_root/goroot" \
    GITHUB_WORKSPACE="$test_root/workspace" GITEA_WORKSPACE="$test_root/workspace" \
    RUNNER_TEMP="$test_root/tmp" TMPDIR="$test_root/tmp" \
    GOPATH="$test_root/go" GOMODCACHE="$test_root/mod" GOCACHE="$test_root/build" "$@"
