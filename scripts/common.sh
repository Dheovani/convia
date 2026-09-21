#!/usr/bin/env bash
#
# What the development scripts in this directory both need.
#
# Source it:
#
#     . "$(dirname "${BASH_SOURCE[0]}")/common.sh"
#
# There are two scripts because there are two ways to run Convia locally, and
# they differ in what they start rather than in how they read .env or wait for
# a port. This holds the part that is the same, so that a fix to it is one fix.
#
# It is the sibling of common.ps1 and the two are kept in step.

# load_environment reads .env into this process, which is where Convia's
# configuration comes from. It is not optional: Convia reads the environment
# rather than a file, so a missing .env is a missing configuration.
load_environment() {
  if [[ ! -f .env ]]; then
    echo "error: .env does not exist. Create it from the template with: cp .env.example .env" >&2
    exit 1
  fi
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
}

# require_tools stops before anything is started when something it needs is
# absent, rather than halfway through with a container already running.
require_tools() {
  for tool in "$@"; do
    if ! command -v "$tool" >/dev/null 2>&1; then
      echo "error: $tool is not installed or is not on PATH." >&2
      exit 1
    fi
  done
}

# port_in_use reports whether something already answers on a local port.
port_in_use() {
  (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null
}

# refuse_held_port names what to do about a port something else holds, which is
# usually an earlier run of one of these scripts.
refuse_held_port() {
  if port_in_use "$1"; then
    echo "error: port $1 is already in use. Stop whatever holds it - possibly an earlier Convia - and run this again." >&2
    exit 1
  fi
}

# start_dependencies starts the containers Convia needs, according to what .env
# configures. The media plane and the shared channel are opt-in profiles,
# because Convia runs without either: starting what is not configured would
# mean a container nothing talks to.
start_dependencies() {
  local profiles=()
  if [[ -n "${CONVIA_LIVEKIT_URL:-}" ]]; then
    profiles+=(--profile media)
  fi
  if [[ -n "${CONVIA_REDIS_URL:-}" ]]; then
    profiles+=(--profile shared)
  fi

  echo "Starting the dependencies..."
  docker compose "${profiles[@]}" up --detach --wait
}

# install_interface_dependencies installs web/'s dependencies when they are
# missing, or when the lockfile changed since they were installed.
install_interface_dependencies() {
  local installed=web/node_modules/.package-lock.json
  if [[ ! -f "$installed" || web/package-lock.json -nt "$installed" ]]; then
    echo "Installing the interface dependencies..."
    (cd web && npm ci)
  fi
}

# warn_without_media says once that calls cannot be started, rather than
# leaving somebody to find out by pressing the button.
warn_without_media() {
  if [[ -z "${CONVIA_LIVEKIT_URL:-}" ]]; then
    echo "warning: no media plane is configured in .env, so calls cannot be started. See docs/media.md." >&2
  fi
}

# wait_for_convia waits until Convia answers, and says what happened when it
# does not. A process that exited is reported as having exited rather than as a
# timeout: the two have different causes and the same symptom.
wait_for_convia() {
  local pid="$1" port="$2" attempts="${3:-60}"

  for _ in $(seq 1 "$attempts"); do
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "error: Convia exited before it was ready. Its output is above." >&2
      exit 1
    fi
    if curl --silent --fail --output /dev/null "http://127.0.0.1:$port/health"; then
      return 0
    fi
    sleep 0.5
  done

  echo "error: Convia did not answer on port $port within $((attempts / 2)) seconds." >&2
  exit 1
}
