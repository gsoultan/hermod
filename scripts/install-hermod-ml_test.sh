#!/usr/bin/env bash
#
# Verifies scripts/install-hermod-ml.sh against a stand-in docker and curl:
# the image tag it picks (slim or -dl, latest or a release), the token it
# generates and passes to the worker, the restart policy and port, the wait
# for the worker to answer, and that it refuses rather than half-installs.
#
#   ./scripts/install-hermod-ml_test.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$REPO_ROOT/scripts/install-hermod-ml.sh"

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
CALLS="$FAKE_BIN/calls"

# A stand-in docker: records every call. FAKE_DOCKER_DOWN makes `docker info`
# fail; FAKE_EXISTS makes the container already exist; FAKE_ARCH is the
# daemon's architecture.
cat > "$FAKE_BIN/docker" <<'EOF'
#!/usr/bin/env bash
echo "docker $*" >> "$CALLS"
case "$1" in
  info)
    [[ -n "${FAKE_DOCKER_DOWN:-}" ]] && { echo "Cannot connect to the Docker daemon" >&2; exit 1; }
    if [[ "$*" == *Architecture* ]]; then echo "${FAKE_ARCH:-x86_64}"; fi
    exit 0 ;;
  container)
    [[ "$2" == inspect && -n "${FAKE_EXISTS:-}" ]] && exit 0
    exit 1 ;;
  run) echo "0123456789abcdef"; exit 0 ;;
  logs) echo "worker log line"; exit 0 ;;
esac
exit 0
EOF
# A stand-in curl: the worker answers unless FAKE_NEVER_READY is set.
cat > "$FAKE_BIN/curl" <<'EOF'
#!/usr/bin/env bash
echo "curl $*" >> "$CALLS"
[[ -n "${FAKE_NEVER_READY:-}" ]] && exit 7
echo '{"ready":true}'
EOF
chmod +x "$FAKE_BIN/docker" "$FAKE_BIN/curl"

# run <expected-exit> <description> [env...] -- [args...]
OUT=""
run() {
  local want="$1" desc="$2"; shift 2
  local envs=()
  while [[ $# -gt 0 && "$1" != "--" ]]; do envs+=("$1"); shift; done
  shift
  : > "$CALLS"
  local status
  OUT="$(env PATH="$FAKE_BIN:$PATH" CALLS="$CALLS" "${envs[@]}" "$SCRIPT" --wait 2 "$@" 2>&1)" && status=0 || status=$?
  if [[ "$status" -eq "$want" ]]; then
    pass "$desc"
  else
    fail "$desc (exit $status, wanted $want)"
    echo "      ${OUT//$'\n'/$'\n      '}" >&2
  fi
}

expect_call() {
  if grep -qE -- "$1" "$CALLS"; then pass "$2"; else fail "$2"; sed 's/^/      /' "$CALLS" >&2; fi
}
expect_out() {
  if grep -qE -- "$1" <<<"$OUT"; then pass "$2"; else fail "$2"; echo "      ${OUT//$'\n'/$'\n      '}" >&2; fi
}

echo "install-hermod-ml.sh"

run 0 "installs the slim image" --
expect_call 'docker run -d --name hermod-ml --restart unless-stopped -p 8090:8090 -e HERMOD_ML_TOKEN=[0-9a-f]{64} -v hermod-ml:/var/lib/hermod-ml ghcr.io/gsoultan/hermod-ml:latest$' \
  "runs ghcr.io/gsoultan/hermod-ml:latest with a 64-hex token, restart unless-stopped and port 8090"
expect_call 'curl .*http://localhost:8090/v2/health/ready' "waits on the worker's readiness check"
TOKEN="$(grep -oE 'HERMOD_ML_TOKEN=[0-9a-f]{64}' "$CALLS" | cut -d= -f2)"
expect_out "Token: +$TOKEN" "prints the same token it gave the worker"
expect_out 'Address: +http://localhost:8090' "prints the address to paste into Hermod"

run 0 "a second install generates another token" --
SECOND="$(grep -oE 'HERMOD_ML_TOKEN=[0-9a-f]{64}' "$CALLS" | cut -d= -f2)"
if [[ -n "$SECOND" && "$SECOND" != "$TOKEN" ]]; then pass "tokens differ between installs"; else fail "the token repeated: $SECOND"; fi

run 0 "installs the deep-learning image" -- --deep-learning
expect_call 'ghcr.io/gsoultan/hermod-ml:latest-dl$' "--deep-learning picks the -dl tag"

run 0 "pins a release" -- --version v1.18.0 --deep-learning
expect_call 'ghcr.io/gsoultan/hermod-ml:1.18.0-dl$' "--version v1.18.0 runs the 1.18.0-dl tag, without the v"

run 0 "takes a given token, port and network" -- --token my-token --port 9000 --network hermod-net
expect_call 'docker run -d --name hermod-ml --restart unless-stopped --network hermod-net -p 9000:8090 -e HERMOD_ML_TOKEN=my-token ' \
  "passes the token, port and network to docker run"
expect_out 'http://hermod-ml:8090' "says the address Hermod uses on that network"

run 1 "refuses without a Docker daemon" FAKE_DOCKER_DOWN=1 --
expect_out 'Docker' "says Docker is the problem"
if grep -q "docker run" "$CALLS"; then fail "ran a container without a daemon"; else pass "runs nothing"; fi

run 1 "refuses to replace a worker that exists" FAKE_EXISTS=1 --
expect_out 'docker rm -f hermod-ml' "says how to remove the old one"
if grep -q "docker run" "$CALLS"; then fail "ran over an existing container"; else pass "runs nothing"; fi

run 1 "refuses the -dl image on arm64" FAKE_ARCH=aarch64 -- --deep-learning
expect_out 'amd64' "says the deep-learning image is amd64 only"

run 1 "fails when the worker never answers" FAKE_NEVER_READY=1 --
expect_out 'docker logs hermod-ml' "points at the worker's log"

run 2 "refuses an unknown option" -- --bogus
run 2 "refuses a bad version" -- --version 'latest; rm -rf /'

echo
if [[ "$FAILURES" -gt 0 ]]; then
  echo "${RED}$FAILURES check(s) failed${RESET}" >&2
  exit 1
fi
echo "${GREEN}all checks passed${RESET}"
