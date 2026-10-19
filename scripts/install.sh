#!/bin/sh
# Copyright 2026 The Gitea Authors. All rights reserved.
# SPDX-License-Identifier: MIT
#
# install.sh downloads the gt binary for this system from dl.gitea.com, verifies its SHA-256
# checksum and installs it.
#
#   curl -fsSL https://gitea.com/gitea/cli/raw/branch/main/scripts/install.sh | sh
#   curl -fsSL https://gitea.com/gitea/cli/raw/branch/main/scripts/install.sh | sh -s -- --nightly
#
# --nightly installs the latest build of the main branch instead of a release; --version VERSION
# installs that version.
#
# Environment variables:
#   GITEA_CLI_VERSION       version to install, such as 1.0.0, or nightly (default: the latest release)
#   GITEA_CLI_INSTALL_DIR   directory to install gt into (default: /usr/local/bin when it is
#                           writable, else ~/.local/bin)
#   GITEA_CLI_DOWNLOAD_URL  base URL of the release files (default: https://dl.gitea.com/gitea-cli)

set -eu

download_url=${GITEA_CLI_DOWNLOAD_URL:-https://dl.gitea.com/gitea-cli}
releases_api=https://gitea.com/api/v1/repos/gitea/cli/releases/latest

fail() {
  echo "install.sh: $*" >&2
  exit 1
}

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$2" "$1"
  else
    fail "curl or wget is required"
  fi
}

platform() {
  case $(uname -s) in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    FreeBSD) os=freebsd ;;
    *) fail "unsupported system $(uname -s); download gt from $download_url" ;;
  esac
  case $(uname -m) in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    armv7*) arch=arm-7 ;;
    armv6*) arch=arm-6 ;;
    armv5*) arch=arm-5 ;;
    *) fail "unsupported architecture $(uname -m); download gt from $download_url" ;;
  esac
  echo "$os-$arch"
}

latest_version() {
  fetch "$releases_api" "$work/release.json" || fail "cannot read the latest release; set GITEA_CLI_VERSION"
  version=$(sed -n 's/.*"tag_name":"v\{0,1\}\([^"]*\)".*/\1/p' "$work/release.json")
  [ -n "$version" ] || fail "cannot read the latest release; set GITEA_CLI_VERSION"
  echo "$version"
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d' ' -f1
  else
    fail "sha256sum or shasum is required to verify the download"
  fi
}

install_dir() {
  if [ -n "${GITEA_CLI_INSTALL_DIR:-}" ]; then
    echo "$GITEA_CLI_INSTALL_DIR"
  elif [ -w /usr/local/bin ]; then
    echo /usr/local/bin
  else
    echo "$HOME/.local/bin"
  fi
}

requested=${GITEA_CLI_VERSION:-}
while [ $# -gt 0 ]; do
  case $1 in
    --nightly) requested=nightly ;;
    --version)
      [ $# -gt 1 ] || fail "--version needs a version, such as 1.0.0"
      requested=$2
      shift
      ;;
    *) fail "unknown option $1; use --nightly or --version VERSION" ;;
  esac
  shift
done

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT INT TERM

version=${requested:-$(latest_version)}
version=${version#v}
# Nightly builds of the main branch are published as version main.
[ "$version" != nightly ] || version=main
file="gitea-cli-$version-$(platform)"
# The compressed file is smaller; without xz, the plain binary is downloaded instead.
if command -v xz >/dev/null 2>&1; then
  file="$file.xz"
fi

echo "Downloading gt $version ($file)"
fetch "$download_url/$version/$file" "$work/$file" || fail "cannot download $download_url/$version/$file"
fetch "$download_url/$version/$file.sha256" "$work/$file.sha256" || fail "cannot download the checksum of $file"
expected=$(cut -d' ' -f1 "$work/$file.sha256")
[ "$(sha256 "$work/$file")" = "$expected" ] || fail "checksum mismatch for $file; not installing"

binary="$work/gt"
case $file in
  *.xz) xz -dc "$work/$file" >"$binary" ;;
  *) mv "$work/$file" "$binary" ;;
esac
chmod 755 "$binary"

dir=$(install_dir)
mkdir -p "$dir"
mv "$binary" "$dir/gt" || fail "cannot write to $dir; set GITEA_CLI_INSTALL_DIR to a writable directory"
echo "Installed gt $version to $dir/gt"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "Add $dir to your PATH to run gt" ;;
esac
