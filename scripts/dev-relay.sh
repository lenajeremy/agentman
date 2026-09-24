#!/usr/bin/env bash
#
# Run the relay from this checkout and reach it from a phone.
#
# A relay on localhost is not reachable from a phone, and the phone is the
# whole point of the product — so the local relay is published through the
# public one with `am expose`. Two relays are involved and they are not
# interchangeable:
#
#   * the local relay under test, on $PORT, which the daemon and phone use;
#   * the public relay, used only to carry the tunnel that makes the local one
#     reachable. `am expose` must never be pointed at the local relay, or it
#     would be asked to tunnel itself.
#
# Nothing in the repository is edited. The CLI takes AGENTMAN_RELAY, and the
# app takes whichever relay it paired against, so the URL stays out of the
# source tree where a rotating tunnel address does not belong.
#
# Usage:  scripts/dev-relay.sh [port]
set -euo pipefail

PORT="${1:-8099}"
PUBLIC_RELAY="${AGENTMAN_PUBLIC_RELAY:-https://agentman-production.up.railway.app}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STATE="${TMPDIR:-/tmp}/agentman-dev-relay"
mkdir -p "$STATE"

# A stable secret keeps paired devices working across restarts: the relay signs
# device tokens with it and re-verifies them on every connection, so a fresh
# secret would silently unpair the phone.
SECRET_FILE="$STATE/secret"
if [ ! -f "$SECRET_FILE" ]; then
  openssl rand -hex 32 > "$SECRET_FILE"
  echo "Generated a development relay secret at $SECRET_FILE"
fi
SECRET="$(cat "$SECRET_FILE")"

# The same reasoning as the secret, for the address. A tunnel link is derived
# from the account and a nonce, and `am expose` invents a fresh one per process
# — so without this every rebuild hands out a new URL and the phone, which
# stored the old one when it paired, quietly stops finding the relay.
NONCE_FILE="$STATE/tunnel-nonce"
if [ ! -f "$NONCE_FILE" ]; then
  openssl rand -hex 16 > "$NONCE_FILE"
fi
export AGENTMAN_TUNNEL_NONCE="$(cat "$NONCE_FILE")"

if lsof -nP -iTCP:"$PORT" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "Port $PORT is already in use. Pass another port: scripts/dev-relay.sh 8100" >&2
  exit 1
fi

cleanup() {
  [ -n "${RELAY_PID:-}" ] && kill "$RELAY_PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "Building…"
go build -o "$STATE/relay" ./cmd/relay
go build -o "$STATE/am" ./cmd/am

echo "Starting the local relay on :$PORT"
AGENTMAN_RELAY_SECRET="$SECRET" PORT="$PORT" "$STATE/relay" > "$STATE/relay.log" 2>&1 &
RELAY_PID=$!

for _ in $(seq 1 40); do
  if curl -fsS "http://localhost:$PORT/health" >/dev/null 2>&1; then break; fi
  if ! kill -0 "$RELAY_PID" 2>/dev/null; then
    echo "The relay exited. Last output:" >&2
    tail -20 "$STATE/relay.log" >&2
    exit 1
  fi
  sleep 0.25
done
curl -fsS "http://localhost:$PORT/health" >/dev/null || { echo "Relay never became healthy." >&2; exit 1; }
echo "  healthy: $(curl -fsS "http://localhost:$PORT/health")"

cat <<EOF

Publishing the local relay through $PUBLIC_RELAY
Leave this running. The public link below is your relay for as long as it is.

EOF

# -relay is explicit: if AGENTMAN_RELAY is already exported for the daemon,
# expose would otherwise try to tunnel the local relay through itself.
exec "$STATE/am" expose -relay "$PUBLIC_RELAY" "$PORT"
