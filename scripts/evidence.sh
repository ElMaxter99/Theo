#!/usr/bin/env bash
# Runs the real checks for a change and collects the evidence for the pull request.
#
#   scripts/evidence.sh            # unit tests, web checks, mock e2e, real e2e (needs Docker)
#   scripts/evidence.sh --no-real  # skip the real e2e (no Docker); say so in the PR
#
# Output goes to evidence/ (git-ignored): report.md (paste it into the PR description),
# screenshots/ (the Playwright captures), logs/ (full output of every step).
# Exit code is non-zero if any step failed: do not open the PR as ready in that case.
set -u
cd "$(dirname "$0")/.."
root=$PWD
out=$root/evidence
real=1; [ "${1:-}" = "--no-real" ] && real=0
rm -rf "$out"; mkdir -p "$out/logs" "$out/screenshots"
rows=(); fail=0

step() { # name, dir, command...
  local name=$1 dir=$2; shift 2
  local log="$out/logs/$(echo "$name" | tr -c 'A-Za-z0-9' '-').log"
  if (cd "$dir" && "$@") >"$log" 2>&1; then rows+=("| $name | pass |"); else rows+=("| $name | **FAIL** (see logs/) |"); fail=1
    echo "::group::$name failed (last lines)"; grep -A14 -E "^(not ok|--- FAIL)" "$log" | head -60; tail -25 "$log"; echo "::endgroup::"; fi
}

step "gofmt"            . bash -c 'test -z "$(gofmt -l .)"'
step "go vet"           . go vet ./...
step "go test"          . go test ./...
step "web check + unit" web bash -c 'npm ci && npm run check && npm test'
step "web build is committed" . bash -c '(cd web && npm run build) && test -z "$(git status --porcelain -- internal/ui/dist)"'
step "e2e (mock)"       web npm run test:e2e
if [ $real = 1 ]; then
  docker info >/dev/null 2>&1 || { echo "Docker daemon not reachable (on a cloud session try: dockerd &)" >&2; exit 2; }
  step "e2e (real theo serve + Docker)" web npm run test:e2e:real
else
  rows+=("| e2e (real) | NOT RUN (--no-real) |")
fi

cp web/e2e/screenshots/*.png "$out/screenshots/" 2>/dev/null
{
  echo "## Evidence"
  echo; echo "Run: $(date -u +%FT%TZ) on $(uname -sm), commit $(git rev-parse --short HEAD)"
  echo; echo "| Check | Result |"; echo "|---|---|"; printf '%s\n' "${rows[@]}"
  echo; echo "Screenshots: $(ls "$out/screenshots" | wc -l) files in evidence/screenshots (also the CI artifact \`e2e-evidence\`)."
  echo "Only a desktop machine can show native notifications, window focus and the desktop app; say what was not checked."
} >"$out/report.md"
cat "$out/report.md"
exit $fail
