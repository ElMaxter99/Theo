#!/usr/bin/env bash
# Builds theo into ./dist.
#
#   scripts/build.sh              build for this system
#   scripts/build.sh all          build for linux, darwin and windows (amd64 and arm64)
#   scripts/build.sh linux/arm64  build for one target
#
# The version comes from VERSION if set, else from `git describe`.
set -euo pipefail

cd "$(dirname "$0")/.."
version="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)}"
pkg=github.com/ElMaxter99/Theo/internal/engine

build() {
  local os="$1" arch="$2" ext=""
  [ "$os" = windows ] && ext=".exe"
  local out="dist/theo-${os}-${arch}${ext}"
  echo "building $out ($version)"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X ${pkg}.Version=${version}" -o "$out" ./cmd/theo
}

mkdir -p dist
case "${1:-}" in
  "")
    build "$(go env GOOS)" "$(go env GOARCH)"
    ;;
  all)
    for os in linux darwin windows; do
      for arch in amd64 arm64; do build "$os" "$arch"; done
    done
    ;;
  */*)
    build "${1%/*}" "${1#*/}"
    ;;
  *)
    echo "usage: scripts/build.sh [all | <os>/<arch>]" >&2
    exit 2
    ;;
esac
