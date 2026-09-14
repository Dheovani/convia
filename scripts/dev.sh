#!/usr/bin/env bash
#
# Runs Convia and its interface together, for local testing.
#
# Starts the local dependencies with Docker Compose, applies the migrations,
# builds and starts the API, and serves the interface with hot reloading. Open
# http://localhost:5173: the interface reaches the API through the Vite proxy,
# so the browser still sees one origin and the session cookie works as it does
# in production.
#
# Configuration comes from .env at the repository root, exactly as the README
# describes. The media plane (LiveKit) and the shared channel (Redis) are
# started only when .env configures them.
#
# Nobody needs an account beforehand: create one on the sign-in page.
#
# Ctrl+C stops both. The containers are left running, because they are the
# slowest part to start and they hold the database; stop them with
# `docker compose down`.
#
# Usage: ./scripts/dev.sh [--skip-migrations]

set -euo pipefail

# The interface's development server. web/vite.config.ts proxies /v1 from here
# to the API on 8080.
interface_port=5173
proxied_api_port=8080

skip_migrations=false
for argument in "$@"; do
  case "$argument" in
    --skip-migrations) skip_migrations=true ;;
    *) echo "error: unknown argument $argument" >&2; exit 2 ;;
  esac
done

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

if [[ ! -f .env ]]; then
  echo "error: .env does not exist. Create it from the template with: cp .env.example .env" >&2
  exit 1
fi
set -a
# shellcheck disable=SC1091
. ./.env
set +a

for tool in docker go node npm curl; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "error: $tool is not installed or is not on PATH." >&2
    exit 1
  fi
done

api_port="${CONVIA_HTTP_PORT:-$proxied_api_port}"
if [[ "$api_port" != "$proxied_api_port" ]]; then
  echo "error: CONVIA_HTTP_PORT is $api_port, but web/vite.config.ts proxies the interface to $proxied_api_port." >&2
  exit 1
fi

for port in "$api_port" "$interface_port"; do
  if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
    echo "error: port $port is already in use. Stop whatever holds it - possibly an earlier Convia - and run this again." >&2
    exit 1
  fi
done

profiles=()
if [[ -n "${CONVIA_LIVEKIT_URL:-}" ]]; then
  profiles+=(--profile media)
fi
if [[ -n "${CONVIA_REDIS_URL:-}" ]]; then
  profiles+=(--profile shared)
fi

echo "Starting the dependencies..."
docker compose "${profiles[@]}" up --detach --wait

if [[ "$skip_migrations" == false ]]; then
  echo "Applying migrations..."
  go run ./cmd/convia migrate up
fi

# The interface's dependencies are installed when they are missing, or when the
# lockfile changed since they were.
installed=web/node_modules/.package-lock.json
if [[ ! -f "$installed" || web/package-lock.json -nt "$installed" ]]; then
  echo "Installing the interface dependencies..."
  (cd web && npm ci)
fi

if [[ -z "${CONVIA_LIVEKIT_URL:-}" ]]; then
  echo "warning: no media plane is configured in .env, so calls cannot be started. See docs/media.md." >&2
fi

binary="${TMPDIR:-/tmp}/convia-dev/convia$(go env GOEXE)"
echo "Building Convia..."
go build -o "$binary" ./cmd/convia

"$binary" serve &
api=$!
# A background job in a script does not receive Ctrl+C, so the API is stopped
# here instead, with the signal it already shuts down gracefully on.
trap 'kill "$api" 2>/dev/null || true' EXIT

ready=false
for _ in $(seq 1 60); do
  if ! kill -0 "$api" 2>/dev/null; then
    echo "error: Convia exited before it was ready. Its output is above." >&2
    exit 1
  fi
  if curl --silent --fail --output /dev/null "http://127.0.0.1:$api_port/health"; then
    ready=true
    break
  fi
  sleep 0.5
done
if [[ "$ready" == false ]]; then
  echo "error: Convia did not answer on port $api_port within 30 seconds." >&2
  exit 1
fi

echo
echo "  Interface  http://localhost:$interface_port"
echo "  API        http://localhost:$api_port"
echo "  No account yet? Create one on the sign-in page."
echo "  Ctrl+C stops both."
echo

cd web
node node_modules/vite/bin/vite.js --port "$interface_port" --strictPort
