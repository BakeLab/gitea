#!/bin/bash
set -euo pipefail

case "${1:-linux}" in
  linux|linux/amd64) ;;
  *) echo "Unsupported Gitea server target: $1 (supported: linux/amd64)" >&2; exit 1 ;;
esac

GO="${GO:-go}"
tags="bindata $TAGS"
"$GO" generate -tags "$tags" ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$GO" build -tags "$tags" \
  -ldflags "-s -w $LDFLAGS" -o "${DIST}/binaries/gitea-${VERSION##*/}-linux-amd64" .
