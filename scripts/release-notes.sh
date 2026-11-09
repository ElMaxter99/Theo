#!/usr/bin/env bash
# Prints the notes of a release: the "## vX.Y.Z" section of CHANGELOG.md and,
# when CHANGELOG.es.md has the same section, a "## Español" part with it.
# Prints nothing when CHANGELOG.md has no such section (the caller then falls
# back to GitHub's generated notes).
#
#   scripts/release-notes.sh v0.7.0 [CHANGELOG.md] [CHANGELOG.es.md]
set -euo pipefail

version="${1:?usage: release-notes.sh vX.Y.Z [CHANGELOG.md] [CHANGELOG.es.md]}"
en="${2:-CHANGELOG.md}"
es="${3:-CHANGELOG.es.md}"

section() {
  awk -v h="## $2" '
    $0 == h || index($0, h " ") == 1 { on = 1; next }
    /^## / { on = 0 }
    on' "$1"
}

notes=$(section "$en" "$version")
printf '%s\n' "$notes" | grep -q '[^[:space:]]' || exit 0
printf '%s\n' "$notes"

if [ -f "$es" ]; then
  notes_es=$(section "$es" "$version")
  if printf '%s\n' "$notes_es" | grep -q '[^[:space:]]'; then
    printf '\n---\n\n## Español\n%s\n' "$notes_es"
  fi
fi
