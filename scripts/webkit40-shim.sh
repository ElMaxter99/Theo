#!/usr/bin/env bash
# webview_go asks pkg-config for webkit2gtk-4.0, which Ubuntu 24.04 and newer
# no longer ship (only webkit2gtk-4.1, same API for what webview uses). This
# writes a webkit2gtk-4.0.pc that forwards to 4.1 and prints the directory to
# put on PKG_CONFIG_PATH:
#   export PKG_CONFIG_PATH="$(scripts/webkit40-shim.sh):$PKG_CONFIG_PATH"
# It prints nothing harmful and writes nothing when a real 4.0 is installed.
set -euo pipefail
if pkg-config --exists webkit2gtk-4.0 2>/dev/null; then
  echo "."
  exit 0
fi
dir="${1:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/webkit40-shim}"
mkdir -p "$dir"
cat > "$dir/webkit2gtk-4.0.pc" <<PC
Name: webkit2gtk-4.0 (alias of 4.1)
Description: forwards to webkit2gtk-4.1
Version: $(pkg-config --modversion webkit2gtk-4.1)
Requires: webkit2gtk-4.1
PC
echo "$dir"
