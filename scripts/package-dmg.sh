#!/usr/bin/env bash
# Makes the macOS disk image from the desktop archive: Theo.app next to a link
# to /Applications. Needs macOS (hdiutil). Run from the repository root.
#
#   scripts/package-dmg.sh <tag> <arch> <path-to-theo-desktop_<tag>_darwin_<arch>.zip> [out-dir]
set -euo pipefail

tag="${1:?tag}"; arch="${2:?arch}"; zip="${3:?desktop archive}"; out="${4:-dist}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$out"
ditto -x -k "$zip" "$work/unzipped"
app="$(find "$work/unzipped" -maxdepth 2 -name Theo.app -type d | head -n 1)"
[ -d "$app" ] || { echo "Theo.app is not in $zip" >&2; exit 1; }
stage="$work/stage"; mkdir -p "$stage"
ditto "$app" "$stage/Theo.app"
ln -s /Applications "$stage/Applications"
hdiutil create -volname "Theo ${tag#v}" -srcfolder "$stage" -fs HFS+ -format UDZO -ov "$out/Theo_${tag}_darwin_${arch}.dmg" > /dev/null
ls -l "$out/Theo_${tag}_darwin_${arch}.dmg"
