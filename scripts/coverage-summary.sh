#!/usr/bin/env bash
# Prints a Markdown summary of a Go coverage profile: the total, then each
# package from the least covered to the best. Report only; it never fails on a
# low number (CI has no coverage threshold).
#
#   go test -coverprofile=coverage.out ./...
#   scripts/coverage-summary.sh coverage.out
set -euo pipefail

profile="${1:-coverage.out}"
[ -f "$profile" ] || { echo "no coverage profile: $profile" >&2; exit 1; }

total=$(go tool cover -func="$profile" | awk '/^total:/ {print $3}')

echo "## Test coverage"
echo
echo "**Total: ${total}** of the statements (reported only; there is no threshold)."
echo
echo "| Package | Statements covered |"
echo "|---|---|"
# profile lines: <file>:<from>,<to> <statements> <count>; the mode line is skipped.
awk -F'[: ]' '
  NR == 1 && /^mode:/ { next }
  {
    file = $1
    pkg = file; sub("/[^/]*$", "", pkg)
    stmts[pkg] += $(NF-1)
    if ($NF > 0) hit[pkg] += $(NF-1)
  }
  END { for (p in stmts) printf "%.1f\t%s\t%d/%d\n", 100 * hit[p] / stmts[p], p, hit[p], stmts[p] }
' "$profile" | sort -n | while IFS=$'\t' read -r pct pkg counts; do
  echo "| \`${pkg#github.com/ElMaxter99/Theo/}\` | ${pct}% (${counts}) |"
done
