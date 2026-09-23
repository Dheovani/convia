#!/usr/bin/env bash
#
# Runs Convia and its desktop application together, for local testing.
#
# Starts the local dependencies with Docker Compose, applies the migrations,
# builds the interface into the application, starts the API, and opens the
# application's window.
#
# This is the product as somebody installs it: there is no development server
# and no proxy. The application carries the interface inside its own binary and
# talks to Convia over the public API, which is the arrangement everything
# about the session depends on.
#
# Use ./scripts/dev.sh instead for working on the interface itself - it serves
# web/ with hot reloading, and a change is on the screen when it is saved. Here
# a change needs the interface rebuilt and the window reopened, which is what
# running this again does.
#
# Configuration comes from .env at the repository root, exactly as the README
# describes. The media plane (LiveKit) and the shared channel (Redis) are
# started only when .env configures them.
#
# The application is for Windows, so this script runs under Git Bash there. On
# macOS and Linux it refuses, and points at the other script.
#
# Closing the window stops Convia. The containers are left running, because
# they are the slowest part to start and they hold the database; stop them with
# `docker compose down`.
#
# Usage: ./scripts/app.sh [--skip-migrations] [--skip-interface]

set -euo pipefail

# Where Convia answers when .env says nothing. The application asks which
# installation to connect to and remembers the answer, so this is only what the
# script prints and waits for.
default_api_port=8080

# Where Vite writes and where the binaries embed from. See web/vite.config.ts.
bundle=internal/web/assets/dist/index.html

skip_migrations=false
skip_interface=false
for argument in "$@"; do
  case "$argument" in
    --skip-migrations) skip_migrations=true ;;
    --skip-interface) skip_interface=true ;;
    *) echo "error: unknown argument $argument" >&2; exit 2 ;;
  esac
done

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

# shellcheck source=scripts/common.sh
. "$(dirname "${BASH_SOURCE[0]}")/common.sh"

require_tools docker go node npm curl

if [[ "$(go env GOOS)" != windows ]]; then
  echo "error: Convia's application is for Windows. Use ./scripts/dev.sh to run the service and the interface in a browser." >&2
  exit 1
fi

load_environment

api_port="${CONVIA_HTTP_PORT:-$default_api_port}"
refuse_held_port "$api_port"

start_dependencies

if [[ "$skip_migrations" == false ]]; then
  echo "Applying migrations..."
  go run ./cmd/convia migrate up
fi

install_interface_dependencies

# The application refuses to open a window with no interface in it, so a build
# that was skipped when there is nothing to skip has to be caught here, where
# the remedy can be named.
if [[ "$skip_interface" == true && ! -f "$bundle" ]]; then
  echo "error: there is no interface to reuse. Run this again without --skip-interface." >&2
  exit 1
fi
if [[ "$skip_interface" == false ]]; then
  echo "Building the interface..."
  (cd web && npm run build)
fi

warn_without_media

output="${TMPDIR:-/tmp}/convia-dev"
service="$output/convia$(go env GOEXE)"
application="$output/convia-desktop$(go env GOEXE)"

# The application needs Wails' build tags. Without `production` it compiles a
# stub that opens no window and says the tags are missing; `desktop` selects
# nothing today and is what Wails' own CLI passes. There is no `-H windowsgui`
# here on purpose: in development the console is where the log goes.
echo "Building Convia and its application..."
go build -o "$service" ./cmd/convia
go build -tags desktop,production -o "$application" ./cmd/convia-desktop

"$service" serve &
api=$!
# A background job in a script does not receive Ctrl+C, so the API is stopped
# here instead, with the signal it already shuts down gracefully on.
trap 'kill "$api" 2>/dev/null || true' EXIT

wait_for_convia "$api" "$api_port"

echo
echo "  Convia       http://localhost:$api_port"
echo "  Application  the window that is opening"
echo "  No account yet? Create one from the application."
echo "  Closing the window stops Convia."
echo

# The window runs in the foreground: this script exists to be stopped by
# closing it, the way somebody quits an application.
"$application"
