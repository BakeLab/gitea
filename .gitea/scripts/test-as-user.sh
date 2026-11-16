#!/usr/bin/env bash
set -euo pipefail
if (( EUID != 0 )); then
    exec "$@"
fi
id gitea-ci >/dev/null 2>&1 || useradd --create-home --shell /bin/bash gitea-ci
cache_dir="${RUNNER_TEMP:?}/gitea-ci"
mkdir -p "$cache_dir"
# Keep Go's cache accessible without exposing root's home to the test user.
module_cache="$(go env GOMODCACHE)"
if [[ "$module_cache" != "$cache_dir/mod" && ! -d "$cache_dir/mod" ]]; then
    cp -a "$module_cache" "$cache_dir/mod"
fi
workspace_owner="$(stat -c '%u:%g' "${GITHUB_WORKSPACE:?}")"
trap 'chown -R "$workspace_owner" "$GITHUB_WORKSPACE"' EXIT
chown -R gitea-ci:gitea-ci "$GITHUB_WORKSPACE" "$cache_dir"
runuser -u gitea-ci -- env HOME=/home/gitea-ci USER=gitea-ci LOGNAME=gitea-ci \
    PATH="$PATH" GOPATH="$cache_dir/go" GOMODCACHE="$cache_dir/mod" GOCACHE="$cache_dir/build" "$@"
