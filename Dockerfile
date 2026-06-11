# syntax=docker/dockerfile:1
#
# bragent multi-arch image.
#
# Stage 1: build the static binary with the release tag stamped into
# main.Version. CGO is off (modernc.org/sqlite is pure-Go), so the
# resulting binary runs on scratch — no glibc/musl coupling.
#
# Stage 2: scratch + ca-certificates + the binary. The user mounts a
# config file at /etc/bragent/config.toml and (optionally) a writable
# volume for the SQLite session store. The default config is baked in
# so `docker run --rm ghcr.io/kapoost/bragent --version` just works.

ARG GO_VERSION=1.25

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src
RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
        -trimpath \
        -ldflags "-s -w -X main.Version=${VERSION}" \
        -o /out/bragent \
        ./cmd/bragent

FROM scratch AS runtime
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/bragent /bragent
COPY config.docker.toml /etc/bragent/config.toml
COPY feeds/example.json /etc/bragent/feeds/example.json
# Pre-create the writable volume mount-point with the right owner so the
# scratch image works even without an explicit `-v` (Docker auto-creates
# an anonymous volume).
COPY --from=build --chown=65534:65534 /tmp /var/lib/bragent

USER 65534:65534
EXPOSE 8080
VOLUME ["/var/lib/bragent"]

# `--config` is baked into ENTRYPOINT so additional CLI args (e.g.
# `--simulate-host`) appended at `docker run` time don't replace it.
# Override the config by passing a second `--config /path/to/your.toml`
# — Go's flag.Parse keeps the last occurrence.
ENTRYPOINT ["/bragent", "--config", "/etc/bragent/config.toml"]
