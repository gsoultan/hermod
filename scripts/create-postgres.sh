#!/usr/bin/env bash
#
# Create the Hermod development PostgreSQL container using Apple's `container`
# CLI (macOS 26+).
#
#   ./scripts/create-postgres.sh              create it (no-op if it exists)
#   ./scripts/create-postgres.sh --recreate   delete and rebuild from scratch
#
# The container is configured for Change Data Capture: `wal_level=logical` plus
# replication slots and WAL senders, which Hermod's PostgreSQL CDC source
# requires. Without those a CDC workflow starts but never receives a change.
#
# Environment overrides:
#   HERMOD_DEV_PG_CONTAINER  container name        (default: postgres-dev)
#   HERMOD_DEV_PG_PORT       host port             (default: 5432)
#   HERMOD_DEV_PG_IMAGE      image                 (default: arm64v8/postgres:18-alpine)
#   HERMOD_DEV_PG_PASSWORD   postgres password     (default: postgres)
#
# The image is the arm64v8 build rather than the multi-arch `library/postgres`,
# so Apple Silicon runs a native ARM64 binary with no architecture negotiation.
# Override with HERMOD_DEV_PG_IMAGE on other hardware.
#
# Data lives inside the container's own filesystem and survives `container
# stop`/`start`. It does NOT survive `container delete` — use --recreate
# deliberately. PGDATA is intentionally not bind-mounted: PostgreSQL requires
# strict ownership on that directory, which does not survive the macOS→Linux
# filesystem boundary reliably.

set -euo pipefail

NAME="${HERMOD_DEV_PG_CONTAINER:-postgres-dev}"
PORT="${HERMOD_DEV_PG_PORT:-5432}"
IMAGE="${HERMOD_DEV_PG_IMAGE:-docker.io/arm64v8/postgres:18-alpine}"
PASSWORD="${HERMOD_DEV_PG_PASSWORD:-postgres}"

DATABASES=(hermod_metadata hermod_test_source hermod_test_sink)

RECREATE=0
for arg in "$@"; do
  case "$arg" in
    --recreate) RECREATE=1 ;;
    -h|--help) awk 'NR>1 && /^#/ {sub(/^# ?/, ""); print; next} NR>1 {exit}' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) echo "unknown option: $arg (try --help)" >&2; exit 2 ;;
  esac
done

if [[ -t 1 ]]; then
  BOLD=$'\033[1m'; GREEN=$'\033[32m'; YELLOW=$'\033[33m'; RED=$'\033[31m'; DIM=$'\033[2m'; RESET=$'\033[0m'
else
  BOLD=""; GREEN=""; YELLOW=""; RED=""; DIM=""; RESET=""
fi
say()  { echo "${BOLD}▸${RESET} $*"; }
ok()   { echo "  ${GREEN}✓${RESET} $*"; }
warn() { echo "  ${YELLOW}!${RESET} $*"; }
die()  { echo "  ${RED}✗${RESET} $*" >&2; exit 1; }

# --- preflight ----------------------------------------------------------------

command -v container >/dev/null 2>&1 \
  || die "the 'container' CLI was not found. Install Apple's container tool (macOS 26+)."
# Presence is not enough: a stopped helper service fails every call with an
# opaque error, so check it explicitly and say how to fix it.
container system status >/dev/null 2>&1 \
  || die "the container service is not running. Start it with: container system start"

ls_all() { container ls -a 2>/dev/null | awk 'NR>1 {print $1}'; }
ls_running() { container ls 2>/dev/null | awk 'NR>1 {print $1}'; }

# `container run` reports a port clash as
# "failed to bootstrap container ... Address already in use (errno: 48)",
# which gives no hint about which port or what is holding it. Check first and
# name the culprit.
port_in_use() {
  local port="$1"
  if command -v lsof >/dev/null 2>&1; then
    lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1 && return 0
  fi
  (echo >"/dev/tcp/127.0.0.1/$port") >/dev/null 2>&1 && return 0
  return 1
}

port_holder() {
  local port="$1"
  # Another container publishing the same port is the common case.
  local c
  for c in $(ls_all); do
    if container inspect "$c" 2>/dev/null | grep -q "\"hostPort\":$port,"; then
      echo "container '$c'"; return
    fi
  done
  if command -v lsof >/dev/null 2>&1; then
    local proc
    proc="$(lsof -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | awk 'NR==2 {print $1" (pid "$2")"}')"
    [[ -n "$proc" ]] && { echo "$proc"; return; }
  fi
  echo "an unknown process"
}

