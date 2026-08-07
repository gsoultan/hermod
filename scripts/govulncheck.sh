#!/usr/bin/env bash
#
# Vulnerability gate.
#
# Plain `govulncheck ./...` is the right check, but it has no way to record a
# reviewed, justified exemption. Without one, a single unfixable advisory in a
# dependency leaves the gate permanently red — and a gate that is always red is
# a gate everyone learns to ignore, which is strictly worse than no gate. This
# wrapper keeps it sharp: known-and-accepted advisories are listed below with
# the reason they are accepted, and *anything else* fails the build.
#
# An entry here is a claim that has to stay true. Each one states why the
# vulnerable code path is unreachable from Hermod, and where the test that
# holds it that way lives. Re-check them whenever the dependency's usage
# changes.
#
#   ./scripts/govulncheck.sh          # gate (exempted advisories allowed)
#   ./scripts/govulncheck.sh --all    # report everything, exempt nothing
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# --- Accepted advisories -------------------------------------------------------
#
# GO-2026-5046 / GO-2026-5047 / GO-2026-5048 — github.com/hamba/avro/v2
#   All three are decoder-path denial of service: a hostile Avro *stream*
#   declares an enormous array/map block count, and the decoder either spins on
#   it or allocates until the process dies. All require untrusted Avro input to
#   be decoded, and all are unfixed upstream (v2.31.0 is the latest release;
#   only a third-party fork carries the patch).
#
#   Hermod never decodes Avro. Its entire use of the library is
#   pkg/infra/schema/validators.go: avro.Parse on a schema string and
#   avro.Marshal to *encode* a map for validation. There is no reader, no
#   decoder and no stream. TestSchemaPackageNeverDecodesAvro in
#   pkg/infra/schema fails the build if a decode path is ever introduced, so
#   this exemption cannot silently stop being true.
#
declare -a EXEMPT=(
  "GO-2026-5046"
  "GO-2026-5047"
  "GO-2026-5048"
)

MODE="${1:-gate}"

json="$(govulncheck -format json ./... 2>/dev/null || true)"

# govulncheck emits a stream of pretty-printed JSON objects rather than JSONL,
# so walk it with raw_decode instead of reading line by line.
found="$(printf '%s' "$json" | python3 -c '
import sys, json
dec = json.JSONDecoder()
data = sys.stdin.read()
ids, i, n = set(), 0, len(data)
while i < n:
    while i < n and data[i].isspace():
        i += 1
    if i >= n:
        break
    obj, i = dec.raw_decode(data, i)
    f = obj.get("finding")
    # A finding without a trace is a module-level advisory the code does not
    # call; only symbol-level findings mean the vulnerable code is reachable.
    if f and f.get("osv") and f.get("trace") and f["trace"][0].get("function"):
        ids.add(f["osv"])
print("\n".join(sorted(ids)))
')"

if [ "$MODE" = "--all" ]; then
  echo "All reachable advisories:"
  echo "${found:-  (none)}"
  exit 0
fi

unexpected=""
while IFS= read -r id; do
  [ -z "$id" ] && continue
  skip=""
  for e in "${EXEMPT[@]}"; do
    [ "$id" = "$e" ] && skip=1 && break
  done
  [ -n "$skip" ] || unexpected="${unexpected}${id}"$'\n'
done <<< "$found"

if [ -n "$unexpected" ]; then
  echo "FAIL: reachable vulnerabilities with no recorded exemption:" >&2
  echo "$unexpected" >&2
  echo "Run 'govulncheck ./...' for detail. Fix them, or add an exemption to" >&2
  echo "scripts/govulncheck.sh with the reason the path is unreachable." >&2
  exit 1
fi

echo "govulncheck: no unexempted reachable vulnerabilities."
if [ -n "$found" ]; then
  echo "Accepted (see scripts/govulncheck.sh for justification):"
  echo "$found" | sed 's/^/  /'
fi
