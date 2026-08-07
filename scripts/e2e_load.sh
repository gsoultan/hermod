#!/usr/bin/env bash
#
# Heavy-traffic harness for the CDC → db_lookup → Postgres sink scenario.
#
#   ./scripts/e2e_load.sh [ROWS] [BATCH]
#
# Inserts ROWS orders into the source table in BATCH-sized transactions, then
# waits for them to land in the sink, sampling the engine's CPU and RSS while it
# drains. Reports throughput, resource high-water marks, and whether every row
# arrived enriched.
#
# It measures rather than asserts: the point is evidence about behaviour under
# load, including the failure modes (backpressure, sink outage) exercised
# separately by the caller.

set -euo pipefail

ROWS="${1:-20000}"
BATCH="${2:-2000}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PG="${HERMOD_DEV_PG_CONTAINER:-postgres-dev}"
SRC_DB=hermod_test_source
SINK_DB=hermod_test_sink

psql_src() { container exec "$PG" psql -U postgres -d "$SRC_DB" -tAc "$1"; }
psql_sink() { container exec "$PG" psql -U postgres -d "$SINK_DB" -tAc "$1"; }

engine_pid() { pgrep -f "$REPO_ROOT/.dev/hermod" | head -1; }

say() { echo "▸ $*"; }

PID="$(engine_pid || true)"
[[ -n "$PID" ]] || { echo "engine not running (start ./scripts/dev.sh)" >&2; exit 1; }

BASE_SINK="$(psql_sink 'SELECT count(*) FROM orders_enriched')"
BASE_ERR="$(/usr/bin/grep -ac '"level":"error"' "$REPO_ROOT/.dev/logs/backend.log" || echo 0)"
say "engine pid $PID · sink starts at $BASE_SINK rows"

# --- produce -----------------------------------------------------------------

say "inserting $ROWS orders in batches of $BATCH"
INS_START=$(date +%s)
produced=0
while (( produced < ROWS )); do
  n=$(( ROWS - produced < BATCH ? ROWS - produced : BATCH ))
  psql_src "INSERT INTO orders (order_ref, customer_code, amount)
            SELECT 'LOAD-'||g, 'C'||LPAD((((g+$produced)%500)+1)::text,4,'0'), ((g%997)*1.37)::numeric(12,2)
            FROM generate_series(1,$n) g;" >/dev/null
  produced=$(( produced + n ))
done
INS_END=$(date +%s)
INS_SECS=$(( INS_END - INS_START ))
(( INS_SECS > 0 )) || INS_SECS=1
say "produced $produced rows in ${INS_SECS}s ($(( produced / INS_SECS ))/s into Postgres)"

# --- drain, sampling resources ----------------------------------------------

TARGET=$(( BASE_SINK + ROWS ))
say "draining to $TARGET rows in the sink"

PEAK_RSS=0; PEAK_CPU=0; SAMPLES=0; STALLED=0; LAST=-1
DRAIN_START=$(date +%s)
while :; do
  now="$(psql_sink 'SELECT count(*) FROM orders_enriched' 2>/dev/null || echo "$LAST")"
  read -r cpu rss <<<"$(ps -o %cpu=,rss= -p "$PID" 2>/dev/null || echo '0 0')"
  # ps pads; strip whitespace before comparing.
  rss="${rss// /}"; cpu="${cpu// /}"
  awk -v a="${cpu:-0}" -v b="$PEAK_CPU" 'BEGIN{exit !(a>b)}' && PEAK_CPU="$cpu"
  (( ${rss:-0} > PEAK_RSS )) && PEAK_RSS="${rss:-0}"
  SAMPLES=$(( SAMPLES + 1 ))

  (( now >= TARGET )) && break

  if [[ "$now" == "$LAST" ]]; then
    STALLED=$(( STALLED + 1 ))
    # 60 consecutive idle seconds means it is not going to finish.
    (( STALLED > 60 )) && { say "STALLED at $now/$TARGET"; break; }
  else
    STALLED=0
  fi
  LAST="$now"

  if ! kill -0 "$PID" 2>/dev/null; then
    say "ENGINE DIED during drain at $now/$TARGET rows"
    break
  fi
  sleep 1
done
DRAIN_SECS=$(( $(date +%s) - DRAIN_START ))
(( DRAIN_SECS > 0 )) || DRAIN_SECS=1

FINAL="$(psql_sink 'SELECT count(*) FROM orders_enriched')"
DELIVERED=$(( FINAL - BASE_SINK ))
ENRICHED="$(psql_sink "SELECT count(*) FROM orders_enriched WHERE data->>'customer_name' IS NOT NULL")"
MISPLACED="$(psql_sink "SELECT count(*) FROM orders_enriched WHERE data ? '\$'")"
ERR_NOW="$(/usr/bin/grep -ac '"level":"error"' "$REPO_ROOT/.dev/logs/backend.log" || echo 0)"

cat <<REPORT

────────── RESULT ──────────
  rows requested     $ROWS
  rows delivered     $DELIVERED
  enriched (total)   $ENRICHED
  misplaced under \$  $MISPLACED
  drain time         ${DRAIN_SECS}s
  throughput         $(( DELIVERED / DRAIN_SECS )) rows/s end-to-end
  peak RSS           $(( PEAK_RSS / 1024 )) MB
  peak CPU           ${PEAK_CPU}%
  new error lines    $(( ERR_NOW - BASE_ERR ))
  engine alive       $(kill -0 "$PID" 2>/dev/null && echo yes || echo NO)
────────────────────────────
REPORT
