#!/usr/bin/env bash
#
# Verifies scripts/govulncheck.sh fails closed. The gate used to discard
# govulncheck's exit status, so a scan that was OOM-killed or could not reach
# the vulnerability database printed "no unexempted reachable
# vulnerabilities" and passed.
#
#   ./scripts/govulncheck_test.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECK="$REPO_ROOT/scripts/govulncheck.sh"

if [[ -t 1 ]]; then
  GREEN=$'\033[32m'; RED=$'\033[31m'; RESET=$'\033[0m'
else
  GREEN=""; RED=""; RESET=""
fi

FAILURES=0
pass() { echo "  ${GREEN}✓${RESET} $*"; }
fail() { echo "  ${RED}✗${RESET} $*" >&2; FAILURES=$((FAILURES + 1)); }

FAKE_BIN="$(mktemp -d)"
trap 'rm -rf "$FAKE_BIN"' EXIT

# A stand-in govulncheck that prints $FAKE_OUTPUT and exits $FAKE_EXIT.
cat > "$FAKE_BIN/govulncheck" <<'EOF'
#!/usr/bin/env bash
printf '%s' "${FAKE_OUTPUT:-}"
exit "${FAKE_EXIT:-0}"
EOF
chmod +x "$FAKE_BIN/govulncheck"

# finding <osv-id> — one symbol-level (reachable) finding as govulncheck emits it.
finding() {
  printf '{\n  "finding": {\n    "osv": "%s",\n    "trace": [\n      {\n        "module": "example.com/m",\n        "function": "F"\n      }\n    ]\n  }\n}\n' "$1"
}

# run <expected-exit> <description> <fake-exit> <fake-output>
run() {
  local want="$1" desc="$2" code="$3" output="$4"
  local out status
  out="$(PATH="$FAKE_BIN:$PATH" FAKE_EXIT="$code" FAKE_OUTPUT="$output" "$CHECK" 2>&1)" && status=0 || status=$?
  if [[ "$status" -eq "$want" ]]; then
    pass "$desc"
  else
    fail "$desc (exit $status, wanted $want)"
    echo "      ${out//$'\n'/$'\n      '}" >&2
  fi
}

echo "govulncheck.sh"

run 0 "a clean scan passes" 0 '{"config": {}}'

run 0 "only exempted findings pass" 0 "$(finding GO-2026-5046)"

run 1 "an unexempted reachable finding fails" 0 "$(finding GO-2026-6665)"

# The shape of a scan the OOM killer took: SIGKILL after whole objects had
# been written but before any finding, so what was printed still parses.
run 1 "a killed scan fails rather than passing" 137 '{"config": {"protocol_version": "v1.0.0"}}'

# The shape of a scan that could not fetch the database: no output at all.
run 1 "a scan that errors with no output fails" 1 ''

# Output truncated without a non-zero exit is still not a scan anyone can trust.
run 1 "unparseable output fails" 0 '{"finding": {"osv": "GO-2026-6665"'

if [[ "$FAILURES" -gt 0 ]]; then
  echo "${RED}$FAILURES failure(s)${RESET}" >&2
  exit 1
fi
echo "${GREEN}all passed${RESET}"
