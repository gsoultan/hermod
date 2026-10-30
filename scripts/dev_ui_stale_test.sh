#!/usr/bin/env bash
#
# Regression test for when `dev.sh` refreshes the UI bundle the API serves.
#
# The API port serves internal/api/static, which is generated and not
# committed. dev.sh only rebuilt it under --build-ui, so after pulling the
# sidebar redesign the API port kept serving the old UI with no hint why: the
# bundle on disk predated every source file it was built from.
#
# A bundle older than its sources is stale and must be rebuilt without being
# asked; a bundle newer than all of them must not, since a rebuild is slow.
#
#   ./scripts/dev_ui_stale_test.sh
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

# A scratch repository with the layout dev.sh resolves from its own location,
# so the probe reads these files and never the real checkout's.
seed() {
  local dir="$1" f
  rm -rf "$dir"
  mkdir -p "$dir/scripts" "$dir/ui/src" "$dir/ui/public" "$dir/internal/api/static"
  cp "$REPO_ROOT/scripts/dev.sh" "$dir/scripts/dev.sh"
  for f in ui/src/App.tsx ui/public/icon.svg ui/index.html ui/package.json ui/bun.lock ui/vite.config.ts; do
    echo "x" > "$dir/$f"
    touch -t 202601010000 "$dir/$f"
  done
  # Directories too: a deleted source file shows only as its directory's mtime.
  touch -t 202601010000 "$dir/ui/src" "$dir/ui/public"
}

bundle_at() { echo "<html></html>" > "$1/internal/api/static/index.html"; touch -t "$2" "$1/internal/api/static/index.html"; }
state() { HERMOD_DEV_DIR="$1/.dev" "$1/scripts/dev.sh" --print-ui-state; }

expect() {
  local dir="$1" want="$2" what="$3" got
  got="$(state "$dir" 2>&1)" || true
  if [[ "$got" == "$want" ]]; then pass "$what"; else fail "$what: want '$want', got '$got'"; fi
}

echo "▸ The served UI bundle is rebuilt only when its sources moved past it"

DIR="$SCRATCH/missing"
seed "$DIR"
expect "$DIR" stale "no bundle on disk is stale"

DIR="$SCRATCH/fresh"
seed "$DIR"
bundle_at "$DIR" 202602010000
expect "$DIR" fresh "a bundle newer than every source is fresh"

DIR="$SCRATCH/src-edit"
seed "$DIR"
bundle_at "$DIR" 202602010000
touch -t 202603010000 "$DIR/ui/src/App.tsx"
expect "$DIR" stale "a source file newer than the bundle makes it stale"

DIR="$SCRATCH/src-delete"
seed "$DIR"
bundle_at "$DIR" 202602010000
rm "$DIR/ui/src/App.tsx"
expect "$DIR" stale "a source file deleted after the bundle makes it stale"

for f in ui/public/icon.svg ui/index.html ui/package.json ui/bun.lock ui/vite.config.ts; do
  DIR="$SCRATCH/other-$(basename "$f")"
  seed "$DIR"
  bundle_at "$DIR" 202602010000
  touch -t 202603010000 "$DIR/$f"
  expect "$DIR" stale "$f newer than the bundle makes it stale"
done

if [[ "$FAILURES" -gt 0 ]]; then
  echo "${RED}${FAILURES} failure(s)${RESET}" >&2
  exit 1
fi
echo "${GREEN}all passed${RESET}"
