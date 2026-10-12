#!/usr/bin/env bash
#
# Installs the hermod-ml training worker with Docker in one command, and
# prints the address and token to paste into Hermod (Machine Learning >
# Getting started > Set up the worker).
#
#   ./scripts/install-hermod-ml.sh                   # slim image: scikit-learn, XGBoost
#   ./scripts/install-hermod-ml.sh --deep-learning   # adds PyTorch and TensorFlow (amd64 only)
#
# Options:
#   --deep-learning     run the "-dl" image (about 2 GB larger, linux/amd64 only)
#   --version X.Y.Z     the release to run; default latest. Use Hermod's own
#                       version so the two match (a leading "v" is dropped).
#   --token TOKEN       the worker's token; default a new random one
#   --port PORT         the port on this machine; default 8090
#   --network NAME      also join this Docker network, so a Hermod container
#                       on it reaches the worker as http://hermod-ml:8090
#   --name NAME         the container's name; default hermod-ml
#   --wait SECONDS      how long to wait for the worker to answer; default 180
#
# Image tags are those the release workflow publishes
# (.github/workflows/release.yml, ml-worker-image): X.Y.Z, X.Y and latest, and
# the same with "-dl" for the deep-learning image.

set -euo pipefail

IMAGE_REPO="ghcr.io/gsoultan/hermod-ml"
VERSION="latest"
DEEP=""
TOKEN=""
PORT="8090"
NETWORK=""
NAME="hermod-ml"
WAIT="180"

usage() { sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'; }
die() { echo "error: $*" >&2; exit 1; }
bad() { echo "error: $*" >&2; echo "Run $0 --help for the options." >&2; exit 2; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --deep-learning) DEEP=1; shift ;;
    --version) [[ $# -ge 2 ]] || bad "--version needs a value"; VERSION="$2"; shift 2 ;;
    --token) [[ $# -ge 2 ]] || bad "--token needs a value"; TOKEN="$2"; shift 2 ;;
    --port) [[ $# -ge 2 ]] || bad "--port needs a value"; PORT="$2"; shift 2 ;;
    --network) [[ $# -ge 2 ]] || bad "--network needs a value"; NETWORK="$2"; shift 2 ;;
    --name) [[ $# -ge 2 ]] || bad "--name needs a value"; NAME="$2"; shift 2 ;;
    --wait) [[ $# -ge 2 ]] || bad "--wait needs a value"; WAIT="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) bad "unknown option: $1" ;;
  esac
done

VERSION="${VERSION#v}"
[[ "$VERSION" == latest || "$VERSION" =~ ^[0-9]+\.[0-9]+(\.[0-9]+(-[0-9A-Za-z.]+)?)?$ ]] ||
  bad "--version must be latest or a release such as 1.18.0, not '$VERSION'"
[[ "$PORT" =~ ^[0-9]+$ ]] && (( PORT > 0 && PORT < 65536 )) || bad "--port must be a port number, not '$PORT'"
[[ "$WAIT" =~ ^[0-9]+$ ]] || bad "--wait must be a number of seconds, not '$WAIT'"
[[ "$NAME" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] || bad "--name must be a container name, not '$NAME'"
[[ -z "$NETWORK" || "$NETWORK" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] || bad "--network must be a network name, not '$NETWORK'"
[[ "$TOKEN" =~ ^[^[:space:]]*$ ]] || bad "--token must not contain spaces"

TAG="$VERSION"
[[ -n "$DEEP" ]] && TAG="$VERSION-dl"
IMAGE="$IMAGE_REPO:$TAG"

# 1. Docker, and a daemon that answers.
command -v docker >/dev/null 2>&1 ||
  die "Docker is not installed. Install it from https://docs.docker.com/get-docker/ and run this again."
docker info >/dev/null 2>&1 ||
  die "Docker is installed but its daemon does not answer. Start Docker (or run this as a user allowed to use it) and run this again."

if [[ -n "$DEEP" ]]; then
  arch="$(docker info --format '{{.Architecture}}' 2>/dev/null || true)"
  case "$arch" in
    aarch64|arm64) die "the deep-learning image is published for amd64 (x86_64) only, and this Docker runs on $arch. Run without --deep-learning, or on an amd64 machine." ;;
  esac
fi

if docker container inspect "$NAME" >/dev/null 2>&1; then
  die "a container named $NAME already exists. To replace it (its datasets and models stay in the hermod-ml volume): docker rm -f $NAME, then run this again. Or pick another --name."
fi

# 2. A token, unless one was given.
if [[ -z "$TOKEN" ]]; then
  if command -v openssl >/dev/null 2>&1; then
    TOKEN="$(openssl rand -hex 32)"
  else
    TOKEN="$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')"
  fi
fi
[[ -n "$TOKEN" ]] || die "could not generate a token; pass one with --token"

# 3. Run it.
run_args=(run -d --name "$NAME" --restart unless-stopped)
[[ -n "$NETWORK" ]] && run_args+=(--network "$NETWORK")
run_args+=(-p "$PORT:8090" -e "HERMOD_ML_TOKEN=$TOKEN" -v hermod-ml:/var/lib/hermod-ml "$IMAGE")

echo "Starting $IMAGE as $NAME on port $PORT..."
docker "${run_args[@]}" >/dev/null

# 4. Wait until it answers.
ready_url="http://localhost:$PORT/v2/health/ready"
echo "Waiting for the worker to answer (up to ${WAIT}s; the first start downloads the image)..."
deadline=$((SECONDS + WAIT))
until curl -fsS --max-time 3 "$ready_url" >/dev/null 2>&1; do
  if (( SECONDS >= deadline )); then
    echo "error: the worker did not answer at $ready_url within ${WAIT}s." >&2
    echo "Its log says why: docker logs $NAME" >&2
    exit 1
  fi
  sleep 1
done

# 5. What to paste into Hermod.
address="http://localhost:$PORT"
[[ -n "$NETWORK" ]] && address="http://$NAME:8090"
cat <<EOF

hermod-ml is running.

In Hermod, open Machine Learning > Getting started > Set up the worker, and paste:

  Address: $address
  Token:   $TOKEN

Which address Hermod needs depends on where Hermod runs:
  - on this machine, not in a container: http://localhost:$PORT
EOF
if [[ -n "$NETWORK" ]]; then
  echo "  - in a container on the $NETWORK network: http://$NAME:8090"
else
  echo "  - in a container: run this again with --network <Hermod's network> and use http://$NAME:8090"
  echo "    (inside a container, localhost is that container, not this machine)"
fi
echo "  - on another machine: http://<this machine's address>:$PORT"
echo
echo "Keep the token: it is the worker's password. Stop the worker with: docker rm -f $NAME"
