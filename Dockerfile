# Hermod container image.
#
# The UI is served by the API from an embedded filesystem
# (internal/api/server.go: //go:embed all:static), so it has to exist before the
# Go build runs — a binary built without it starts and serves nothing.
# scripts/dev.sh gets there via `hermod --build-ui`, which shells out to bun and
# then copies ui/dist into internal/api/static. That is circular here (the
# binary would have to exist to build the assets it embeds), so this does the
# two steps directly.

# --- UI -----------------------------------------------------------------------
FROM oven/bun:1-alpine AS ui

WORKDIR /src

# Manifests first: this layer is rebuilt only when dependencies change, not on
# every source edit.
COPY ui/package.json ui/bun.lock* ./ui/
RUN cd ui && bun install --frozen-lockfile

COPY ui/ ./ui/
RUN cd ui && bun run build

# --- Go build -----------------------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# The embedded assets. .gitkeep is all that is committed under static/; the rest
# is generated, so it is copied in rather than assumed present.
COPY --from=ui /src/ui/dist/ ./internal/api/static/

# CGO off to match .goreleaser.yaml and to keep the runtime image free of libc.
# -trimpath and the ldflags mirror the release build so a container and a
# tarball of the same tag are the same binary.
ARG VERSION=dev
ENV CGO_ENABLED=0
RUN go build -trimpath \
      -ldflags="-s -w -X 'github.com/user/hermod/internal/version.Version=${VERSION}'" \
      -o /out/hermod ./cmd/hermod

# Fail the build rather than ship a binary that serves a blank page.
RUN test -f ./internal/api/static/index.html

# --- Runtime ------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

# Distroless static: no shell, no package manager, no libc. Nothing to exec if
# the process is compromised, and nothing to patch on a CVE treadmill.
# :nonroot runs as uid 65532 and the chart sets runAsNonRoot to enforce it.

COPY --from=build /out/hermod /usr/local/bin/hermod

# Hermod writes db_config.yaml (JWT secret, crypto master key) into its config
# directory, which defaults to ~/.hermod — not writable for uid 65532 here. The
# chart mounts a volume at this path; without one, first-run setup cannot
# persist and the container comes up asking to be configured on every restart.
ENV HERMOD_CONFIG_DIR=/var/lib/hermod
VOLUME /var/lib/hermod

# API (default --port 4000) and gRPC (default --grpc-port 50051).
EXPOSE 4000 50051

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/hermod"]
