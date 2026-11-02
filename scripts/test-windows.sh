#!/usr/bin/env bash
# Copyright 2026 The Gitea Authors. All rights reserved.
# SPDX-License-Identifier: MIT
#
# Runs the Go tests on Windows in a dockur/windows QEMU VM on the docker host, see CONTRIBUTING.md.
#
# Env:
#   WINDOWS_TEST_IMAGE    dockur/windows image, pinned in the Makefile
#   WINDOWS_TEST_TIMEOUT  minutes to wait for installing Windows and running the tests (default 165)
set -euo pipefail

image="${WINDOWS_TEST_IMAGE:?}"
timeout="${WINDOWS_TEST_TIMEOUT:-165}"
name=""
work="$(mktemp -d)"

cleanup() {
  docker rm -fv "$name" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

echo "==> Building Windows test binaries"
oem="$work/oem"
mkdir -p "$oem/bin" "$oem/src" "$work/shared"
module="$(go list -m)"
packages="$(GOOS=windows go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./...)"
GOOS=windows GOARCH=amd64 go test -c -ldflags='-s -w' -o "$oem/bin/" $packages
for package in $packages; do
  echo "${package##*/}.test.exe ${package#"$module"/}"
done > "$oem/tests.txt"
git ls-files -z --cached --others --exclude-standard | while IFS= read -r -d '' file; do
  if [ -e "$file" ]; then printf '%s\0' "$file"; fi
done | tar --null -T - -cf - | tar -xf - -C "$oem/src"
# a repository like the CI checkout, which the tests run in
git -C "$oem/src" init -q -b main
git -C "$oem/src" add -Af .
git -C "$oem/src" -c user.name=test -c user.email=test@localhost -c commit.gpgsign=false -c core.hooksPath=/dev/null commit -qm test
git -C "$oem/src" remote add origin https://gitea.com/gitea/cli
cp scripts/test-windows-setup.ps1 scripts/test-windows.ps1 "$oem/"
# AMSI blocks a script that turns off real-time scanning, which costs minutes per test run, so a policy does it
# the delayed shutdown only matters when the host never removes the VM
sed 's/$/\r/' > "$oem/install.bat" <<'EOF'
@echo off
set log=\\host.lan\Data\install.log
echo ==^> install.bat started at %TIME% >> %log%
reg.exe add "HKLM\SOFTWARE\Policies\Microsoft\Windows Defender\Real-Time Protection" /v DisableRealtimeMonitoring /t REG_DWORD /d 1 /f > nul
powershell.exe -NoProfile -ExecutionPolicy Bypass -File C:\OEM\test-windows-setup.ps1 >> %log% 2>&1
echo ==^> setup exited with %ERRORLEVEL% >> %log%
if %ERRORLEVEL% equ 0 C:\pwsh\pwsh.exe -NoProfile -ExecutionPolicy Bypass -File C:\OEM\test-windows.ps1 >> %log% 2>&1
if not exist \\host.lan\Data\exitcode (echo 1)> \\host.lan\Data\exitcode
shutdown.exe /s /t 600
EOF

kvm=Y cores="$(nproc)"
if ! docker run --rm --privileged --entrypoint sh "$image" -c 'test -w /dev/kvm'; then
  kvm=N cores=4 # more emulated vCPUs slow Windows down
  echo "==> The docker host has no usable /dev/kvm, Windows runs emulated and much slower"
fi

echo "==> Installing Windows"
deadline=$((SECONDS + timeout * 60))
boot() {
  name="gitea-cli-windows-test-$$-$1" # a new name ends the log and monitor loops of the previous VM
  # the timeout stops the VM even when this script is killed before its cleanup runs
  docker create --rm --name "$name" --privileged -e VERSION=2025 -e EDITION=core -e KVM="$kvm" -e CPU_CORES="$cores" -e RAM_SIZE=8G \
    -e DISK_SIZE=32G -e DISK_CACHE=unsafe -e DISK_IO=threads --entrypoint timeout "$image" "$(((deadline - SECONDS) / 60 + 10))m" /usr/bin/tini -s /run/entry.sh >/dev/null
  docker cp "$oem" "$name:/oem"
  docker cp "$work/shared" "$name:/shared"
  docker start "$name" >/dev/null
  docker logs -f "$name" &
  while sleep 10 && docker container inspect "$name" >/dev/null 2>&1; do # short sleeps end the loop soon after cleanup
    if ((SECONDS % 300 < 10)); then
      echo "==> $((SECONDS / 60))m elapsed, VM disk $(docker exec "$name" du -sh /storage | cut -f1)"
    fi
  done &
  docker exec "$name" sh -c 'cd /shared && touch install.log summary.txt && tail -qf install.log summary.txt' &
}

boots=1
boot "$boots"
until docker exec "$name" test -f /shared/exitcode 2>/dev/null; do
  if ((SECONDS >= deadline)) || [ "$(docker container inspect -f '{{.State.Running}}' "$name" 2>/dev/null)" != true ]; then
    echo "Windows stopped or did not finish the tests within $timeout minutes" >&2
    exit 1
  fi
  # emulated, the firmware can give up on the install image before it has read it
  if docker logs "$name" 2>&1 | grep -q 'No bootable option or device was found'; then
    if ((boots == 3)); then
      echo "Windows did not boot from its install image $boots times" >&2
      exit 1
    fi
    echo "==> Windows did not boot from its install image, starting a new VM"
    docker rm -fv "$name" >/dev/null
    boots=$((boots + 1))
    boot "$boots"
  fi
  sleep 10
done
results="$work/results"
docker cp "$name:/shared" "$results"

tr -d '\r' < "$results/summary.txt" | while read -r result package _; do
  if [ "$result" = FAIL ]; then
    echo "::group::$package"
    cat "$results/${package//\//-}.log"
    echo "::endgroup::"
  fi
done
if [ -f "$results/error.txt" ]; then
  cat "$results/error.txt" >&2
fi
echo "==> Slowest tests"
find "$results" -name '*.log' -exec cat {} + | tr -d '\r' | sed -nE 's/^--- (PASS|FAIL): (\S+) \(([0-9.]+)s\)$/\3 \2/p' | sort -rn | awk 'NR <= 20'
echo "==> Packages"
tr -d '\r' < "$results/summary.txt"
exit "$(tr -d '\r\n' < "$results/exitcode")"
