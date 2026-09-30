#!/usr/bin/env bash
#
# Lose an installation on purpose, and put it back from a backup.
#
# This is `M24-016`. A backup nobody has restored is a file, and the way that is
# usually discovered is on the day it matters. The exercise is therefore the
# deliverable: it runs in CI, against a real PostgreSQL, and it fails the build
# when a restored Convia is not a working Convia.
#
# **What it is built to catch is the restore that looks like it worked.** A
# `pg_restore` that prints no error and leaves the installation broken is the
# normal failure, not a rare one: identity sequences left behind so the next
# insert collides with a row that already exists, constraints restored without
# their triggers, a schema version the binary does not expect, or -- the one
# with no error message at all -- a Convia that starts, answers /health, and
# refuses every sign-in because the password digests came back mangled.
#
# So it does not check that the dump file exists. It signs in as somebody who
# existed before the loss, reads back what they wrote, and writes something new.
#
# Both halves have been proved by breaking the restore on purpose:
#
#   --schema-only        pg_restore says nothing, the schema version is right,
#                        and Convia refuses to start -- it cannot read the
#                        journal's floor. Caught before the first request.
#   --exclude-table-data pg_restore says nothing, the schema version is right,
#                        Convia starts, /health is green and the sign-in works.
#     =messages          The history comes back empty, and that is the only
#                        thing that notices.
#
# **The loss is not only PostgreSQL.** Presence and the relay live in Redis, and
# the media plane holds rooms that no longer exist. A real recovery arrives with
# an empty Redis and a media server that never heard of the calls in the backup,
# so this restores into exactly that: new Redis, no media plane. An installation
# that only works when its cache survived has not been recovered.

set -euo pipefail

: "${CONVIA_RESTORE_ORIGIN_URL:?the database to back up}"
: "${CONVIA_RESTORE_TARGET_URL:?the empty database to restore into}"
: "${CONVIA_RESTORE_BINARY:=./convia}"
: "${CONVIA_RESTORE_PORT:=8099}"
: "${CONVIA_RESTORE_REDIS_URL:=}"

dump="$(mktemp -d)/convia.dump"
password='correct horse battery staple'
username="restored$(date +%s)"
said='Written before the installation was lost.'

serving=""

stop() {
  if [ -n "$serving" ] && kill -0 "$serving" 2>/dev/null; then
    kill "$serving" 2>/dev/null || true
    wait "$serving" 2>/dev/null || true
  fi
  serving=""
}

trap stop EXIT

# serve starts a Convia against a database and waits for it to answer, failing
# the exercise rather than hanging when it does not.
serve() {
  local url="$1" log="$2"

  CONVIA_ENVIRONMENT=development \
  CONVIA_HTTP_HOST=127.0.0.1 \
  CONVIA_HTTP_PORT="$CONVIA_RESTORE_PORT" \
  CONVIA_DATABASE_URL="$url" \
  CONVIA_REDIS_URL="$CONVIA_RESTORE_REDIS_URL" \
    "$CONVIA_RESTORE_BINARY" serve > "$log" 2>&1 &
  serving=$!

  for _ in $(seq 1 30); do
    if curl --silent --fail --output /dev/null "http://127.0.0.1:$CONVIA_RESTORE_PORT/health"; then
      return 0
    fi
    if ! kill -0 "$serving" 2>/dev/null; then
      echo "Convia stopped before it was ready:"
      cat "$log"
      return 1
    fi
    sleep 1
  done

  echo "Convia never became ready:"
  cat "$log"
  return 1
}

ask() {
  curl --silent --show-error --output /dev/stdout \
    --write-out '\n%{http_code}' "$@"
}

at="http://127.0.0.1:$CONVIA_RESTORE_PORT"

echo "== what is lost =="

CONVIA_ENVIRONMENT=development CONVIA_DATABASE_URL="$CONVIA_RESTORE_ORIGIN_URL" \
  "$CONVIA_RESTORE_BINARY" migrate up
serve "$CONVIA_RESTORE_ORIGIN_URL" /tmp/convia-origin.log

registered="$(ask --request POST "$at/v1/accounts" \
  --header "Origin: $at" --header 'Content-Type: application/json' \
  --data "{\"username\":\"$username\",\"password\":\"$password\"}" \
  --dump-header /tmp/registered.headers)"

if [ "$(printf '%s' "$registered" | tail -n1)" != "201" ]; then
  echo "could not register somebody to lose: $registered"
  exit 1
fi

cookie="$(grep -i '^set-cookie:' /tmp/registered.headers | head -n1 | sed 's/^[Ss]et-[Cc]ookie: *//; s/;.*//')"
if [ -z "$cookie" ]; then
  echo "registering returned no session cookie"
  exit 1
fi

room="$(ask --request POST "$at/v1/me/rooms" \
  --header "Origin: $at" --header "Cookie: $cookie" --header 'Content-Type: application/json' \
  --data '{"name":"Before the loss"}')"
