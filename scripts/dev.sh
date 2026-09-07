#!/usr/bin/env bash
#
# Hermod development stack.
#
#   ./scripts/dev.sh             start everything (Postgres + API/worker + UI)
#   ./scripts/dev.sh --sqlite    use SQLite instead of Postgres (no container needed)
#   ./scripts/dev.sh --reset     wipe the dev database and re-seed from scratch
#   ./scripts/dev.sh --build-ui  refresh the UI bundle the API serves, then start
#   ./scripts/dev.sh --detach    start, print the banner, then exit (for CI)
#   ./scripts/dev.sh --stop      stop a running stack and exit
#   ./scripts/dev.sh --print-ports  show the ports this run would use, then exit
#
# Ports are chosen, not assumed. It prefers 4005 (API), 50051 (gRPC) and 5175
# (UI), and steps up to the next free number for any of them that is taken, so
# a second stack or an unrelated service never produces "address already in
# use". Watch the banner for the numbers it settled on. Pin one with the
# environment variables below and it is used as given, or the run fails saying
# who holds it — a port you asked for by name is never silently swapped.
#
# The script completes Hermod's first-run wizard for you, so the only thing left
# to do is type a username and password on the login page.
#
# Work against the UI port (5175). It runs Vite, so edits to ui/src appear
# immediately via hot reload. The API port (4005) also serves a UI, but that one
# is a pre-built bundle in internal/api/static and will NOT show your edits until
# you run --build-ui; it exists for testing what production actually ships.
#
# It keeps its state out of your way: config lives in .dev/ inside the repo
# rather than ~/.hermod, so a dev run never overwrites the configuration of
# another Hermod instance you may have set up.
#
# The Postgres path uses Apple's `container` CLI (macOS 26+). No container
# runtime available? Use --sqlite, which needs nothing.
#
# Environment overrides:
#   HERMOD_DEV_API_PORT      pin the API port     (default: 4005, else next free)
#   HERMOD_DEV_GRPC_PORT     pin the gRPC port    (default: 50051, else next free)
#   HERMOD_DEV_UI_PORT       pin the UI port      (default: 5175, else next free)
#   HERMOD_DEV_PG_CONTAINER  container name (default: postgres-dev)
#   HERMOD_DEV_PG_PORT       host port for Postgres (default: auto-detected)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEV_DIR="$REPO_ROOT/.dev"
LOG_DIR="$DEV_DIR/logs"
BIN="$DEV_DIR/hermod"

# Preferred ports. These are only a starting point: whichever of them is busy
# gets stepped over at startup (see resolve_ports), so a second stack — or an
# unrelated service — never turns into "address already in use".
#
# The gRPC port matters more than it looks. 50051 is *the* conventional gRPC
# port, so it is the one most likely to be taken by something else on the
# machine, and a failed bind on it is fatal for the whole process
# (cmd/hermod/server_util.go:88) even though the API and UI ports were free.
API_PORT_PREF="${HERMOD_DEV_API_PORT:-4005}"
GRPC_PORT_PREF="${HERMOD_DEV_GRPC_PORT:-50051}"
UI_PORT_PREF="${HERMOD_DEV_UI_PORT:-5175}"

# A port named explicitly is a request, not a hint: pinning one and silently
# getting another is worse than being told it is taken.
API_PORT_PINNED="${HERMOD_DEV_API_PORT:+1}"
GRPC_PORT_PINNED="${HERMOD_DEV_GRPC_PORT:+1}"
UI_PORT_PINNED="${HERMOD_DEV_UI_PORT:+1}"

API_PORT="$API_PORT_PREF"
GRPC_PORT="$GRPC_PORT_PREF"
UI_PORT="$UI_PORT_PREF"

# The ports a running stack actually chose. --stop is a separate invocation and
# cannot re-derive them (they are free again only *after* the stack dies), so
# they are recorded here for it to sweep.
PORTS_FILE="$DEV_DIR/.ports"

