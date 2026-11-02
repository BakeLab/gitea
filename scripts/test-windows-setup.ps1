# Copyright 2026 The Gitea Authors. All rights reserved.
# SPDX-License-Identifier: MIT
#
# Installs the tools the tests need at the first logon of the VM from scripts/test-windows.sh.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$shared = '\\host.lan\Data'
try {
    Write-Output '==> Setting up Windows'
    Set-ItemProperty HKLM:\SYSTEM\CurrentControlSet\Control\FileSystem LongPathsEnabled 1
    foreach ($tool in @(
            @('git.7z.exe', 'https://github.com/git-for-windows/git/releases/download/v2.56.0.windows.3/PortableGit-2.56.0.3-64-bit.7z.exe', '4fc27b779829a36df77911e3f4dea09c66c790dd6c5f7dfa3b6a5479f1fe354f'),
            @('pwsh.zip', 'https://github.com/PowerShell/PowerShell/releases/download/v7.6.8/PowerShell-7.6.8-win-x64.zip', 'b9f074f923c499b51ec99d313da8e9ae90d17a9f3adf00c73d6189f34d908528'))) {
        Write-Output "==> Downloading $($tool[1])"
        Invoke-WebRequest -UseBasicParsing $tool[1] -OutFile "$env:TEMP\$($tool[0])"
        if ((Get-FileHash "$env:TEMP\$($tool[0])").Hash -ne $tool[2]) { throw "checksum mismatch for $($tool[1])" }
    }
    Write-Output '==> Extracting git and pwsh'
    New-Item -ItemType Directory C:\pwsh | Out-Null
    @(
        Start-Process "$env:TEMP\git.7z.exe" -ArgumentList '-oC:\git', '-y' -PassThru
        Start-Process tar.exe -ArgumentList '-xf', "$env:TEMP\pwsh.zip", '-C', 'C:\pwsh' -PassThru -NoNewWindow
    ) | Wait-Process
    $env:Path = "C:\git\cmd;C:\git\bin;C:\pwsh;$env:Path"
    Get-Command git, sh, pwsh | Out-Null
} catch {
    $_ | Out-String | Tee-Object "$shared\error.txt"
    Set-Content "$shared\exitcode" 1
    exit 1
}