roomID="$(printf '%s' "$room" | head -n-1 | sed 's/.*"id":"\([^"]*\)".*/\1/')"

if [ -z "$roomID" ]; then
  echo "could not open a room: $room"
  exit 1
fi

posted="$(ask --request POST "$at/v1/me/rooms/$roomID/messages" \
  --header "Origin: $at" --header "Cookie: $cookie" --header 'Content-Type: application/json' \
  --data "{\"body\":\"$said\"}")"

if [ "$(printf '%s' "$posted" | tail -n1)" != "201" ]; then
  echo "could not write anything to lose: $posted"
  exit 1
fi

echo "an account, a room, and a message exist"

echo "== the backup =="
pg_dump --format=custom --no-owner --no-privileges --file="$dump" "$CONVIA_RESTORE_ORIGIN_URL"
echo "dumped $(du -h "$dump" | cut -f1)"

stop

echo "== the loss =="
# The origin is not dropped politely. Whatever is only in the backup from here
# is all there is, which is the condition the exercise is about.
psql "$CONVIA_RESTORE_ORIGIN_URL" --quiet --command 'DROP SCHEMA public CASCADE; CREATE SCHEMA public'
echo "the original database is gone"

echo "== the restore =="
pg_restore --no-owner --no-privileges --exit-on-error \
  --dbname="$CONVIA_RESTORE_TARGET_URL" "$dump"
echo "restored without error, which proves nothing yet"

# The schema version the binary expects. A restore that brings back an older
# one starts and then fails on the first query touching the missing column.
status() {
  CONVIA_ENVIRONMENT=development CONVIA_DATABASE_URL="$CONVIA_RESTORE_TARGET_URL" \
    "$CONVIA_RESTORE_BINARY" migrate status 2>&1
}

pending="$(status | grep -ci 'pending' || true)"
if [ "$pending" != "0" ]; then
  echo "the restored schema is not the one this binary expects:"
  status
  exit 1
fi
echo "the restored schema is the one this binary expects"

# A new Redis, and no media plane: a recovery does not get its cache back.
serve "$CONVIA_RESTORE_TARGET_URL" /tmp/convia-restored.log

echo "== is it a working Convia? =="

signed="$(ask --request POST "$at/v1/sessions" \
  --header "Origin: $at" --header 'Content-Type: application/json' \
  --data "{\"username\":\"$username\",\"password\":\"$password\"}" \
  --dump-header /tmp/signed.headers)"

if [ "$(printf '%s' "$signed" | tail -n1)" != "201" ]; then
  echo "somebody who existed before the loss cannot sign in: $signed"
  echo "--- the restored Convia said ---"
  cat /tmp/convia-restored.log
  exit 1
fi
echo "somebody who existed before the loss signs in, so the password digests survived"

restored="$(grep -i '^set-cookie:' /tmp/signed.headers | head -n1 | sed 's/^[Ss]et-[Cc]ookie: *//; s/;.*//')"

history="$(ask --request GET "$at/v1/me/rooms/$roomID/messages" \
  --header "Origin: $at" --header "Cookie: $restored")"

if ! printf '%s' "$history" | grep --quiet "$said"; then
  echo "what was written before the loss did not come back: $history"
  exit 1
fi
echo "what was written before the loss reads back"

# The sequence test. A restore that leaves identity sequences behind succeeds at
# everything above and fails on the next write, which is the failure an operator
# meets an hour later rather than during the recovery.
after="$(ask --request POST "$at/v1/me/rooms/$roomID/messages" \
  --header "Origin: $at" --header "Cookie: $restored" --header 'Content-Type: application/json' \
  --data '{"body":"Written after the recovery."}')"

if [ "$(printf '%s' "$after" | tail -n1)" != "201" ]; then
  echo "the restored installation cannot take a new message: $after"
  echo "--- the restored Convia said ---"
  cat /tmp/convia-restored.log
  exit 1
fi
echo "the restored installation takes a new message, so the sequences came with it"

# Presence lives in Redis, which is empty. It has to answer rather than fail:
# an installation that refuses to say who is around until somebody warms a cache
# is not recovered, it is waiting.
me="$(ask --request GET "$at/v1/me" --header "Origin: $at" --header "Cookie: $restored")"
userID="$(printf '%s' "$me" | head -n-1 | sed 's/.*"user_id":"\([^"]*\)".*/\1/')"

presence="$(ask --request GET "$at/v1/me/people/presence?user_id=$userID" \
  --header "Origin: $at" --header "Cookie: $restored")"

if [ "$(printf '%s' "$presence" | tail -n1)" != "200" ]; then
  echo "presence does not answer against an empty Redis: $presence"
  exit 1
fi
echo "presence answers against an empty Redis"

echo
echo "RESTORE_EXERCISE_OK"