ADMIN_USER="${HERMOD_DEV_ADMIN_USER:-admin}"
ADMIN_PASS="${HERMOD_DEV_ADMIN_PASS:-admin}"
ADMIN_EMAIL="${HERMOD_DEV_ADMIN_EMAIL:-admin@hermod.local}"

# Dev-only values. Both are required by the backend; neither is a secret worth
# protecting on a laptop, and both are confined to .dev/.
CRYPTO_KEY="${HERMOD_DEV_CRYPTO_KEY:-hermod-dev-master-key-change-me}"
export HERMOD_JWT_SECRET="${HERMOD_JWT_SECRET:-hermod-dev-jwt-secret}"

# Keep dev config away from ~/.hermod (see internal/config.ConfigDirEnv).
export HERMOD_CONFIG_DIR="$DEV_DIR/config"

PG_CONTAINER="${HERMOD_DEV_PG_CONTAINER:-postgres-dev}"
PG_DSN="postgres://postgres:postgres@localhost:5432/hermod_metadata?sslmode=disable"
SQLITE_PATH="$DEV_DIR/hermod.db"

USE_SQLITE=0
DO_RESET=0
DO_STOP=0
DO_BUILD_UI=0
DO_DETACH=0
DO_PRINT_PORTS=0
for arg in "$@"; do
  case "$arg" in
    --sqlite) USE_SQLITE=1 ;;
    --reset)  DO_RESET=1 ;;
    --stop)     DO_STOP=1 ;;
    --build-ui) DO_BUILD_UI=1 ;;
    --detach)   DO_DETACH=1 ;;
    --print-ports) DO_PRINT_PORTS=1 ;;
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

# --- port selection ------------------------------------------------------------

port_in_use() {
  local port="$1"
  if command -v lsof >/dev/null 2>&1; then
    lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1 && return 0
  fi
  # Fallback for a machine without lsof. A refused connection means free.
  (echo >"/dev/tcp/127.0.0.1/$port") >/dev/null 2>&1 && return 0
  return 1
}

port_holder() {
  local port="$1"
  command -v lsof >/dev/null 2>&1 || { echo "another process"; return; }
  local who
  who="$(lsof -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | awk 'NR==2 {print $1" (pid "$2")"}')"
  echo "${who:-another process}"
}

# First free port at or above the preferred one. Scanning upward keeps the
# result predictable — a busy 4005 becomes 4006, not a random high port — which
# matters because the number ends up in a URL a human has to type.
pick_free_port() {
  local preferred="$1" label="$2" pinned="${3:-}"
  if ! port_in_use "$preferred"; then
    echo "$preferred"; return
  fi
  if [[ -n "$pinned" ]]; then
    die "$label port $preferred is pinned but already in use by $(port_holder "$preferred"). Free it, or unset the override to auto-select."
  fi
  local port
  for port in $(seq $((preferred + 1)) $((preferred + 50))); do
    port_in_use "$port" || { echo "$port"; return; }
  done
  die "no free $label port found in ${preferred}-$((preferred + 50))"
}

# `die` inside the command substitution above exits only the subshell, so its
# failure must be propagated here explicitly — without the `|| exit`, a rejected
# port would leave the variable empty and the stack would start on nothing.
resolve_ports() {
  API_PORT="$(pick_free_port "$API_PORT_PREF" API "$API_PORT_PINNED")"   || exit 1
  GRPC_PORT="$(pick_free_port "$GRPC_PORT_PREF" gRPC "$GRPC_PORT_PINNED")" || exit 1
  UI_PORT="$(pick_free_port "$UI_PORT_PREF" UI "$UI_PORT_PINNED")"       || exit 1

  # Vite reads these, not the HERMOD_DEV_* names. Without the export the UI kept
  # binding its own hardcoded 5175 with strictPort — so setting HERMOD_DEV_UI_PORT
  # moved every check in this script but not the server it was checking, and the
  # override could never actually resolve a conflict.
  export HERMOD_UI_PORT="$UI_PORT"
  export HERMOD_API_TARGET="http://localhost:$API_PORT"
}

