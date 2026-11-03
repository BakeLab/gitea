#!/usr/bin/env bash
# Copyright 2026 The Gitea Authors. All rights reserved.
# SPDX-License-Identifier: MIT
#
# Runs the Go tests on Windows in a dockur/windows QEMU VM on the docker host, see CONTRIBUTING.md.
#
# Env:
#   WINDOWS_TEST_IMAGE    dockur/windows image, pinned in the Makefile
#   WINDOWS_TEST_TIMEOUT  minutes to wait for installing Windows and running the tests (default 165)
#   GO                    go command for building the test binaries (default go)
#   WINDOWS_TEST_CACHE    directory to keep the installed Windows in, so later runs skip the install
set -euo pipefail

image="${WINDOWS_TEST_IMAGE:?}"
timeout="${WINDOWS_TEST_TIMEOUT:-165}"
cache="${WINDOWS_TEST_CACHE:-}" # keeps the installed Windows between runs
go="${GO:-go}"
work="$(mktemp -d)"
volume="gitea-cli-windows-test-${work##*.}"
name=""

cleanup() {
  docker rm -fv "$name" >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

echo "==> Building Windows test binaries"
oem="$work/oem"
shared="$work/shared"
payload="$work/payload"
src="$payload/src"
mkdir -p "$oem" "$shared" "$payload/bin" "$src"
module="$("$go" list -m)"
packages="$(GOOS=windows "$go" list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./...)"
GOOS=windows GOARCH=amd64 "$go" test -c -ldflags='-s -w' -o "$payload/bin/" $packages
for package in $packages; do
  echo "${package##*/}.test.exe ${package#"$module"/}"
done > "$payload/tests.txt"
git ls-files -z --cached --others --exclude-standard | while IFS= read -r -d '' file; do
  if [ -e "$file" ]; then printf '%s\0' "$file"; fi
done | tar --null -T - -cf - | tar -xf - -C "$src"
# a repository like the CI checkout, which the tests run in
git -C "$src" init -q -b main
git -C "$src" add -Af .
git -C "$src" -c user.name=test -c user.email=test@localhost -c commit.gpgsign=false -c core.hooksPath=/dev/null commit -qm test
git -C "$src" remote add origin https://gitea.com/gitea/cli
cp scripts/test-windows.ps1 "$payload/"
tar -cf "$shared/payload.tar" -C "$payload" .
cp scripts/test-windows-setup.ps1 "$oem/"
sed 's/$/\r/' > "$shared/run.bat" <<'EOF'
@echo off
set log=\\host.lan\Data\install.log
rd /s /q C:\tests 2> nul
md C:\tests
tar.exe -xf \\host.lan\Data\payload.tar -C C:\tests >> %log% 2>&1
C:\pwsh\pwsh.exe -NoProfile -ExecutionPolicy Bypass -File C:\tests\test-windows.ps1 >> %log% 2>&1
if not exist \\host.lan\Data\exitcode (echo 1)> \\host.lan\Data\exitcode
cd /d C:\
rd /s /q C:\tests
if not exist \\host.lan\Data\save (shutdown.exe /s /t 600 & exit /b)
:collected
if not exist \\host.lan\Data\collected (ping.exe -n 2 127.0.0.1 > nul & goto collected)
defrag.exe C: /L > nul
shutdown.exe /s /t 0
EOF
sed 's/$/\r/' > "$oem/install.bat" <<'EOF' # a policy turns off real-time scanning, AMSI blocks scripts that do it
@echo off
set log=\\host.lan\Data\install.log
echo ==^> install.bat started at %TIME% >> %log%
reg.exe add "HKLM\SOFTWARE\Policies\Microsoft\Windows Defender\Real-Time Protection" /v DisableRealtimeMonitoring /t REG_DWORD /d 1 /f > nul
powershell.exe -NoProfile -ExecutionPolicy Bypass -File C:\OEM\test-windows-setup.ps1 >> %log% 2>&1
echo ==^> setup exited with %ERRORLEVEL% >> %log%
if %ERRORLEVEL% neq 0 (shutdown.exe /s /t 600 & exit /b)
(
  echo @echo off
  echo :wait
  echo if exist \\host.lan\Data\run.bat goto run
  echo ping.exe -n 3 127.0.0.1 ^> nul
  echo goto wait
  echo :run
  echo call \\host.lan\Data\run.bat
)> C:\run.bat
schtasks.exe /create /tn tests /sc onlogon /it /rl highest /tr C:\run.bat /f > nul
call C:\run.bat
EOF

kvm=Y cores=max
if ! docker run --rm --privileged --entrypoint sh "$image" -c 'test -w /dev/kvm'; then
  kvm=N cores=4 # more emulated vCPUs slow Windows down
  echo "==> The docker host has no usable /dev/kvm, Windows runs emulated and much slower"
fi

vm=(-e VERSION=2025 -e EDITION=core -e RAM_SIZE=8G -e DISK_SIZE=32G -e DISK_FMT=qcow2)
fingerprint="$(printf '%s\n' "$image" "${vm[*]}" | cat - "$oem/install.bat" "$oem/test-windows-setup.ps1" | sha256sum | cut -d' ' -f1)" # what the installed disk depends on
docker volume create "$volume" >/dev/null
cached=N
if [ -n "$cache" ] && [ "$(cat "$cache/fingerprint" 2>/dev/null)" = "$fingerprint" ]; then
  cached=Y
  echo "==> Booting the cached Windows install"
else
  echo "==> Installing Windows"
  if [ -n "$cache" ]; then
    touch "$shared/save"
  fi
fi
deadline=$((SECONDS + timeout * 60))
boot() {
  name="gitea-cli-windows-test-${work##*.}-$1" # a new name ends the log and monitor loops of the previous VM
  # the timeout stops the VM even when this script is killed before its cleanup runs
  docker create --rm --name "$name" --privileged -v "$volume:/storage" "${vm[@]}" -e KVM="$kvm" -e CPU_CORES="$cores" -e DISK_CACHE=unsafe -e DISK_IO=threads \
    --entrypoint timeout "$image" "$(((deadline - SECONDS) / 60 + 10))m" /usr/bin/tini -s /run/entry.sh >/dev/null
  if [ "$cached" = Y ]; then
    docker cp "$cache/storage/." "$name:/storage"
  else
    docker cp "$oem" "$name:/oem"
  fi
  docker cp "$shared" "$name:/shared"
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
    docker volume rm "$volume" >/dev/null && docker volume create "$volume" >/dev/null
    boots=$((boots + 1))
    boot "$boots"
  fi
  sleep 10
done
results="$work/results"
docker exec "$name" rm /shared/payload.tar
docker cp "$name:/shared" "$results"
docker exec "$name" touch /shared/collected

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

if [ -f "$shared/save" ]; then
  echo "==> Saving the Windows install to $cache"
  until [ "$(docker container inspect -f '{{.State.Running}}' "$name" 2>/dev/null)" != true ]; do
    sleep 2
  done
  docker run --rm -v "$volume:/storage" --entrypoint sh "$image" -c \
    'cd /storage && rm -f -- *.iso && qemu-img convert -O qcow2 data.qcow2 data.new && mv data.new data.qcow2'
  rm -rf "$cache" && mkdir -p "$cache"
  helper="$(docker create -v "$volume:/storage" --entrypoint true "$image")"
  docker cp "$helper:/storage" "$cache/storage"
  docker rm "$helper" >/dev/null
  echo "$fingerprint" > "$cache/fingerprint"
  if [ -n "${GITHUB_OUTPUT:-}" ]; then
    echo "save-cache=true" >> "$GITHUB_OUTPUT"
  fi
fi
exit "$(tr -d '\r\n' < "$results/exitcode")"
