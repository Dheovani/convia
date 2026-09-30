# Restoring from a backup

**When:** the database is lost, corrupted, or something was deleted that cannot be put back by hand.

This procedure runs in CI on every change, as `.github/scripts/restore_exercise.sh`. That is deliberate: a restore procedure nobody has run is a document, and the day it is needed is not the day to find out which step is wrong.

## What Convia keeps, and where

| Where | What | Survives a restore? |
| --- | --- | --- |
| PostgreSQL | everything that is a record: accounts, rooms, memberships, messages, invitations, credentials, the event journal | **only this is backed up** |
| Redis | presence, and the relay between instances | no, and it does not need to |
| The media plane | rooms for calls that are happening now | no, and it does not need to |

**Only PostgreSQL is backed up, and that is the whole design.** Presence is what is true this second and is rebuilt by the pages that report it. The media plane holds conversations that are happening, and a conversation does not survive the installation being lost either way. A backup that included them would restore claims about a world that has moved on.

## Taking a backup

```
pg_dump --format=custom --no-owner --no-privileges --file=convia-$(date +%Y%m%d%H%M).dump "$CONVIA_DATABASE_URL"
```

`--format=custom` so it can be restored selectively and in parallel. `--no-owner --no-privileges` so it restores into a database whose roles are named differently, which is the normal case when restoring somewhere new.

**Where the dump is kept is a deployment decision Convia does not make**, and it is the one thing this runbook cannot check for you. A dump on the same disk as the database is not a backup.

## Restoring

1. **Make an empty database.** Do not restore over a database that has anything in it; `pg_restore` will half-succeed and leave you worse off than before.

   ```
   psql "$ADMIN_URL" --command 'CREATE DATABASE convia_restored'
   ```

2. **Restore, and refuse to continue on an error.**

   ```
   pg_restore --no-owner --no-privileges --exit-on-error --dbname="$RESTORED_URL" convia-….dump
   ```

   Without `--exit-on-error`, `pg_restore` reports problems and carries on, which is how a partial restore becomes a running installation.

3. **Check the schema is the one the binary expects**, before starting it.

   ```
   CONVIA_DATABASE_URL="$RESTORED_URL" convia migrate status
   ```

   Anything pending means the backup predates the binary. **Do not run `migrate up` to close the gap without reading what the pending migrations do** — restoring an old backup into a new schema is a different operation from a restore, and some of them cannot be undone.

4. **Start Convia against it, with a Redis of its own.** Point `CONVIA_REDIS_URL` at an empty Redis rather than one another installation is using: two installations sharing a channel will relay each other's events, and a restored one announcing an old world into a live one is worse than being down.

## Then prove it, because `pg_restore` printing nothing proves nothing

**The failure to expect is the restore that looks like it worked.** Run these four in order, and stop at the first that does not answer:

1. **Somebody who existed before the loss signs in.** `POST /v1/sessions` with a known account. This is the password digests, and it is the failure with no error message: Convia starts, `/health` is green, and every sign-in is refused.
2. **What they wrote reads back.** `GET /v1/me/rooms/{id}/messages` for a room you know had something in it. An empty answer here with a successful sign-in means the schema came back and the rows did not.
3. **A new write is accepted.** `POST /v1/me/rooms/{id}/messages`. This is the identity sequences. A restore that leaves them behind passes both checks above and fails an hour later, on somebody else's request.
4. **Presence answers.** `GET /v1/me/people/presence?user_id=…`. Against the empty Redis. It should answer that nobody is around, not fail — an installation that refuses to say who is present until a cache warms is not recovered, it is waiting.

If all four answer, the installation is back.

## What this does not cover

**Point-in-time recovery.** A logical dump restores to the moment it was taken, so a bad write is recoverable only as far back as the last dump. Recovering to the second before a mistake needs WAL archiving, which is a deployment decision — where the segments live and how long they are kept — that Convia does not make and this repository does not exercise.
