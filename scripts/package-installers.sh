#!/usr/bin/env bash
# Builds the installers that can be built on Linux, from the archives the build
# and desktop jobs already made. Run from the repository root.
#
#   scripts/package-installers.sh <tag> <assets-dir> <out-dir>
#
#   windows amd64 -> theo-setup_<tag>_windows_amd64.exe        (NSIS, per user)
#   linux amd64   -> theo-installer_<tag>_linux_amd64.sh       (per user, any distribution)
#                    theo_<tag>_linux_amd64.deb                 (Debian, Ubuntu)
#
# <assets-dir> holds theo_<tag>_<os>_<arch>.{tar.gz,zip} and
# theo-desktop_<tag>_<os>_<arch>.{tar.gz,zip}. The macOS .dmg is made by
# scripts/package-dmg.sh on a Mac.
set -euo pipefail

tag="${1:?tag}"; assets="${2:?assets dir}"; out="${3:?output dir}"
ver="${tag#v}"
brand=web/public/brand
mkdir -p "$out"
out="$(cd "$out" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

unpack() { # <archive> <destdir>
  mkdir -p "$2"
  case "$1" in
    *.zip) unzip -q -o "$1" -d "$2" ;;
    *) tar -xzf "$1" -C "$2" ;;
  esac
}
find_file() { find "$1" -type f -name "$2" | head -n 1; }

# ---- Windows -------------------------------------------------------------
cli="$assets/theo_${tag}_windows_amd64.zip"
desk="$assets/theo-desktop_${tag}_windows_amd64.zip"
if [ -f "$cli" ] && [ -f "$desk" ]; then
  w="$work/win"; mkdir -p "$w"
  unpack "$cli" "$work/win-cli"; unpack "$desk" "$work/win-desk"
  cp "$(find_file "$work/win-cli" theo.exe)" "$w/theo.exe"
  cp "$(find_file "$work/win-desk" theo-desktop.exe)" "$w/theo-desktop.exe"
  cp LICENSE "$w/LICENSE"
  # NSIS wants a numeric X.Y.Z for the file version: a release candidate keeps its suffix in the name only.
  makensis -V2 -DVERSION="${ver%%-*}" -DSRC="$w" -DICON="$PWD/$brand/avatar-plum.ico" \
    -DOUT="$out/theo-setup_${tag}_windows_amd64.exe" packaging/windows/installer.nsi
fi

# ---- Linux ---------------------------------------------------------------
cli="$assets/theo_${tag}_linux_amd64.tar.gz"
desk="$assets/theo-desktop_${tag}_linux_amd64.tar.gz"
if [ -f "$cli" ] && [ -f "$desk" ]; then
  unpack "$cli" "$work/lin-cli"; unpack "$desk" "$work/lin-desk"
  p="$work/payload"; mkdir -p "$p"
  install -m 0755 "$(find_file "$work/lin-cli" theo)" "$p/theo"
  install -m 0755 "$(find_file "$work/lin-desk" theo-desktop)" "$p/theo-desktop"
  install -m 0644 "$brand/app-icon-plum-128.png" "$p/theo.png"
  install -m 0644 packaging/desktop/theo-desktop.desktop "$p/theo-desktop.desktop"
  tar -C "$p" -czf "$work/payload.tar.gz" theo theo-desktop theo.png theo-desktop.desktop
  sh="$out/theo-installer_${tag}_linux_amd64.sh"
  sed -e "s/@VERSION@/${ver}/g" -e "s/@ARCH@/amd64/g" packaging/linux/installer-header.sh > "$sh"
  cat "$work/payload.tar.gz" >> "$sh"
  chmod 0755 "$sh"

  d="$work/deb"
  mkdir -p "$d/DEBIAN" "$d/usr/bin" "$d/usr/share/applications" "$d/usr/share/icons/hicolor/128x128/apps" "$d/usr/share/doc/theo"
  install -m 0755 "$p/theo" "$d/usr/bin/theo"
  install -m 0755 "$p/theo-desktop" "$d/usr/bin/theo-desktop"
  install -m 0644 "$p/theo-desktop.desktop" "$d/usr/share/applications/theo-desktop.desktop"
  install -m 0644 "$p/theo.png" "$d/usr/share/icons/hicolor/128x128/apps/theo.png"
  install -m 0644 LICENSE "$d/usr/share/doc/theo/copyright"
  size=$(du -sk "$d/usr" | cut -f1)
  # A release candidate sorts before its release: 0.11.0-rc.1 is 0.11.0~rc.1 for dpkg.
  debver="${ver/-rc./~rc.}"
  cat > "$d/DEBIAN/control" <<CONTROL
Package: theo
Version: ${debver}
Architecture: amd64
Maintainer: ElMaxter99 <ElMaxter99@users.noreply.github.com>
Installed-Size: ${size}
Depends: libgtk-3-0, libwebkit2gtk-4.1-0
Recommends: git, docker.io | docker-ce
Section: devel
Priority: optional
Homepage: https://github.com/ElMaxter99/Theo
Description: Isolated Docker Compose environments for your Git branches
 Theo gives every Git worktree its own Docker Compose environment. This
 package has the theo command and the theo-desktop window. Installed this way
 Theo cannot replace itself: install each new .deb, or use the installer
 script of the release, which installs for your user and updates from the app.
CONTROL
  dpkg-deb --root-owner-group -Zxz --build "$d" "$out/theo_${tag}_linux_amd64.deb" > /dev/null
fi
ls -l "$out"
