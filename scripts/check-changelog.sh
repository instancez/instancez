#!/usr/bin/env bash
# Usage: scripts/check-changelog.sh v0.0.12
# Fails unless CHANGELOG.md has a non-empty "## [0.0.12] - YYYY-MM-DD" section
# and nothing is left under [Unreleased]. Run before tagging; CI re-runs it on tags.
set -euo pipefail

ver="${1#v}"
f="$(dirname "$0")/../CHANGELOG.md"
[[ "$ver" =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]] || { echo "usage: $0 vX.Y.Z" >&2; exit 2; }

# body of a "## [name]" section, minus comments and blank lines
section() {
  awk -v h="## [$1]" 'index($0,h)==1{on=1;next} /^## \[/{on=0} on' "$f" \
    | grep -v -e '^<!--' -e '^[[:space:]]*$' -e '^### ' || true
}

grep -qE "^## \[$ver\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$" "$f" \
  || { echo "CHANGELOG.md: missing '## [$ver] - YYYY-MM-DD' heading" >&2; exit 1; }
[[ -n "$(section "$ver")" ]] \
  || { echo "CHANGELOG.md: [$ver] section is empty" >&2; exit 1; }
[[ -z "$(section Unreleased)" ]] \
  || { echo "CHANGELOG.md: [Unreleased] still has entries; move them under [$ver]" >&2; exit 1; }
echo "CHANGELOG.md ok for $ver"
