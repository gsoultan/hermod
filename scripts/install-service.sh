#!/usr/bin/env bash
set -euo pipefail

MODE="${1:-standalone}"
shift || true

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EXE="$REPO_ROOT/hermod"

if [[ ! -x "$EXE" ]]; then
  echo "Building hermod..."
  (cd "$REPO_ROOT" && go build -o "$EXE" ./cmd/hermod)
fi

"$EXE" -mode "$MODE" -service install "$@"
"$EXE" -service start

echo "Service installed and started. Useful commands:"
echo "  $EXE -service status"
echo "  $EXE -service stop"
echo "  $EXE -service uninstall"}