# --- container runtime (Apple container) ---------------------------------------
#
# Hermod's dev database runs under Apple's `container` CLI (macOS 26+).
# Note its differences from the Docker/Podman CLIs: `--format` accepts only
# json|table|yaml|toml (no Go templates), and a container's ID *is* its name,
# so names come from the first column of the table output.

require_container_cli() {
  command -v container >/dev/null 2>&1 \
    || die "the 'container' CLI was not found. Install Apple's container tool, or re-run with --sqlite"
  # Presence is not enough — the helper service must be running, and a stopped
  # one fails every call with an opaque error.
  container system status >/dev/null 2>&1 \
    || die "the container service is not running (try: container system start), or re-run with --sqlite"
}

rt_ls_running() { container ls 2>/dev/null | awk 'NR>1 {print $1}'; }
rt_ls_all()     { container ls -a 2>/dev/null | awk 'NR>1 {print $1}'; }
rt_start()      { container start "$1" >/dev/null; }
rt_exec()       { local name="$1"; shift; container exec "$name" "$@"; }

# Host port the Postgres container publishes. Apple container frequently maps
# to a non-default host port, so read it back rather than assuming 5432.
rt_pg_host_port() {
  local name="$1"
  if [[ -n "${HERMOD_DEV_PG_PORT:-}" ]]; then
    echo "$HERMOD_DEV_PG_PORT"; return
  fi
  if command -v python3 >/dev/null 2>&1; then
    local port
    port="$(container inspect "$name" 2>/dev/null | python3 -c '
import json,sys
try:
    d=json.load(sys.stdin)
    c=d[0] if isinstance(d,list) else d
    for p in c.get("configuration",{}).get("publishedPorts",[]) or []:
        if int(p.get("containerPort",0))==5432:
            print(p.get("hostPort","")); break
except Exception:
    pass' 2>/dev/null)"
    [[ -n "$port" ]] && { echo "$port"; return; }
  fi
  echo 5432
}

# --- shutdown -----------------------------------------------------------------

API_PID=""
UI_PID=""
TAIL_PID=""

# Every process this script starts carries one of these absolute paths on its
# command line. Matching on them reaches processes a port sweep cannot see, and
# because the paths are inside this checkout, another Hermod on the machine is
# left alone.
dev_signatures() {
  printf '%s\n' \
    "$BIN" \
    "$REPO_ROOT/ui/node_modules/.bin/vite" \
    "tail -f $LOG_DIR/"
}

# Signal every process matching a signature, skipping this script and its
# parent so the sweep cannot take itself down.
sweep_signatures() {
  local signal="$1" sig pid
  while IFS= read -r sig; do
    for pid in $(pgrep -f "$sig" 2>/dev/null || true); do
      [[ "$pid" == "$$" || "$pid" == "$PPID" ]] && continue
      kill "-$signal" "$pid" 2>/dev/null || true
    done
  done < <(dev_signatures)
}

