#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?BB_SDK_BOT_TOKEN is required}"
: "${GH_REPO:?repository is required}"
: "${GITHUB_RUN_ID:?workflow run ID is required}"
: "${RUNNER_TEMP:?runner temporary directory is required}"

open_prs=$(gh pr list --base trim --state open --json headRefName --jq '[.[] | select(.headRefName | startswith("depbot/bb-sdk-"))] | length')
if [[ "$open_prs" != 0 ]]; then
  echo 'A BB SDK update PR is already open.'
  exit 0
fi

export GIT_CONFIG_GLOBAL="$RUNNER_TEMP/bb-sdk-depbot.gitconfig"
trap 'rm -f "$GIT_CONFIG_GLOBAL"' EXIT
gh auth setup-git
module=github.com/BakeLab/breadboard/sdk/go
before=$(go list -m -f '{{.Version}}' "$module")
go get "$module@main"
after=$(go list -m -f '{{.Version}}' "$module")
if [[ "$before" == "$after" ]]; then
  echo 'BB SDK is current.'
  exit 0
fi
make tidy
git diff --check
branch="depbot/bb-sdk-$GITHUB_RUN_ID"
git switch -c "$branch"
git config user.name 'bb-sdk-depbot'
git config user.email 'bb-sdk-depbot@users.noreply.github.com'
git add go.mod go.sum assets/go-licenses.json
git commit -m "chore(deps): bump BB SDK to $after"
git push origin "$branch"
body="$RUNNER_TEMP/bb-sdk-depbot-pr.md"
printf 'Update the BB SDK from `%s` to `%s` to track Breadboard main. Generated with `go get` and `make tidy`; CI must pass before merging.\n' "$before" "$after" > "$body"
gh pr create --base trim --head "$branch" --title "chore(deps): bump BB SDK to $after" --body-file "$body"
