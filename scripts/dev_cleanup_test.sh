#!/usr/bin/env bash
#
# Regression test for `dev.sh --stop`.
#
# A dev run once left a hermod process alive for nearly two hours. It had lost
# its listener, so the port sweep in stop_stack could not see it, but it was
# still running its worker loop and still holding fd 1 on .dev/logs/backend.log
# — so it kept writing SQLite errors into the *next* run's log, which read as a
# database misconfiguration that did not exist. Five `tail -f` processes had
# accumulated the same way.
#
# Cleanup therefore has to match on what a process *is*, not only on what port
# it happens to hold.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEV_DIR="$REPO_ROOT/.dev"
LOG_DIR="$DEV_DIR/logs"
BIN="$DEV_DIR/hermod"

mkdir -p "$LOG_DIR"
touch "$LOG_DIR/backend.log"

if [[ -t 1 ]]; then
  GREEN=$'\033[32m'; RED=$'\033[31m'; RESET=$'\033[0m'
else
  GREEN=""; RED=""; RESET=""
fi

FAILURES=0
pass() { echo "  ${GREEN}✓${RESET} $*"; }
fail() { echo "  ${RED}✗${RESET} $*" >&2; FAILURES=$((FAILURES + 1)); }

# A stand-in for a hermod that is running but holds no port. Named so its
# command line carries the dev binary path, which is the only thing that
# distinguishes this checkout's processes from any other Hermod on the machine.
DECOY="$DEV_DIR/hermod-cleanup-decoy"
ln -sf /bin/sleep "$DECOY"

cleanup() {
  [[ -n "${DECOY_PID:-}" ]] && kill -9 "$DECOY_PID" 2>/dev/null || true
  [[ -n "${TAIL_PID:-}" ]] && kill -9 "$TAIL_PID" 2>/dev/null || true
  rm -f "$DECOY"
}
trap cleanup EXIT

"$DECOY" 300 &
DECOY_PID=$!

tail -f "$LOG_DIR/backend.log" >/dev/null 2>&1 &
TAIL_PID=$!

# Make sure both are actually up before asking anything to kill them, or the
# test passes for the wrong reason.
sleep 0.5
kill -0 "$DECOY_PID" 2>/dev/null || { echo "setup failed: decoy did not start" >&2; exit 1; }
kill -0 "$TAIL_PID" 2>/dev/null || { echo "setup failed: tail did not start" >&2; exit 1; }

echo "▸ Running ./scripts/dev.sh --stop"
"$REPO_ROOT/scripts/dev.sh" --stop >/dev/null 2>&1 || true
sleep 1

if kill -0 "$DECOY_PID" 2>/dev/null; then
  fail "a portless dev process survived --stop (pid $DECOY_PID)"
else
  pass "portless dev process reaped"
fi

if kill -0 "$TAIL_PID" 2>/dev/null; then
  fail "the log tail survived --stop (pid $TAIL_PID)"
else
  pass "log tail reaped"
fi

# The sweep must stay inside this checkout. A hermod installed elsewhere on the
# machine has a different path and must not be touched.
OUTSIDER="$(mktemp -d)/hermod"
ln -sf /bin/sleep "$OUTSIDER"
"$OUTSIDER" 300 &
OUTSIDER_PID=$!
sleep 0.3
"$REPO_ROOT/scripts/dev.sh" --stop >/dev/null 2>&1 || true
sleep 0.5
if kill -0 "$OUTSIDER_PID" 2>/dev/null; then
  pass "a hermod outside this checkout was left alone"
else
  fail "--stop killed a hermod outside this checkout"
fi
kill -9 "$OUTSIDER_PID" 2>/dev/null || true
rm -rf "$(dirname "$OUTSIDER")"

if [[ "$FAILURES" -gt 0 ]]; then
  echo "${RED}$FAILURES check(s) failed${RESET}" >&2
  exit 1
fi
echo "${GREEN}dev.sh cleanup OK${RESET}"
