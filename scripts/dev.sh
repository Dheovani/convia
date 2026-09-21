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

# shellcheck source=scripts/common.sh
. "$(dirname "${BASH_SOURCE[0]}")/common.sh"

require_tools docker go node npm curl
load_environment

api_port="${CONVIA_HTTP_PORT:-$proxied_api_port}"
if [[ "$api_port" != "$proxied_api_port" ]]; then
  echo "error: CONVIA_HTTP_PORT is $api_port, but web/vite.config.ts proxies the interface to $proxied_api_port." >&2
  exit 1
fi

for port in "$api_port" "$interface_port"; do
  refuse_held_port "$port"
done

start_dependencies

if [[ "$skip_migrations" == false ]]; then
  echo "Applying migrations..."
  go run ./cmd/convia migrate up
fi

install_interface_dependencies

warn_without_media

binary="${TMPDIR:-/tmp}/convia-dev/convia$(go env GOEXE)"
echo "Building Convia..."
go build -o "$binary" ./cmd/convia

"$binary" serve &
api=$!
# A background job in a script does not receive Ctrl+C, so the API is stopped
# here instead, with the signal it already shuts down gracefully on.
trap 'kill "$api" 2>/dev/null || true' EXIT

wait_for_convia "$api" "$api_port"

echo
echo "  Interface  http://localhost:$interface_port"
echo "  API        http://localhost:$api_port"
echo "  No account yet? Create one on the sign-in page."
echo "  Ctrl+C stops both."
echo

cd web
node node_modules/vite/bin/vite.js --port "$interface_port" --strictPort
