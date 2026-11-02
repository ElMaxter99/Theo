# Builds theo-desktop (the web interface in a native window) for Windows into .\dist.
# It needs cgo and a C compiler, so it runs on Windows itself.
#
#   .\scripts\build-desktop.ps1          build theo-desktop.exe
#   .\scripts\build-desktop.ps1 -Cli     also build theo.exe
#
# The version comes from $env:VERSION if set, else from `git describe`.
param([switch]$Cli)
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

function Fail($msg) { Write-Host "build-desktop: $msg" -ForegroundColor Red; exit 1 }

if (-not (Get-Command go -ErrorAction SilentlyContinue)) { Fail "Go is not installed (https://go.dev/dl/)" }
if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) {
  Fail "no C compiler (gcc) in PATH. Install one with:  winget install BrechtSanders.WinLibs.POSIX.UCRT   then open a NEW PowerShell window"
}

$version = $env:VERSION
if (-not $version) { $version = (git describe --tags --always --dirty 2>$null) }
if (-not $version) { $version = "0.0.0-dev" }
$pkg = "github.com/ElMaxter99/Theo/internal/engine"
$arch = (go env GOARCH)

New-Item -ItemType Directory -Force dist | Out-Null

if ($Cli) {
  Write-Host "building dist\theo.exe ($version)"
  $env:CGO_ENABLED = "0"
  go build -trimpath -ldflags "-s -w -X $pkg.Version=$version" -o dist\theo.exe .\cmd\theo
  if ($LASTEXITCODE -ne 0) { Fail "building theo failed" }
}

# The icon of the executable.
go run github.com/akavel/rsrc@v0.10.2 -ico web/public/brand/avatar-plum.ico -arch $arch -o "cmd/theo-desktop/rsrc_windows_$arch.syso"
if ($LASTEXITCODE -ne 0) { Fail "generating the icon resource failed" }

Write-Host "building dist\theo-desktop.exe ($version)"
$env:CGO_ENABLED = "1"
go build -tags desktop -trimpath -ldflags "-s -w -H=windowsgui -X $pkg.Version=$version" -o dist\theo-desktop.exe .\cmd\theo-desktop
if ($LASTEXITCODE -ne 0) { Fail "building theo-desktop failed" }
Write-Host "done: dist\theo-desktop.exe  (needs the Microsoft Edge WebView2 Runtime, included in Windows 11)"
