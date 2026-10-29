# Builds theo into .\dist.
#
#   .\scripts\build.ps1                build for this system
#   .\scripts\build.ps1 all            build for linux, darwin and windows (amd64 and arm64)
#   .\scripts\build.ps1 linux/arm64    build for one target
#
# The version comes from $env:VERSION if set, else from `git describe`.
param([string]$Target = "")

$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

$version = $env:VERSION
if (-not $version) {
    $version = (git describe --tags --always --dirty 2>$null)
    if (-not $version) { $version = "0.0.0-dev" }
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

New-Item -ItemType Directory -Force dist | Out-Null
if ($Target -eq "") {
    Build (go env GOOS) (go env GOARCH)
} elseif ($Target -eq "all") {
    foreach ($os in "linux", "darwin", "windows") {
        foreach ($arch in "amd64", "arm64") { Build $os $arch }
    }
} elseif ($Target -match "^(\w+)/(\w+)$") {
    Build $Matches[1] $Matches[2]
} else {
    Write-Error "usage: build.ps1 [all | <os>/<arch>]"
}
