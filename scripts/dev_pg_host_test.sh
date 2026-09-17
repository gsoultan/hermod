#!/usr/bin/env bash
#
# Regression test for the database address `dev.sh` hands the backend.
#
# A dev run once failed at the login page with "error" and nothing else. The
# banner was green, the container was healthy, and `pg_isready` passed — but
# the stack had never been set up. The DSN said `localhost:5432`, and on that
# machine a Homebrew postgresql@17 held 127.0.0.1:5432 while the container
# published on *:5432. Loopback wins, so every connection went to the native
# server, which has no `postgres` role. First-run setup failed with
# `FATAL: role "postgres" does not exist`, no admin user was ever created, and
# the only trace was a JSON file nobody reads.
#
# `pg_isready` could not catch it: it runs *inside* the container, so it
# answers for a server the backend may never reach. The check has to be made
# from the host, over the DSN itself, and it has to prove identity — that the
# server answering is this container and not a bystander that merely owns the
# port.
#
#   ./scripts/dev_pg_host_test.sh
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PG_CONTAINER="${HERMOD_DEV_PG_CONTAINER:-postgres-dev}"

if [[ -t 1 ]]; then
  GREEN=$'\033[32m'; RED=$'\033[31m'; YELLOW=$'\033[33m'; RESET=$'\033[0m'
else
  GREEN=""; RED=""; YELLOW=""; RESET=""
fi

FAILURES=0
pass() { echo "  ${GREEN}✓${RESET} $*"; }
fail() { echo "  ${RED}✗${RESET} $*" >&2; FAILURES=$((FAILURES + 1)); }
skip() { echo "  ${YELLOW}—${RESET} skipped: $*"; exit 0; }

# This one needs the real container and a host-side client; there is nothing
# meaningful to assert without them.
command -v container >/dev/null 2>&1 || skip "no 'container' CLI"
container system status >/dev/null 2>&1 || skip "container service not running"
container ls 2>/dev/null | awk 'NR>1 {print $1}' | grep -qx "$PG_CONTAINER" \
  || skip "container '$PG_CONTAINER' is not running"
command -v psql >/dev/null 2>&1 || skip "no host psql to connect with"

export PGCONNECT_TIMEOUT=5

echo "▸ The DSN dev.sh would use reaches '$PG_CONTAINER', not whoever owns the port"

DSN="$("$REPO_ROOT/scripts/dev.sh" --print-dsn 2>&1)" \
  || { fail "--print-dsn failed: $DSN"; exit 1; }
echo "    dsn: $DSN"

# A server's system identifier is generated at initdb and is unique per
# cluster, so matching it proves the host reached *this* container rather than
# some other Postgres that happens to have a 'postgres' role.
WANT="$(container exec "$PG_CONTAINER" psql -U postgres -tAc \
  'SELECT system_identifier FROM pg_control_system()' 2>/dev/null | tr -d '[:space:]')"
[[ -n "$WANT" ]] || { fail "could not read the container's system identifier"; exit 1; }

if GOT="$(psql "$DSN" -tAc 'SELECT system_identifier FROM pg_control_system()' 2>&1 | tr -d '[:space:]')"; then
  if [[ "$GOT" == "$WANT" ]]; then
    pass "connects to the container (system_identifier $WANT)"
  else
    fail "DSN reached a different Postgres: got $GOT, want $WANT"
  fi
else
  fail "DSN did not connect: $GOT"
fi

echo "▸ A host named explicitly is used as given"
PINNED="$(HERMOD_DEV_PG_HOST=198.51.100.7 "$REPO_ROOT/scripts/dev.sh" --print-dsn 2>&1 || true)"
if [[ "$PINNED" == *"@198.51.100.7:"* ]]; then
  pass "HERMOD_DEV_PG_HOST is honoured"
else
  fail "HERMOD_DEV_PG_HOST ignored — got: $PINNED"
fi

if [[ "$FAILURES" -gt 0 ]]; then
  echo "${RED}$FAILURES check(s) failed${RESET}" >&2
  exit 1
fi
echo "${GREEN}all checks passed${RESET}"
