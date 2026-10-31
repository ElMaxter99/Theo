#!/usr/bin/env bash
# Packages a built theo-desktop binary for its system and writes the archive to
# dist/. Run from the repository root.
#
#   scripts/package-desktop.sh <goos> <goarch> <tag> <path-to-binary>
#
#   linux   -> dist/theo-desktop_<tag>_linux_<arch>.tar.gz
#              theo-desktop, theo-desktop.desktop, theo.png, install.sh
#   darwin  -> dist/theo-desktop_<tag>_darwin_<arch>.zip      (Theo.app bundle)
#              needs macOS tools: sips, iconutil, codesign, ditto
#   windows -> dist/theo-desktop_<tag>_windows_<arch>.zip     (theo-desktop.exe)
#
# The archives are named so that `sha256sum *.tar.gz *.zip` in the release job
# covers them next to the CLI archives.
set -euo pipefail

goos="${1:?goos}"; goarch="${2:?goarch}"; tag="${3:?tag}"; bin="${4:?binary}"
[ -f "$bin" ] || { echo "no such binary: $bin" >&2; exit 1; }
brand=web/public/brand
name="theo-desktop_${tag}_${goos}_${goarch}"
root="dist/$name"
rm -rf "$root"
mkdir -p "$root"

case "$goos" in
linux)
  install -m 0755 "$bin" "$root/theo-desktop"
  install -m 0644 packaging/desktop/theo-desktop.desktop "$root/theo-desktop.desktop"
  install -m 0644 "$brand/app-icon-plum-128.png" "$root/theo.png"
  install -m 0755 packaging/desktop/install.sh "$root/install.sh"
  cp LICENSE README.md "$root/"
  tar -C dist -czf "dist/$name.tar.gz" "$name"
  ;;
darwin)
  app="$root/Theo.app"
  mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
  install -m 0755 "$bin" "$app/Contents/MacOS/theo-desktop"
  sed "s/@VERSION@/${tag#v}/g" packaging/desktop/Info.plist.in > "$app/Contents/Info.plist"
  iconset="$(mktemp -d)/Theo.iconset"
  mkdir -p "$iconset"
  sips -z 16 16 "$brand/app-icon-plum-128.png" --out "$iconset/icon_16x16.png" >/dev/null
  sips -z 32 32 "$brand/app-icon-plum-128.png" --out "$iconset/icon_16x16@2x.png" >/dev/null
  sips -z 32 32 "$brand/app-icon-plum-128.png" --out "$iconset/icon_32x32.png" >/dev/null
  cp "$brand/app-icon-plum-64.png" "$iconset/icon_32x32@2x.png"
  cp "$brand/app-icon-plum-128.png" "$iconset/icon_128x128.png"
  iconutil -c icns "$iconset" -o "$app/Contents/Resources/Theo.icns"
  # Ad-hoc signature: not a Developer ID, so Gatekeeper still asks on first
  # open (see docs/cli/desktop.md), but the bundle is internally consistent.
  codesign --force --deep --sign - "$app"
  cp LICENSE README.md "$root/"
  (cd dist && ditto -c -k --keepParent "$name" "$name.zip")
  ;;
windows)
  install -m 0755 "$bin" "$root/theo-desktop.exe"
  cp LICENSE README.md "$root/"
  (cd dist && powershell -NoProfile -Command "Compress-Archive -Path '$name' -DestinationPath '$name.zip' -Force")
  ;;
*)
  echo "unsupported goos: $goos" >&2; exit 1 ;;
esac
rm -rf "$root"
ls -l dist
