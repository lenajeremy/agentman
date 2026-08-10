# The relay, and only the relay.
#
# This repository also holds the daemon (cmd/am) and the mobile app, but the
# relay is the one piece anyone deploys — the daemon runs on your own machine
# and the app runs on your phone. Building just this keeps the image tiny and
# makes self-hosting a single `docker build`.

FROM golang:1.24-alpine AS build
WORKDIR /src

# Dependencies first, so a source-only change reuses the cached layer.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# Static, stripped, and reproducible. CGO is off so the result runs on scratch.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/relay ./cmd/relay

FROM alpine:3.20
# TLS roots, so the relay can dial out (health checks, future push delivery).
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 relay
USER relay

COPY --from=build /out/relay /usr/local/bin/relay

# Railway supplies PORT; this is only the fallback for a plain docker run.
ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/relay"]