stop_stack() {
  # Kill by recorded PID first, then sweep by port, then by command signature.
  # The signature sweep is the one that matters after a crash: a hermod whose
  # listener failed to bind keeps running while holding no port at all, which a
  # port-only sweep can never find. One such process survived a previous run for
  # nearly two hours, writing into the following run's log.
  for pid in "$API_PID" "$UI_PID" "$TAIL_PID"; do
    [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true
  done
  for port in "$API_PORT" "$GRPC_PORT" "$UI_PORT"; do
    local stale
    stale="$(lsof -ti tcp:"$port" 2>/dev/null || true)"
    [[ -n "$stale" ]] && kill $stale 2>/dev/null || true
  done

  sweep_signatures TERM
  # Give them a moment to close cleanly, then insist: anything that ignores
  # SIGTERM is inherited by the next run.
  sleep 1
  sweep_signatures KILL
  wait 2>/dev/null || true
}

on_exit() {
  echo
  say "Shutting down..."
  stop_stack
  ok "stopped"
}

if [[ "$DO_STOP" == "1" ]]; then
  say "Stopping any running dev stack"
  # Sweep the ports the stack actually chose, which are not the preferred ones
  # if it had to step over a conflict. The signature sweep would still reach the
  # processes; this just keeps the port sweep honest.
  if [[ -f "$PORTS_FILE" ]]; then
    # shellcheck disable=SC1090
    source "$PORTS_FILE" 2>/dev/null || true
  fi
  stop_stack
  rm -f "$PORTS_FILE" 2>/dev/null || true
  ok "done"
  exit 0
fi

if [[ "$DO_PRINT_PORTS" == "1" ]]; then
  resolve_ports
  echo "api=$API_PORT"
  echo "grpc=$GRPC_PORT"
  echo "ui=$UI_PORT"
  exit 0
fi

# --- preflight ----------------------------------------------------------------

say "Checking prerequisites"
command -v go   >/dev/null || die "go not found on PATH"
command -v bun  >/dev/null || die "bun not found on PATH (https://bun.sh)"
command -v curl >/dev/null || die "curl not found on PATH"
ok "go $(go env GOVERSION), bun $(bun --version)"

mkdir -p "$DEV_DIR" "$LOG_DIR" "$HERMOD_CONFIG_DIR"

say "Selecting ports"
resolve_ports
for _spec in "API:$API_PORT:$API_PORT_PREF" "gRPC:$GRPC_PORT:$GRPC_PORT_PREF" "UI:$UI_PORT:$UI_PORT_PREF"; do
  IFS=: read -r _label _got _want <<<"$_spec"
  if [[ "$_got" == "$_want" ]]; then
    ok "$_label :$_got"
  else
    warn "$_label :$_want was taken by $(port_holder "$_want") — using :$_got instead"
  fi
done
printf 'API_PORT=%s\nGRPC_PORT=%s\nUI_PORT=%s\n' "$API_PORT" "$GRPC_PORT" "$UI_PORT" > "$PORTS_FILE"

if [[ "$DO_RESET" == "1" ]]; then
  say "Resetting dev state"
  rm -f "$SQLITE_PATH"* 2>/dev/null || true
  rm -rf "${HERMOD_CONFIG_DIR:?}/"* 2>/dev/null || true
  ok "cleared .dev/ (databases and config)"
fi

# --- database -----------------------------------------------------------------

if [[ "$USE_SQLITE" == "1" ]]; then
  DB_TYPE="sqlite"
  DB_CONN="$SQLITE_PATH"
  say "Using SQLite at ${DIM}${SQLITE_PATH}${RESET}"
else
  DB_TYPE="postgres"
  DB_CONN="$PG_DSN"
  say "Preparing PostgreSQL"
  require_container_cli

  if ! rt_ls_running | grep -qx "$PG_CONTAINER"; then
    if rt_ls_all | grep -qx "$PG_CONTAINER"; then
      rt_start "$PG_CONTAINER"
      ok "started existing container '$PG_CONTAINER'"
    else
      # Nothing to configure by hand: build it, correctly, on first run.
      say "Container '$PG_CONTAINER' not found — creating it"
      HERMOD_DEV_PG_CONTAINER="$PG_CONTAINER" "$REPO_ROOT/scripts/create-postgres.sh" \
        || die "could not create the PostgreSQL container. See the output above."
    fi
  else
    ok "container '$PG_CONTAINER' already running"
  fi

  # Resolve the DSN only once the container is up, so the published port can be
  # read from it.
  PG_PORT="$(rt_pg_host_port "$PG_CONTAINER")"
  PG_DSN="postgres://postgres:postgres@localhost:${PG_PORT}/hermod_metadata?sslmode=disable"
  DB_CONN="$PG_DSN"
  [[ "$PG_PORT" != "5432" ]] && ok "Postgres published on host port $PG_PORT"

  for _ in $(seq 1 30); do
    rt_exec "$PG_CONTAINER" pg_isready -U postgres >/dev/null 2>&1 && break
    sleep 1
  done
  rt_exec "$PG_CONTAINER" pg_isready -U postgres >/dev/null 2>&1 \
    || die "Postgres did not become ready"

  # CDC needs logical decoding; warn rather than fail so non-CDC work still runs.
  if [[ "$(rt_exec "$PG_CONTAINER" psql -U postgres -tAc 'SHOW wal_level' 2>/dev/null || true)" != "logical" ]]; then
    warn "wal_level is not 'logical' — Postgres CDC sources will not work"
  fi

  for db in hermod_metadata hermod_test_source hermod_test_sink; do
    if ! rt_exec "$PG_CONTAINER" psql -U postgres -tAc \
        "SELECT 1 FROM pg_database WHERE datname='$db'" | grep -q 1; then
      rt_exec "$PG_CONTAINER" createdb -U postgres "$db"
      ok "created database $db"
    fi
  done
  ok "databases ready"

  if [[ "$DO_RESET" == "1" ]]; then
    rt_exec "$PG_CONTAINER" psql -U postgres -c 'DROP DATABASE IF EXISTS hermod_metadata' >/dev/null 2>&1 || true
    rt_exec "$PG_CONTAINER" createdb -U postgres hermod_metadata
    ok "recreated hermod_metadata"
  fi
fi

# Hermod persists its chosen database to db_config.yaml and prefers that over
# the CLI flags on later starts. Switching between --sqlite and Postgres would
# therefore keep using the previous database and fail to connect, so drop the
# stored config whenever the requested type differs from the recorded one.
DB_STAMP="$DEV_DIR/.db-type"
if [[ -f "$DB_STAMP" && "$(cat "$DB_STAMP")" != "$DB_TYPE" ]]; then
  warn "database type changed ($(cat "$DB_STAMP") → $DB_TYPE); resetting stored config"
  rm -f "$HERMOD_CONFIG_DIR/db_config.yaml" "$HERMOD_CONFIG_DIR/config.yaml" 2>/dev/null || true
fi
echo "$DB_TYPE" > "$DB_STAMP"

# --- build --------------------------------------------------------------------

say "Building backend"
(cd "$REPO_ROOT" && go build -o "$BIN" ./cmd/hermod) || die "go build failed"
ok "built $(basename "$BIN")"

if [[ ! -d "$REPO_ROOT/ui/node_modules" ]]; then
  say "Installing UI dependencies"
  (cd "$REPO_ROOT/ui" && bun install) || die "bun install failed"
  ok "dependencies installed"
fi

if [[ "$DO_BUILD_UI" == "1" ]]; then
  say "Rebuilding the UI bundle served by the API"
  # Use the binary's own --build-ui rather than `bun run build` directly: Vite
  # emits to ui/dist, but the API serves internal/api/static, and config.BuildUI
  # is what copies between the two. Running only the Vite build leaves the
  # served bundle untouched and silently stale.
  #
  # --mode=build-only is required: buildUIAndExit only calls os.Exit for that
  # mode, so without it the binary builds and then starts a second server.
  (cd "$REPO_ROOT" && "$BIN" --build-ui --mode=build-only) || die "UI build failed"
  ok "internal/api/static refreshed"
fi

# Free the ports before binding, so a stale run does not cause a confusing
# "address already in use" three steps later.
stop_stack

# From here on, Ctrl+C should tear the stack down.
trap on_exit EXIT INT TERM

# --- run ----------------------------------------------------------------------

say "Starting API + worker on :$API_PORT (gRPC :$GRPC_PORT)"
# HERMOD_ENV stays unset (development): the backend then serves from disk and
# does not try to use the embedded production bundle.
#
# --grpc-port is not optional here. The backend binds gRPC as well as HTTP, and
# a failed bind on either one takes the whole process down; left at its default
# it would keep dying on 50051 no matter which API port was chosen.
"$BIN" --mode=standalone --port="$API_PORT" --grpc-port="$GRPC_PORT" \
  --db-type="$DB_TYPE" --db-conn="$DB_CONN" \
  > "$LOG_DIR/backend.log" 2>&1 < /dev/null &
API_PID=$!

for _ in $(seq 1 60); do
  curl -sf -o /dev/null "http://localhost:$API_PORT/livez" 2>/dev/null && break
  curl -sf -o /dev/null "http://localhost:$API_PORT/setup" 2>/dev/null && break
  kill -0 "$API_PID" 2>/dev/null || die "backend exited early — see $LOG_DIR/backend.log"
  sleep 1
done
kill -0 "$API_PID" 2>/dev/null || die "backend exited early — see $LOG_DIR/backend.log"
ok "API up (pid $API_PID)"

# --- first-run setup ----------------------------------------------------------

say "Checking first-run setup"
# The endpoint answers 401 once configured, which makes this safely repeatable.
SETUP_CODE="$(curl -s -o "$LOG_DIR/setup.json" -w '%{http_code}' \
  -X POST "http://localhost:$API_PORT/api/config/setup" \
  -H 'Content-Type: application/json' \
  -d "{\"db\":{\"type\":\"$DB_TYPE\",\"conn\":\"$DB_CONN\",\"crypto_master_key\":\"$CRYPTO_KEY\"},
       \"admin\":{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\",\"full_name\":\"Developer\",\"email\":\"$ADMIN_EMAIL\"}}" \
  2>/dev/null || echo 000)"

