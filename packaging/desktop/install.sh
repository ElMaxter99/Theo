#!/bin/sh
# Installs Theo Desktop for the current user (no root): the program in
# ~/.local/bin, the launcher entry and the icon under ~/.local/share.
# Remove it by deleting those three files.
set -eu
here="$(cd "$(dirname "$0")" && pwd)"
bin="${HOME}/.local/bin"
apps="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
icons="${XDG_DATA_HOME:-$HOME/.local/share}/icons/hicolor/128x128/apps"
mkdir -p "$bin" "$apps" "$icons"
install -m 0755 "$here/theo-desktop" "$bin/theo-desktop"
install -m 0644 "$here/theo.png" "$icons/theo.png"
# An absolute Exec path: launchers do not always have ~/.local/bin in PATH.
sed "s|^Exec=.*|Exec=$bin/theo-desktop|" "$here/theo-desktop.desktop" > "$apps/theo-desktop.desktop"
command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$apps" >/dev/null 2>&1 || true
echo "Installed: $bin/theo-desktop and the \"Theo\" launcher entry."
