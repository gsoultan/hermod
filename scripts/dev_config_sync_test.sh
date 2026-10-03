#!/usr/bin/env bash
#
# Regression test for what `dev.sh` does to the stored config between runs.
#
# Switching from --sqlite to Postgres once stopped a dev run at "Checking
# first-run setup" with HTTP 500: `duplicate key value violates unique
# constraint "users_username_key"`. On a type switch dev.sh deleted
# db_config.yaml outright, jwt_secret and crypto_master_key included, so the
# backend came up unconfigured and the script re-ran first-run setup against a
# Postgres database that already had its admin. The data was fine; the config
# that said so was gone.
#
# A type switch only changes *which* database, so only the lines naming it
# should change.
#
#   ./scripts/dev_config_sync_test.sh
#
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

SCRATCH="$(mktemp -d)"
trap 'rm -rf "$SCRATCH"' EXIT

# A scratch dev dir as a completed Postgres run leaves it.
seed() {
  local dir="$1"
  rm -rf "$dir"
  mkdir -p "$dir/config"
  echo "postgres" > "$dir/.db-type"
  cat > "$dir/config/db_config.yaml" <<'YAML'
type: postgres
conn: postgres://postgres:postgres@192.0.2.10:5432/hermod_metadata?sslmode=disable
log_type: ""
log_conn: ""
jwt_secret: kept-jwt-secret
crypto_master_key: kept-master-key
YAML
}

sync() {
  HERMOD_DEV_DIR="$1" "$REPO_ROOT/scripts/dev.sh" "${@:2}" --sync-config
}

field() { sed -n "s/^$2: //p" "$1/config/db_config.yaml" 2>/dev/null; }

echo "▸ A database type switch repoints the stored config instead of deleting it"
DIR="$SCRATCH/switch"
seed "$DIR"
sync "$DIR" --sqlite >/dev/null
if [[ ! -f "$DIR/config/db_config.yaml" ]]; then
  fail "db_config.yaml was deleted — the backend would rerun first-run setup"
else
  [[ "$(field "$DIR" type)" == "sqlite" ]] \
    && pass "type is sqlite" || fail "type is '$(field "$DIR" type)', want sqlite"
  [[ "$(field "$DIR" conn)" == "$DIR/hermod.db" ]] \
    && pass "conn is the SQLite path" || fail "conn is '$(field "$DIR" conn)', want $DIR/hermod.db"
  [[ "$(field "$DIR" jwt_secret)" == "kept-jwt-secret" ]] \
    && pass "jwt_secret kept" || fail "jwt_secret is '$(field "$DIR" jwt_secret)'"
  [[ "$(field "$DIR" crypto_master_key)" == "kept-master-key" ]] \
    && pass "crypto_master_key kept" || fail "crypto_master_key is '$(field "$DIR" crypto_master_key)'"
fi
[[ "$(cat "$DIR/.db-type")" == "sqlite" ]] \
  && pass "type stamp updated" || fail "type stamp is '$(cat "$DIR/.db-type")'"

echo "▸ Same type, stale address: only conn changes"
DIR="$SCRATCH/same"
seed "$DIR"
echo "sqlite" > "$DIR/.db-type"
sed -i.bak 's/^type: postgres$/type: sqlite/; s|^conn: .*|conn: /gone/hermod.db|' "$DIR/config/db_config.yaml"
sync "$DIR" --sqlite >/dev/null
[[ "$(field "$DIR" conn)" == "$DIR/hermod.db" ]] \
  && pass "conn refreshed" || fail "conn is '$(field "$DIR" conn)', want $DIR/hermod.db"
[[ "$(field "$DIR" jwt_secret)" == "kept-jwt-secret" ]] \
  && pass "jwt_secret kept" || fail "jwt_secret is '$(field "$DIR" jwt_secret)'"

echo "▸ No stored config: none is invented"
DIR="$SCRATCH/fresh"
mkdir -p "$DIR"
sync "$DIR" --sqlite >/dev/null
[[ ! -f "$DIR/config/db_config.yaml" ]] \
  && pass "db_config.yaml left for first-run setup to write" \
  || fail "db_config.yaml was created; the backend would skip first-run setup"
[[ "$(cat "$DIR/.db-type" 2>/dev/null)" == "sqlite" ]] \
  && pass "type stamp written" || fail "type stamp missing"

if [[ "$FAILURES" -gt 0 ]]; then
  echo "${RED}$FAILURES check(s) failed${RESET}" >&2
  exit 1
fi
echo "${GREEN}all checks passed${RESET}"
