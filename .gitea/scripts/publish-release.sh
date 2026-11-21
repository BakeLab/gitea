#!/usr/bin/env bash
set -euo pipefail

cd "${1:?release directory is required}"
: "${GITHUB_SHA:?}"
jq -e --arg commit "$GITHUB_SHA" \
  '.commit == $commit and .platform == "linux-amd64" and (.version | type == "string" and length > 0)' manifest.json > /dev/null
test -s gitea-linux-amd64
test "$(cat gitea-linux-amd64.sha256)" = "$(sha256sum gitea-linux-amd64)"
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
payload=$(jq -n --arg tag "$tag" --arg commit "$GITHUB_SHA" --arg version "$version" --arg source_url "$source_url" \
  '{tag_name: $tag, target_commitish: $commit, name: $tag, body: ($version + "\nSource: [" + $commit + "](" + $source_url + ")\nLinux amd64 with embedded frontend assets. Download the uploaded binary, SHA-256 checksum and manifest; generated tag archives are not release deliverables."), draft: true, prerelease: true}')
release=$(curl --fail-with-body --silent --show-error \
  -H "Authorization: token ${RELEASE_TOKEN}" -H 'Content-Type: application/json' \
  --data "$payload" "${api}/releases")
id=$(jq -er '.id' <<< "$release")
for file in gitea-linux-amd64 gitea-linux-amd64.sha256 manifest.json; do
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
