# Copyright 2026 The Gitea Authors. All rights reserved.
# SPDX-License-Identifier: MIT
#
# install.ps1 downloads gt.exe for this system from dl.gitea.com, verifies its SHA-256 checksum,
# installs it and adds its directory to the user's PATH.
#
#   irm https://gitea.com/gitea/cli/raw/branch/main/scripts/install.ps1 | iex
#   $env:GITEA_CLI_VERSION = "nightly"; irm https://gitea.com/gitea/cli/raw/branch/main/scripts/install.ps1 | iex
#
# Environment variables:
#   GITEA_CLI_VERSION       version to install, such as 1.0.0, or nightly for the latest build of
#                           the main branch (default: the latest release)
#   GITEA_CLI_INSTALL_DIR   directory to install gt.exe into (default: %LOCALAPPDATA%\Programs\gitea-cli)
#   GITEA_CLI_DOWNLOAD_URL  base URL of the release files (default: https://dl.gitea.com/gitea-cli)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$downloadUrl = if ($env:GITEA_CLI_DOWNLOAD_URL) { $env:GITEA_CLI_DOWNLOAD_URL } else { 'https://dl.gitea.com/gitea-cli' }
$installDir = if ($env:GITEA_CLI_INSTALL_DIR) { $env:GITEA_CLI_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\gitea-cli' }

$version = $env:GITEA_CLI_VERSION
if (-not $version) {
    try {
        $version = (Invoke-RestMethod 'https://gitea.com/api/v1/repos/gitea/cli/releases/latest').tag_name
    } catch {
        throw "cannot read the latest release; set GITEA_CLI_VERSION"
    }
}
$version = $version -replace '^v', ''
# Nightly builds of the main branch are published as version main.
if ($version -eq 'nightly') { $version = 'main' }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { throw "unsupported architecture $($env:PROCESSOR_ARCHITECTURE); download gt from $downloadUrl" }
}
$file = "gitea-cli-$version-windows-$arch.exe"
$work = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $work | Out-Null
try {
    Write-Host "Downloading gt $version ($file)"
    $binary = Join-Path $work $file
    Invoke-WebRequest -UseBasicParsing -Uri "$downloadUrl/$version/$file" -OutFile $binary
    Invoke-WebRequest -UseBasicParsing -Uri "$downloadUrl/$version/$file.sha256" -OutFile "$binary.sha256"
    $expected = ((Get-Content -Raw "$binary.sha256") -split '\s+')[0]
    if ((Get-FileHash -Algorithm SHA256 $binary).Hash -ne $expected.ToUpperInvariant()) {
        throw "checksum mismatch for $file; not installing"
    }
    New-Item -ItemType Directory -Force -Path $installDir | Out-Null
    Move-Item -Force $binary (Join-Path $installDir 'gt.exe')
} finally {
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
Write-Host "Installed gt $version to $(Join-Path $installDir 'gt.exe')"

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $installDir) {
    [Environment]::SetEnvironmentVariable('Path', ($(if ($userPath) { "$userPath;" } else { '' }) + $installDir), 'User')
    $env:Path = "$env:Path;$installDir"
    Write-Host "Added $installDir to your PATH; open a new terminal to run gt"
}
