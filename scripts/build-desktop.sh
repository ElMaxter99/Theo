#!/usr/bin/env bash
# Builds theo-desktop (the web interface in a native window) for THIS system into ./dist.
# It needs cgo and the system web view, so it cannot be cross-compiled: run it on each system.
#
#   scripts/build-desktop.sh          build theo-desktop
#   scripts/build-desktop.sh --cli    also build theo
#
# The version comes from VERSION if set, else from `git describe`.
set -euo pipefail

cd "$(dirname "$0")/.."
version="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)}"
pkg=github.com/ElMaxter99/Theo/internal/engine
os="$(go env GOOS)"
ldflags="-s -w -X ${pkg}.Version=${version}"

fail() { echo "build-desktop: $*" >&2; exit 1; }

command -v go >/dev/null || fail "Go is not installed (https://go.dev/dl/)"
command -v gcc >/dev/null || command -v cc >/dev/null || fail "no C compiler found (needed for cgo)"

case "$os" in
  linux)
    command -v pkg-config >/dev/null || fail "pkg-config is missing: sudo apt install pkg-config"
    pkg-config --exists gtk+-3.0 || fail "GTK 3 headers missing: sudo apt install libgtk-3-dev"
    pkg-config --exists webkit2gtk-4.1 || pkg-config --exists webkit2gtk-4.0 ||
      fail "WebKitGTK headers missing: sudo apt install libwebkit2gtk-4.1-dev"
    # The web view library asks for webkit2gtk-4.0, which newer distributions no longer ship.
    if ! pkg-config --exists webkit2gtk-4.0; then
      export PKG_CONFIG_PATH="$(scripts/webkit40-shim.sh):${PKG_CONFIG_PATH:-}"
    fi
    out=dist/theo-desktop
    ;;
  darwin)
    xcode-select -p >/dev/null 2>&1 || fail "Xcode command line tools missing: xcode-select --install"
    out=dist/theo-desktop
    ;;
  windows)
    # Git Bash / MSYS: the PowerShell script does the same with Windows-specific checks.
    out=dist/theo-desktop.exe
    ldflags="$ldflags -H=windowsgui"
    go run github.com/akavel/rsrc@v0.10.2 -ico web/public/brand/avatar-plum.ico -arch "$(go env GOARCH)" \
      -o "cmd/theo-desktop/rsrc_windows_$(go env GOARCH).syso"
    ;;
  *) fail "unsupported system: $os" ;;
esac

mkdir -p dist
if [ "${1:-}" = "--cli" ]; then
  ext=""; [ "$os" = windows ] && ext=".exe"
  echo "building dist/theo${ext} ($version)"
  CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X ${pkg}.Version=${version}" -o "dist/theo${ext}" ./cmd/theo
fi
echo "building $out ($version)"
CGO_ENABLED=1 go build -tags desktop -trimpath -ldflags "$ldflags" -o "$out" ./cmd/theo-desktop
echo "done: $out"
