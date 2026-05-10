#!/usr/bin/env bash
# Verifies that every exported identifier of the mcpx public API surface
# (root package + log/zaplog + log/sloglog) is mentioned at least once in a
# *_test.go file. Catches regressions where new public symbols ship without
# tests (see SC-001).
#
# Usage: bash .github/scripts/check_api_coverage.sh

set -euo pipefail

PACKAGES=(
  "."
  "./log/zaplog"
  "./log/sloglog"
)

# Extract exported identifiers from `go doc -all <pkg>`. The format is well
# known: top-level "func Name", "type Name", "func (recv) Method", "var Name",
# "const Name". We pull the bare name from each.
extract_symbols() {
  local pkg="$1"
  go doc -all "$pkg" 2>/dev/null \
    | awk '
        # func Name(...) — top-level function
        /^func [A-Z][A-Za-z0-9_]*\(/ { sub(/^func /, ""); sub(/\(.*/, ""); print; next }
        # func (recv) Method(...) — method on a receiver
        /^func \([^)]+\) [A-Z][A-Za-z0-9_]*\(/ {
            sub(/^func \([^)]+\) /, "")
            sub(/\(.*/, "")
            print
            next
        }
        # type Name ...
        /^type [A-Z][A-Za-z0-9_]*[ \t]/ { sub(/^type /, ""); sub(/[ \t].*/, ""); print; next }
        # var Name ... / const Name ...
        /^(var|const) [A-Z][A-Za-z0-9_]*[ \t]/ {
            sub(/^(var|const) /, "")
            sub(/[ \t].*/, "")
            print
            next
        }
      '
}

missing=0
seen=0

for pkg in "${PACKAGES[@]}"; do
  while IFS= read -r sym; do
    [[ -z "$sym" ]] && continue
    seen=$((seen + 1))
    if ! grep -rqE "\\b${sym}\\b" --include='*_test.go' .; then
      echo "MISSING: ${pkg} ${sym}"
      missing=$((missing + 1))
    fi
  done < <(extract_symbols "$pkg" | sort -u)
done

if (( missing > 0 )); then
  echo
  echo "Public API coverage check FAILED: ${missing} symbols not mentioned in any *_test.go" >&2
  exit 1
fi

echo "Public API coverage OK: ${seen} symbols, all referenced in tests."
