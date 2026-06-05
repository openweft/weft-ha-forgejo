# syntax=docker/dockerfile:1.7

# Image layout for forgejo-ha replicas :
#
#   Stage 1 (builder) cross-builds the weft-ha-forgejo agent against the
#   target arch, pure-Go, CGO disabled — so the resulting binary can drop
#   into any linux/$TARGETARCH base image.
#
#   Stage 2 starts from codeberg.org/forgejo/forgejo:10 (the Forgejo
#   upstream image) and bolts the agent binary on top. The image runs
#   BOTH processes :
#
#     - forgejo (the upstream binary, baked into the base) in the
#       background, holding the listener on :3000,
#     - weft-ha-forgejo agent in the foreground holding the role API on
#       :3001 + the reconcile loop probing forgejo's /api/healthz.
#
#   The entrypoint script wires the two together with `setsid` + a
#   parent-death trap so that if EITHER process dies the container
#   exits non-zero — the supervisor in weft-agent then restarts the
#   replica and the L7 pool drains it in the meantime.
#
# Build :   docker buildx build --platform linux/amd64,linux/arm64 -t … .
# Trigger : workflow_dispatch + on push: tags ['v*'] only (no
#           autopublish on push:main — see openweft policy).

ARG GO_VERSION=1.26
ARG FORGEJO_VERSION=10

############################################################
# Stage 1 — build the weft-ha-forgejo agent
############################################################
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder
ARG TARGETOS
ARG TARGETARCH

# Reproducible builds : trimpath drops $GOPATH from filenames,
# -s -w strips DWARF + symbol table.
ENV CGO_ENABLED=0

WORKDIR /src

# Cache modules independently of the source for layer reuse.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
        -trimpath \
        -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
        -o /out/weft-ha-forgejo \
        ./cmd/weft-ha-forgejo

############################################################
# Stage 2 — Forgejo base + agent + entrypoint wrapper
############################################################
FROM codeberg.org/forgejo/forgejo:${FORGEJO_VERSION}

LABEL org.opencontainers.image.source="https://github.com/openweft/weft-ha-forgejo"
LABEL org.opencontainers.image.description="Forgejo upstream + weft-ha-forgejo HA agent (one process per replica micro-VM)"
LABEL org.opencontainers.image.licenses="BSD-3-Clause AND MIT"

# Forgejo base ships /bin/sh (BusyBox) ; we don't need bash here.
COPY --from=builder /out/weft-ha-forgejo /usr/local/bin/weft-ha-forgejo
COPY docker/entrypoint.sh /usr/local/bin/forgejo-ha-entrypoint

# Forgejo exposes :3000 (HTTP), :22 (SSH). The agent role API is :3001.
EXPOSE 3000 3001 22

ENTRYPOINT ["/usr/local/bin/forgejo-ha-entrypoint"]
