# Fork releases

The internal Gitea mirror publishes Linux amd64 binaries from `trim` after every
CI job succeeds. It reuses the binary with embedded frontend assets exercised by
E2E. Branch builds validate the publisher without writing releases.

Each release contains `gitea-linux-amd64`, its SHA-256 checksum, and a manifest
recording the full source commit, platform and version. The publisher verifies
these inputs before creating a draft and only makes the release public after all
three assets upload successfully. Failed uploads leave a draft; rerun the workflow
to publish a new uniquely named release. Running trim publications are not
cancelled by later pushes.

Find complete builds on the internal repository's Releases page. `trim-*` release
names avoid upstream tag-release workflows. Pull-mirror synchronization may prune
locally created Git tags: use the manifest's source commit for provenance and the
release's uploaded assets for downloads, rather than treating the release tag as
a permanent Git ref or using generated source archives.

Publication uses the job token with repository content-write permission. It does
not install the binary or restart the production service. Deployment remains a
separate operation through the existing infrastructure workflow.

Run local publisher checks with
`python3 .gitea/scripts/test-publish-release.py` (requires Bash, jq and sha256sum).