# --- recreate / existing ------------------------------------------------------

if ls_all | grep -qx "$NAME"; then
  if [[ "$RECREATE" == "1" ]]; then
    say "Removing existing container '$NAME'"
    container stop "$NAME" >/dev/null 2>&1 || true
    container delete "$NAME" >/dev/null 2>&1 || die "could not delete '$NAME'"
    ok "removed (its data is gone)"
  else
    if ls_running | grep -qx "$NAME"; then
      ok "container '$NAME' already exists and is running"
    else
      say "Starting existing container '$NAME'"
      container start "$NAME" >/dev/null || die "could not start '$NAME'"
      ok "started"
    fi
    # Fall through: still ensure the databases exist.
    SKIP_CREATE=1
  fi
fi

# --- create -------------------------------------------------------------------

if [[ "${SKIP_CREATE:-0}" != "1" ]]; then
  if port_in_use "$PORT"; then
    die "host port $PORT is already in use by $(port_holder "$PORT"). Free it, or set HERMOD_DEV_PG_PORT to another port."
  fi
  say "Creating PostgreSQL container '$NAME'"
  echo "  ${DIM}image ${IMAGE} · host port ${PORT} · wal_level=logical${RESET}"
  # The trailing flags are passed to postgres itself, not to `container run`.
  container run -d \
    --name "$NAME" \
    -p "${PORT}:5432" \
    -e POSTGRES_USER=postgres \
    -e POSTGRES_PASSWORD="$PASSWORD" \
    -e POSTGRES_DB=postgres \
    "$IMAGE" \
    -c wal_level=logical \
    -c max_replication_slots=10 \
    -c max_wal_senders=10 \
    -c max_prepared_transactions=32 \
    -c listen_addresses='*' \
    >/dev/null || die "failed to create container '$NAME'"
  ok "created"
fi

# --- readiness ----------------------------------------------------------------

say "Waiting for PostgreSQL to accept connections"
ready=0
for _ in $(seq 1 60); do
  if container exec "$NAME" pg_isready -U postgres >/dev/null 2>&1; then
    ready=1; break
  fi
  sleep 1
done
[[ "$ready" == "1" ]] || die "PostgreSQL did not become ready. Check: container logs $NAME"
ok "accepting connections"

# CDC silently does nothing without logical decoding, so verify rather than assume.
wal="$(container exec "$NAME" psql -U postgres -tAc 'SHOW wal_level' 2>/dev/null || true)"
if [[ "$wal" == "logical" ]]; then
  ok "wal_level=logical (CDC ready)"
else
  warn "wal_level is '$wal', not 'logical' — PostgreSQL CDC sources will not work"
fi

# Transactional sink groups need PREPARE TRANSACTION, which PostgreSQL disables
# by default (max_prepared_transactions = 0) and which cannot be turned on
# without a restart. Verify rather than assume: without it, 2PC fails at the
# first batch rather than at start-up.
prep="$(container exec "$NAME" psql -U postgres -tAc 'SHOW max_prepared_transactions' 2>/dev/null || true)"
if [[ "${prep:-0}" -gt 0 ]]; then
  ok "max_prepared_transactions=$prep (2PC ready)"
else
  warn "max_prepared_transactions is 0 — transactional sink groups will refuse to start"
fi

# --- databases ----------------------------------------------------------------

say "Ensuring Hermod databases exist"
for db in "${DATABASES[@]}"; do
  if container exec "$NAME" psql -U postgres -tAc \
      "SELECT 1 FROM pg_database WHERE datname='$db'" 2>/dev/null | grep -q 1; then
    ok "$db (already present)"
  else
    container exec "$NAME" createdb -U postgres "$db" \
      || die "could not create database $db"
    ok "$db (created)"
  fi
done

cat <<BANNER

  ${GREEN}${BOLD}PostgreSQL is ready.${RESET}

    container  ${BOLD}${NAME}${RESET}
    DSN        postgres://postgres:${PASSWORD}@localhost:${PORT}/hermod_metadata?sslmode=disable
    databases  ${DATABASES[*]}

    ${DIM}psql     container exec -it ${NAME} psql -U postgres
    logs     container logs ${NAME}
    stop     container stop ${NAME}    (data is preserved)
    destroy  ./scripts/create-postgres.sh --recreate${RESET}

BANNER
