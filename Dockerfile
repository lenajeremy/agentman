# The relay, and only the relay.
#
# This repository also holds the daemon (cmd/am) and the mobile app, but the
# relay is the one piece anyone deploys — the daemon runs on your own machine
# and the app runs on your phone. Building just this keeps the image tiny and
# makes self-hosting a single `docker build`.

# Pin both the toolchain patch and the multi-platform image digest. A mutable
# major-version tag can otherwise change the compiler and build environment
# underneath an unchanged commit.
FROM golang:1.26.6-alpine3.23@sha256:e57c41c1d5864341031181b0db34b9a537bb5773eb6428e4e5bdaea0f9135406 AS build
WORKDIR /src

# Dependencies first, so a source-only change reuses the cached layer.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# Stamped so a running relay can say what it is. Without this /health reports
# "dev" forever, and "which commit is actually deployed?" has no answer — a
# question that already cost us once, when a hand-deploy left production ahead
# of main without anything showing it.
ARG VERSION=dev

# Static, stripped, and reproducible. CGO is off so the result runs on scratch.
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/relay ./cmd/relay

FROM scratch
# The relay is an inbound-only static binary. A scratch runtime removes the
# shell, package manager, and unrelated libraries from the attack surface.
USER 10001:10001

COPY --from=build /out/relay /relay

# Railway supplies PORT; this is only the fallback for a plain docker run.
ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["/relay"]
