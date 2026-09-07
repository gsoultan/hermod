#!/usr/bin/env bash
#
# Verifies scripts/dev.sh picks ports that are actually free.
#
# The bug this pins down: dev.sh managed only the API and UI ports, but the
# backend also binds gRPC on :50051 — the canonical gRPC port, so the one most
# likely to be occupied by something else on the machine — and a failed bind on
# *any* of the three is fatal (cmd/hermod/server_util.go:88). The result was
# "address already in use" on a stack whose two documented ports were free.
#
#   ./scripts/dev_ports_test.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -t 1 ]]; then
  GREEN=$'\033[32m'; RED=$'\033[31m'; RESET=$'\033[0m'
else
  GREEN=""; RED=""; RESET=""
fi

FAILURES=0
pass() { echo "  ${GREEN}✓${RESET} $*"; }
fail() { echo "  ${RED}✗${RESET} $*" >&2; FAILURES=$((FAILURES + 1)); }

DECOY_PIDS=()
cleanup() {
  local pid
  for pid in "${DECOY_PIDS[@]:-}"; do
    [[ -n "$pid" ]] && kill -9 "$pid" 2>/dev/null || true
  done
}
trap cleanup EXIT

# Hold a port open for the duration of the test.
occupy() {
  local port="$1"
  # nc keeps the listener open; -l without -k serves one connection, which is
  # enough because nothing ever connects.
  nc -l 127.0.0.1 "$port" >/dev/null 2>&1 &
  DECOY_PIDS+=($!)
}

ports_json() { "$REPO_ROOT/scripts/dev.sh" --print-ports 2>/dev/null; }
field() { ports_json | awk -v k="$1" -F= '$1==k {print $2}'; }

echo "▸ Defaults are used when nothing is in the way"
API="$(field api)"; GRPC="$(field grpc)"; UI="$(field ui)"
[[ "$API" == "4005" ]]  && pass "api=4005"   || fail "api=$API, want 4005"
[[ "$GRPC" == "50051" ]] && pass "grpc=50051" || fail "grpc=$GRPC, want 50051"
[[ "$UI" == "5175" ]]   && pass "ui=5175"    || fail "ui=$UI, want 5175"

echo "▸ Occupied ports are stepped over"
occupy 4005
occupy 50051
occupy 5175
sleep 0.5

API="$(field api)"; GRPC="$(field grpc)"; UI="$(field ui)"
[[ -n "$API" && "$API" != "4005" ]]   && pass "api moved to $API"   || fail "api stayed on $API"
[[ -n "$GRPC" && "$GRPC" != "50051" ]] && pass "grpc moved to $GRPC" || fail "grpc stayed on $GRPC"
[[ -n "$UI" && "$UI" != "5175" ]]     && pass "ui moved to $UI"     || fail "ui stayed on $UI"

echo "▸ An explicitly pinned port is honoured, not silently moved"
PINNED="$(HERMOD_DEV_API_PORT=4321 "$REPO_ROOT/scripts/dev.sh" --print-ports 2>/dev/null \
  | awk -F= '$1=="api" {print $2}')"
[[ "$PINNED" == "4321" ]] && pass "api pinned to 4321" || fail "api=$PINNED, want 4321"

echo "▸ A pinned port that is busy fails loudly instead of drifting"
if HERMOD_DEV_API_PORT=4005 "$REPO_ROOT/scripts/dev.sh" --print-ports >/dev/null 2>&1; then
  fail "a busy pinned port was accepted"
else
  pass "a busy pinned port is rejected"
fi

if [[ "$FAILURES" -gt 0 ]]; then
  echo "${RED}$FAILURES check(s) failed${RESET}" >&2
  exit 1
fi
echo "${GREEN}dev.sh port selection OK${RESET}"
