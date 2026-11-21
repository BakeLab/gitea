#!/usr/bin/env bash
set -euo pipefail

cd "${1:?release directory is required}"
: "${GITHUB_SHA:?}"
cli_commit="$GITHUB_SHA"
jq -e --arg commit "$GITHUB_SHA" \
  '.commit == $commit and .platform == "linux-amd64" and (.version | type == "string" and length > 0)' manifest.json > /dev/null
test -s gitea-linux-amd64
test "$(cat gitea-linux-amd64.sha256)" = "$(sha256sum gitea-linux-amd64)"
cli_version="trim-${cli_commit:0:12}-${GITHUB_RUN_ID:?}-${GITHUB_RUN_ATTEMPT:?}"
jq -e --arg commit "$cli_commit" --arg version "$cli_version" \
  '.repository == "BakeLab/gitea" and .path == "cli" and .commit == $commit and .version == $version and .platforms == ["linux-amd64", "linux-arm64", "darwin-arm64"]' cli-manifest.json > /dev/null
files=(gitea-linux-amd64 gitea-linux-amd64.sha256 manifest.json cli-manifest.json)
for platform in linux-amd64 linux-arm64 darwin-arm64; do
  binary="gt-${platform}"
  test -s "$binary"
  test "$(cat "$binary.sha256")" = "$(sha256sum "$binary")"
  files+=("$binary" "$binary.sha256")
done
jq --slurpfile cli cli-manifest.json '.cli = $cli[0]' manifest.json > manifest.json.tmp
mv manifest.json.tmp manifest.json
if [[ "${2:-}" == --check ]]; then
  exit 0
fi

[[ "${GITHUB_REF:-}" == refs/heads/trim ]]
[[ "${GITHUB_EVENT_NAME:-}" == push || "${GITHUB_EVENT_NAME:-}" == workflow_dispatch ]]
: "${RELEASE_TOKEN:?}"
: "${GITHUB_SERVER_URL:?}"
: "${GITHUB_REPOSITORY:?}"
: "${GITHUB_RUN_ID:?}"
: "${GITHUB_RUN_ATTEMPT:?}"
api="${GITHUB_SERVER_URL}/api/v1/repos/${GITHUB_REPOSITORY}"
tag="trim-${GITHUB_SHA:0:12}-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}"
version=$(jq -r '.version' manifest.json)
source_url="${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/src/commit/${GITHUB_SHA}"
cli_source_url="${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/src/commit/${cli_commit}/cli"
payload=$(jq -n --arg tag "$tag" --arg commit "$GITHUB_SHA" --arg version "$version" --arg source_url "$source_url" --arg cli_commit "$cli_commit" --arg cli_source_url "$cli_source_url" \
  '{tag_name: $tag, target_commitish: $commit, name: $tag, body: ($version + "\nGitea source: [" + $commit + "](" + $source_url + ")\nCLI source: [" + $cli_commit + "](" + $cli_source_url + ")\nGitea Linux amd64 with embedded frontend assets; gt for Linux amd64/arm64 and macOS arm64. Download uploaded binaries, SHA-256 checksums and manifests; generated tag archives are not release deliverables."), draft: true, prerelease: true}')
release=$(curl --fail-with-body --silent --show-error \
  -H "Authorization: token ${RELEASE_TOKEN}" -H 'Content-Type: application/json' \
  --data "$payload" "${api}/releases")
id=$(jq -er '.id' <<< "$release")
for file in "${files[@]}"; do
  curl --fail-with-body --silent --show-error \
    -H "Authorization: token ${RELEASE_TOKEN}" -F "attachment=@${file}" \
    "${api}/releases/${id}/assets?name=${file}" > /dev/null
done
curl --fail-with-body --silent --show-error -X PATCH \
  -H "Authorization: token ${RELEASE_TOKEN}" -H 'Content-Type: application/json' \
  --data '{"draft":false}' "${api}/releases/${id}" > /dev/null
url="${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/releases/tag/${tag}"
printf 'Published %s\n' "$url"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  printf 'Published [%s](%s) from %s.\n' "$tag" "$url" "$GITHUB_SHA" >> "$GITHUB_STEP_SUMMARY"
fi
