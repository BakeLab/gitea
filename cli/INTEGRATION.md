# Integrated BakeLab CLI

This directory contains the `gitea.dev/cli` Go module and builds the `gt` command.
It was imported from `BakeLab/gt` branch `trim` at
[bc10e04e19ea6e2acca55d3b6b32aeb7042ea0a1](https://gitea.int.bb.pub/BakeLab/gitea/src/commit/bc10e04e19ea6e2acca55d3b6b32aeb7042ea0a1).
The import merge retains that commit's
complete parent history in this repository. Preserve the import merge when
integrating this change; squashing it would discard that ancestry.

The original MIT license remains in `LICENSE`, and `UPSTREAM_COMMIT` and
`UPSTREAM_COMMIT_SHA1` preserve the upstream baseline. The module path remains
`gitea.dev/cli` so internal imports and downstream tooling keep working; it does
not depend on the retired standalone repository.

Develop and test from this directory with `make test`, `make vet` and `make lint`.
The root Gitea CI tests this module and builds Linux amd64/arm64 and macOS arm64
binaries. Gitea's trim release contains all server and CLI assets under the same
release tag, with checksums and manifests recording this repository's commit and
the `cli` source path. There is no separate CLI repository publishing workflow.

Download builds from the internal `BakeLab/gitea` Releases page. Existing
organization-owned Generic Packages from earlier CLI releases remain available
for consumers that pin those versions; repository consolidation does not replace
or delete those packages.
