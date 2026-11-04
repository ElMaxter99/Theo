# Builds theo-desktop (the web interface in a native window) for Windows into .\dist.
# It needs Go and a C compiler (cgo), so it runs on Windows itself.
#
#   .\scripts\build-desktop.ps1                 build dist\theo-desktop.exe
#   .\scripts\build-desktop.ps1 -Cli            also build dist\theo.exe
#   .\scripts\build-desktop.ps1 -InstallDeps    install Go and gcc with winget if missing
#   .\scripts\build-desktop.ps1 -Run            open the app when the build finishes
#
# If PowerShell refuses to run the script (execution policy), use build-desktop.cmd
# next to it, or:  powershell -ExecutionPolicy Bypass -File .\scripts\build-desktop.ps1
#
# The version comes from $env:VERSION if set, else from `git describe`.
# Works in Windows PowerShell 5.1 and PowerShell 7.
param([switch]$Cli, [switch]$InstallDeps, [switch]$Run)
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

function Fail($msg) { Write-Host "build-desktop: $msg" -ForegroundColor Red; exit 1 }

# Runs a native command with stderr allowed (Windows PowerShell 5.1 turns native stderr
# into errors under $ErrorActionPreference = "Stop") and returns its output lines.
function Native([scriptblock]$cmd) {
  $old = $ErrorActionPreference; $ErrorActionPreference = "Continue"
  try { & $cmd 2>&1 | ForEach-Object { "$_" } } finally { $ErrorActionPreference = $old }
}

# Re-reads PATH from the registry so tools installed a minute ago are found without a new window.
function Update-Path {
  $m = [Environment]::GetEnvironmentVariable("Path", "Machine")
  $u = [Environment]::GetEnvironmentVariable("Path", "User")
  $env:Path = (($m, $u) | Where-Object { $_ }) -join ";"
  # WinLibs installs gcc under the user's WinGet packages and only adds a link dir to PATH.
  $links = Join-Path $env:LOCALAPPDATA "Microsoft\WinGet\Links"
  if ((Test-Path $links) -and ($env:Path -notlike "*$links*")) { $env:Path += ";$links" }
  $pk = Join-Path $env:LOCALAPPDATA "Microsoft\WinGet\Packages"
  if (Test-Path $pk) {
    Get-ChildItem $pk -Directory -Filter "BrechtSanders.WinLibs*" -ErrorAction SilentlyContinue | ForEach-Object {
      $bin = Join-Path $_.FullName "mingw64\bin"
      if ((Test-Path $bin) -and ($env:Path -notlike "*$bin*")) { $env:Path += ";$bin" }
    }
  }
}

function Winget-Install($id) {
  if (-not (Get-Command winget -ErrorAction SilentlyContinue)) { Fail "winget is not available; install $id by hand (see docs/cli/desktop.md)" }
  Write-Host "installing $id with winget..."
  Native { winget install -e --id $id --accept-package-agreements --accept-source-agreements } | Write-Host
  Update-Path
}

Update-Path

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
  if ($InstallDeps) { Winget-Install "GoLang.Go" }
  if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Fail "Go is not installed. Run this script again with -InstallDeps, or install it from https://go.dev/dl/ and open a NEW PowerShell window"
  }
}
if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) {
  if ($InstallDeps) { Winget-Install "BrechtSanders.WinLibs.POSIX.UCRT" }
  if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) {
    Fail "no C compiler (gcc) found. Run this script again with -InstallDeps, or install one with:  winget install BrechtSanders.WinLibs.POSIX.UCRT   and open a NEW PowerShell window"
  }
}

$version = $env:VERSION
if (-not $version -and (Get-Command git -ErrorAction SilentlyContinue)) {
  $version = (Native { git describe --tags --always --dirty } | Select-Object -First 1)
  if ($LASTEXITCODE -ne 0 -or "$version" -match "^(fatal|error)") { $version = $null }
}
if (-not $version) { $version = "0.0.0-dev" }
$pkg = "github.com/ElMaxter99/Theo/internal/engine"
$arch = (go env GOARCH)

New-Item -ItemType Directory -Force dist | Out-Null

if ($Cli) {
  Write-Host "building dist\theo.exe ($version)"
  $env:CGO_ENABLED = "0"
  Native { go build -trimpath -ldflags "-s -w -X $pkg.Version=$version" -o dist\theo.exe .\cmd\theo } | Write-Host
  if ($LASTEXITCODE -ne 0) { Fail "building theo failed" }
}

# The icon of the executable.
Write-Host "generating the icon resource"
Native { go run github.com/akavel/rsrc@v0.10.2 -ico web/public/brand/avatar-plum.ico -arch $arch -o "cmd/theo-desktop/rsrc_windows_$arch.syso" } | Write-Host
if ($LASTEXITCODE -ne 0) { Fail "generating the icon resource failed (it needs internet the first time to download the rsrc tool)" }

Write-Host "building dist\theo-desktop.exe ($version)"
$env:CGO_ENABLED = "1"
Native { go build -tags desktop -trimpath -ldflags "-s -w -H=windowsgui -X $pkg.Version=$version" -o dist\theo-desktop.exe .\cmd\theo-desktop } | Write-Host
if ($LASTEXITCODE -ne 0) { Fail "building theo-desktop failed (see the messages above)" }

$exe = Join-Path (Get-Location) "dist\theo-desktop.exe"
Write-Host "done: $exe" -ForegroundColor Green
Write-Host "double-click it to open the app (needs the Microsoft Edge WebView2 Runtime, included in Windows 11)"
if ($Run) { Start-Process $exe }
