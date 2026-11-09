# Builds Theo into .\dist.
#
#   .\scripts\build.ps1                on Windows: dist\theo.exe AND dist\theo-desktop.exe (the app)
#                                      elsewhere:  dist/theo-<os>-<arch> (the CLI)
#   .\scripts\build.ps1 all            the CLI for linux, darwin and windows (amd64 and arm64),
#                                      plus the desktop app for this system when it can be built
#   .\scripts\build.ps1 linux/arm64    the CLI for one target
#
# Options (Windows, passed to build-desktop.ps1):
#   -Update       git pull --ff-only first, so the exes contain the latest main
#   -InstallDeps  install Go and gcc with winget if they are missing
#   -Ui           also rebuild the web interface (needs Node.js)
#   -NoDesktop    only the CLI (dist\theo.exe on Windows, no C compiler needed)
#
# The desktop app needs cgo (gcc), so it can only be built on the system it is for.
# The version comes from $env:VERSION if set, else from `git describe`.
param([string]$Target = "", [switch]$Update, [switch]$InstallDeps, [switch]$Ui, [switch]$NoDesktop, [switch]$Run)

$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

$version = $env:VERSION
if (-not $version) {
    # PowerShell 5.1 turns a native command's stderr into an error under "Stop"; relax it for git.
    $old = $ErrorActionPreference; $ErrorActionPreference = "Continue"
    try { $version = (git describe --tags --always --dirty 2>$null | Select-Object -First 1) } catch { $version = $null } finally { $ErrorActionPreference = $old }
    if ($LASTEXITCODE -ne 0 -or -not $version) { $version = "0.0.0-dev" }
}
$pkg = "github.com/ElMaxter99/Theo/internal/engine"

function Build([string]$os, [string]$arch) {
    $ext = if ($os -eq "windows") { ".exe" } else { "" }
    $out = "dist/theo-$os-$arch$ext"
    Write-Host "building $out ($version)"
    $env:CGO_ENABLED = "0"; $env:GOOS = $os; $env:GOARCH = $arch
    try {
        go build -trimpath -ldflags "-s -w -X $pkg.Version=$version" -o $out ./cmd/theo
        if ($LASTEXITCODE -ne 0) { throw "go build failed for $os/$arch" }
    } finally {
        Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
    }
}

$hostOs = (go env GOOS)
$desktopScript = Join-Path $PSScriptRoot "build-desktop.ps1"
function Desktop([switch]$Cli) {
    $a = @{}
    foreach ($k in "Update", "InstallDeps", "Ui", "Run") { if ((Get-Variable $k).Value) { $a[$k] = $true } }
    if (-not $Cli) { $a["NoCli"] = $true }
    & $desktopScript @a
    if ($LASTEXITCODE -ne 0) { throw "build-desktop.ps1 failed" }
}

New-Item -ItemType Directory -Force dist | Out-Null
if ($Target -eq "" -and $hostOs -eq "windows") {
    if ($NoDesktop) { Build "windows" (go env GOARCH); Copy-Item "dist/theo-windows-$(go env GOARCH).exe" "dist/theo.exe" -Force }
    else { Desktop -Cli }   # theo.exe + theo-desktop.exe, with the stale-checkout check and version report
} elseif ($Target -eq "") {
    Build $hostOs (go env GOARCH)
} elseif ($Target -eq "all") {
    foreach ($os in "linux", "darwin", "windows") {
        foreach ($arch in "amd64", "arm64") { Build $os $arch }
    }
    if ($hostOs -eq "windows" -and -not $NoDesktop) { Desktop }
} elseif ($Target -match "^(\w+)/(\w+)$") {
    Build $Matches[1] $Matches[2]
} else {
    Write-Error "usage: build.ps1 [all | <os>/<arch>]"
}