case "$SETUP_CODE" in
  200) ok "created admin user '$ADMIN_USER'" ;;
  401) ok "already configured — existing admin kept" ;;
  *)   warn "setup returned HTTP $SETUP_CODE — see $LOG_DIR/setup.json"
       warn "you may need to finish setup at http://localhost:$UI_PORT/setup" ;;
esac

# --- ui -----------------------------------------------------------------------

say "Starting UI on :$UI_PORT"
(cd "$REPO_ROOT/ui" && bun run dev > "$LOG_DIR/ui.log" 2>&1 < /dev/null) &
UI_PID=$!

for _ in $(seq 1 60); do
  curl -sf -o /dev/null "http://localhost:$UI_PORT" 2>/dev/null && break
  kill -0 "$UI_PID" 2>/dev/null || die "UI exited early — see $LOG_DIR/ui.log"
  sleep 1
done
ok "UI up (pid $UI_PID)"

# --- ready --------------------------------------------------------------------

cat <<BANNER

  ${GREEN}${BOLD}Hermod is running.${RESET}

    ${BOLD}Open this${RESET}  ${BOLD}http://localhost:$UI_PORT${RESET}  ${DIM}← UI, hot-reloads your edits${RESET}
    API        http://localhost:$API_PORT  ${DIM}← API only. It also serves a
               pre-built UI bundle that does NOT reflect your edits; use $UI_PORT.${RESET}
    gRPC       localhost:$GRPC_PORT
    Database   $DB_TYPE

    ${BOLD}Log in with${RESET}
      username  ${BOLD}$ADMIN_USER${RESET}
      password  ${BOLD}$ADMIN_PASS${RESET}

    ${DIM}logs    $LOG_DIR/{backend,ui}.log
    config  $HERMOD_CONFIG_DIR (isolated from ~/.hermod)
    stop    Ctrl+C, or ./scripts/dev.sh --stop${RESET}

BANNER

if [[ "$DO_DETACH" == "1" ]]; then
  # CI starts the stack, runs tests against it, then stops it explicitly.
  # Clear the teardown trap so the processes survive this script exiting, and
  # disown them so the launching shell is not held open by their job entries.
  #
  # disown ONLY here: doing it unconditionally breaks the foreground path,
  # because `wait` cannot wait on a job that is no longer in the job table —
  # it returns immediately and the stack is torn down the moment it starts.
  trap - EXIT INT TERM
  disown "$API_PID" 2>/dev/null || true
  disown "$UI_PID" 2>/dev/null || true
  say "Detached; stop with ./scripts/dev.sh --stop"
  exit 0
fi

# Surface backend logs while the stack runs; exiting this tail tears it all down.
# Recorded so teardown reaps it: an unrecorded `tail -f` outlives the script and
# accumulates one stray process per run.
tail -f "$LOG_DIR/backend.log" &
TAIL_PID=$!
wait "$API_PID" "$UI_PID" 2>/dev/null || true
