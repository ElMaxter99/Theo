#!/bin/sh
# Theo @VERSION@ for Linux: installs the app and the command line for your user,
# no root needed. Run it with:  sh theo-installer_@VERSION@_linux_@ARCH@.sh
# Theo then updates itself from the app ("Update now"). Remove it with theo-uninstall.
set -eu

bin="${HOME}/.local/bin"
data="${XDG_DATA_HOME:-$HOME/.local/share}"
apps="$data/applications"
icons="$data/icons/hicolor/128x128/apps"

echo "Theo @VERSION@: installing for your user"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

start="$(awk '/^__THEO_PAYLOAD__$/ { print NR + 1; exit }' "$0")"
tail -n +"$start" "$0" | tar -xz -C "$tmp"

mkdir -p "$bin" "$apps" "$icons"
# Copy beside, then move: a Theo that is running keeps its file.
for f in theo theo-desktop; do
  cp "$tmp/$f" "$bin/.$f.new"
  chmod 0755 "$bin/.$f.new"
  mv -f "$bin/.$f.new" "$bin/$f"
  echo "  ok  $bin/$f"
done
install -m 0644 "$tmp/theo.png" "$icons/theo.png"
# An absolute Exec path: launchers do not always have ~/.local/bin in PATH.
sed "s|^Exec=.*|Exec=$bin/theo-desktop|" "$tmp/theo-desktop.desktop" > "$apps/theo-desktop.desktop"
echo "  ok  the \"Theo\" entry in your applications menu"
command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$apps" >/dev/null 2>&1 || true

cat > "$bin/theo-uninstall" <<UNINSTALL
#!/bin/sh
# Removes Theo (the programs, the menu entry and the icon). Your projects and Theo's data stay.
rm -f "$bin/theo" "$bin/theo-desktop" "$apps/theo-desktop.desktop" "$icons/theo.png" "$bin/theo-uninstall"
echo "Theo removed."
UNINSTALL
chmod 0755 "$bin/theo-uninstall"

echo "Done. Open it from the applications menu, or run: theo-desktop"
echo "Uninstall: theo-uninstall"

case ":$PATH:" in
  *":$bin:"*) ;;
  *) echo "Note: $bin is not in your PATH, so the theo command is not found yet. Add it to your shell profile:  export PATH=\"\$HOME/.local/bin:\$PATH\"" ;;
esac
if ! ldconfig -p 2>/dev/null | grep -q 'libwebkit2gtk-4\.1'; then
  echo "Note: the window needs WebKitGTK 4.1, which was not found. Install it with:"
  if command -v apt-get >/dev/null 2>&1; then echo "  sudo apt install libwebkit2gtk-4.1-0"
  elif command -v dnf >/dev/null 2>&1; then echo "  sudo dnf install webkit2gtk4.1"
  elif command -v pacman >/dev/null 2>&1; then echo "  sudo pacman -S webkit2gtk-4.1"
  elif command -v zypper >/dev/null 2>&1; then echo "  sudo zypper install libwebkit2gtk-4_1-0"
  else echo "  (the package is called webkit2gtk-4.1 or libwebkit2gtk-4.1 in your distribution)"; fi
  echo "Until then, 'theo ui' opens the same interface in your browser."
fi
exit 0
__THEO_PAYLOAD__
