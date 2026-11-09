#!/usr/bin/env bash
# Checks a release pull request (the branch release/X.Y.Z -> main) against the
# release rules in CONTRIBUTING.md, so a mistake shows on the pull request and
# not after the merge has already tagged the release.
#
#   scripts/check-release-pr.sh release/0.7.0 [base-ref]
#
# Run it from a checkout of the pull request (CI uses the merge result). The
# base ref (default origin/main) is what the README changes are measured from.
# Errors fail the check; warnings only print.
set -uo pipefail

branch="${1:?usage: check-release-pr.sh release/X.Y.Z [base-ref]}"
base="${2:-origin/main}"
errors=0

fail() { echo "::error::$*"; errors=$((errors + 1)); }
warn() { echo "::warning::$*"; }

ver="${branch#release/}"
if [ "$ver" = "$branch" ] || ! [[ "$ver" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  fail "Branch '$branch' is not release/X.Y.Z (digits only)."
  exit 1
fi
tag="v$ver"

# section FILE HEADING: the text under "## HEADING" up to the next "## ".
section() {
  awk -v h="## $2" '
    $0 == h || index($0, h " ") == 1 { on = 1; next }
    /^## / { on = 0 }
    on' "$1"
}
has_text() { grep -q '[^[:space:]]'; }

# 1. The version is new.
if git rev-parse -q --verify "refs/tags/$tag" > /dev/null; then
  fail "$tag already exists. Start a new release/ branch with the next version."
fi

# 2. The changelog has the release's section: it becomes the release notes.
if ! [ -f CHANGELOG.md ] || ! section CHANGELOG.md "$tag" | has_text; then
  fail "CHANGELOG.md has no '## $tag' section with text (it is the release's notes)."
fi

# 3. Nothing is left under "Unreleased": it would never reach a release.
if [ -f CHANGELOG.md ] && section CHANGELOG.md "Unreleased" | has_text; then
  fail "CHANGELOG.md still has entries under '## Unreleased'. Move them into '## $tag'."
fi

# 4. Spanish notes, once the project keeps them (CHANGELOG.es.md).
if [ -f CHANGELOG.es.md ]; then
  if ! section CHANGELOG.es.md "$tag" | has_text; then
    fail "CHANGELOG.es.md has no '## $tag' section with text (the release notes are published in both languages)."
  fi
  if section CHANGELOG.es.md "Unreleased" | has_text; then
    fail "CHANGELOG.es.md still has entries under '## Unreleased'. Move them into '## $tag'."
  fi
fi

# 5. Both READMEs are reviewed together and describe this version.
changed=$(git diff --name-only "$base"...HEAD 2> /dev/null || true)
en=0; es=0
grep -qx 'README.md' <<< "$changed" && en=1
grep -qx 'README.es.md' <<< "$changed" && es=1
if [ "$en" != "$es" ]; then
  fail "README.md and README.es.md must change together: only one of them is changed in this pull request."
elif [ "$en" = 0 ]; then
  warn "Neither README.md nor README.es.md is changed: the release rules ask for both to be reviewed every release."
fi
for f in README.md README.es.md; do
  if [ -f "$f" ] && ! grep -qF "${ver%.*}" "$f"; then
    warn "$f never mentions ${ver%.*}; its \"What's new\" should describe this release."
  fi
done

if [ "$errors" -gt 0 ]; then
  echo "$errors problem(s) in the release pull request for $tag."
  exit 1
fi
echo "Release pull request for $tag: ready."
