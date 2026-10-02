# Testing

This is `M24`'s written half: what Convia tests, what it measures, and what it refuses to turn into a number.

## The layers

| Layer | Runs | Needs |
| --- | --- | --- |
| Unit | always | nothing |
| Integration | always in CI, skipped locally without a database | PostgreSQL, and Redis for the shared stores |
| Contract | always | the OpenAPI document, compared with the route table in both directions |
| Fuzz | seeds always, longer on demand | nothing |
| End-to-end | in CI | a browser, a database, a media plane |
| Restore | in CI | a database to lose, an empty one, and an empty Redis |
| Federation | in CI | two databases and two installations |

**Integration tests skip rather than fail when the database is unset**, so `go test ./...` runs on a laptop with nothing installed — and CI always sets it, so the skipping is never what happens where it matters.

### What the end-to-end journeys are for

Nine of them, and **"only the critical ones" is the rule rather than an aspiration**: the component tests cover what each state looks like, and a journey earns its place only by showing something no single layer can. Calls, language, sessions, and conversations.

Conversations were the gap, and it is the shape to watch for: `internal/messages` is exercised from every side, the interface's `Conversation` is exercised with the socket stubbed, and **nothing anywhere showed that what one person writes reaches the other person's open screen.** Every piece was tested; the seam between them was not. That defect fails no request — it leaves two people each believing they can see what the other said.

Running them locally needs the stack the CI job brings up, and one detail is easy to get wrong: **the browser drives `localhost` while `CONVIA_E2E_SHARE_URL` is a real network address.** Invitation links are refused when they point at loopback, because that is Convia itself. Pointing the browser at the network address instead breaks sign-in, since the session cookie is host-only.

## What is measured, and what is gated

Coverage is **measured per package and gated on presence**. Those are two different things on purpose.

**The gate is: every package that ships code has tests.** Not a percentage. A percentage rewards covering whatever is cheapest and says nothing about whether the covered part is the part that matters — an eighty per cent that is all getters passes, and a sixty per cent that is every invariant fails.

What the gate catches is the failure that actually happened, twice:

- **`internal/secret`** is the one place keeping one credential family from being accepted as another, and it had no tests until `M23-014`.
- **`internal/transaction`** decides whether an event is announced for a change that rolled back, and it had none until `M24-009`.

Both were found by somebody looking. Both are small enough to look like plumbing, which is exactly why nobody looked sooner. A package with no tests is a package where nothing would notice a change.

### The baseline

Measured on the full suite against real PostgreSQL and Redis, as a starting point rather than a target:

| Band | Packages |
| --- | --- |
| 90–100% | `ratelimit`, `secret`, `config`, `api`, `desktop/app`, `web` |
| 75–89% | `server`, `erasure`, `presence`, `media/livekit`, `events/redis`, `desktop/client`, `applications`, `database`, `idempotency`, `desktop/secrets`, `presence/redis`, `sessions`, `events/serving`, `accounts`, `telemetry` |
| 60–74% | `events/journal`, `users`, `media`, `desktop/installations`, `peers`, `messages`, `operator` |
| under 60% | `rooms`, `events`, `participants`, `invitations`, `webhooks`, `export`, `calls`, `credentials`, `departure` |

**The bottom band is where to look, not where to panic.** Most of it is transport and error branches rather than untested invariants — `departure` at 28% is the lowest and its one journey is covered end to end, with the uncovered part being the failure paths of five domains it calls. Raising it means deciding which of those failures are worth simulating, which is `M24-013`'s question rather than a coverage question.

## What the tests are shaped around

**A test says what would go wrong, not what the code does.** A name like `TestAStolenSessionDoesNotThrottleThePhoneItWasStolenFrom` is a sentence somebody can disagree with; `TestRateLimiter` is not.

**Mutation is how a test earns its place.** Every property worth stating is checked by breaking the code and watching the test fail — and it has caught tests that passed for the wrong reason more than once, including two written in this repository that encoded the bug they were meant to prevent.

**Fuzz targets are pointed at forgery rather than crashes.** The signature base between installations, the credential parser, and webhook signatures: places where a defect is somebody getting in rather than a process going down. Two of them found something on their first run, and both findings were the same shape — a primitive whose guarantee depends on an alphabet validated somewhere else, with nothing saying so.

## Finding the transitions nothing exercises

Asking which state transitions are untested by **searching test names** does not work: six of eight apparent gaps turned out to be covered under names the search did not predict. The method that does work is coverage read block by block, filtered to the functions that move something from one state to another — `scratchpad/uncovered_transitions.py` in a working copy, over a profile from the full suite.

**That profile has to be taken with `-coverpkg=./...`**, and this is the trap. Without it Go credits a block only to the package whose test binary ran it, so a service exercised through a sibling's fixture — `internal/invitations` wires the real `participants.Service` — reads as entirely dead. The naive profile reported around 165 transitions nothing reached; none of them were real.

What the corrected profile found was 108 unreached blocks in the domain layer, most of them infrastructure failure paths that no test can reach without fault injection, and **eleven genuine refusals**: giving a role to somebody who already left, admitting a guest to a call that ended or with no invitation at all, the sweep rewriting a call that was already over, declining an invitation the application had withdrawn, and banning an identifier that names nobody.

### What mutating them showed

Twelve mutations, ten caught. The two that survived did so **by construction, and that is worth stating rather than hiding**:

- **A rule enforced in two layers survives either half.** `if call.Ended()` in the service and the status check inside `lockCall` are the same rule, and the second is the one that closes the race between ending a call and joining it. The first is what keeps an ended call from costing a transaction. So each has its own test, and the mutation that proves the pair breaks both together.
- **A shape check is an early-out, not the answer.** Deleting `ValidID` lets a malformed identifier reach the query, where it matches no row and comes back as the same `ErrNotFound`. The test pins the answer, which is the contract; the check saves a round trip.

**And one mutation was silently wrong before the harness caught it.** `if call.Ended()` appears twice in `internal/participants/service.go`, character for character, in `Join` and in `AdmitGuest`. Replacing the first occurrence patches `Join` and leaves `AdmitGuest` guarded — a mutation that proves nothing while reporting that it proved something. Every anchor now has to match exactly once or the run refuses it.

## Quarantine

A flaky test can be switched off. It cannot be switched off quietly.

```go
// QUARANTINE(owner: @someone, issue: #412, until: 2026-11-15)
// The relay races with Redis key expiry under -shuffle.
t.Skip("quarantined: see QUARANTINE above")
```

`.github/scripts/check_quarantine.py` refuses the build without all three, and the date is the part with teeth: **it has to be in the future, and no more than ninety days out.** When it passes, CI fails until somebody fixes the test, extends the date deliberately, or deletes the test and admits the coverage is gone. All three are acceptable answers. Silence is not — a quarantine without a deadline is a deletion nobody had to argue for, and the test goes on existing so that nobody notices it went.

**The gate reads Go and TypeScript alike** — `*_test.go`, `*.test.ts(x)` and `*.spec.ts(x)` — and it reads TypeScript because of what happened when it did not. It was written covering Go only, and the first flaky test found afterwards was a component test, which it could not see: `it.skip`, `xit` and `it.todo` were all ways to switch a test off with nothing saying so. That is `M24-017`, and it is the only thing quarantined today.

**Every other skip has to be a declared shape.** Convia's integration tests skip when their dependency is unset, which is a different thing, so the script carries a short list of recognised shapes and refuses anything else. Hiding a flake behind `t.Skip("TODO")` therefore needs somebody to widen that list in a diff a reviewer reads. Proved by injecting five bad shapes — a bare skip, a quarantine with no marker, a marker whose date had passed, a deadline five years out, and a marker missing its issue — and watching each one refused.

**One test is quarantined**: `Peers.test.tsx > offers to forget a room it could not leave`, until 2026-11-15. It failed about half of full-suite runs and never on its own; two races were found and fixed and it still fails roughly one run in five, so the cause is not either of those. The behaviour it covers is uncovered while it is quarantined, which is what the deadline is for.

## Where the time goes

CI runs the suite with `-json` and pipes it through `.github/scripts/report_durations.py`, which prints the slowest packages and tests, and, when something failed, the failures in a form somebody can read — `-json` keeps the exit code and ruins the log, so the report puts the log back.

**It reports; it does not judge.** There is no duration threshold, because a threshold nobody chose is a threshold somebody silences. What it makes visible is that a suite gets slow one test at a time, each addition too small to argue with: the run this was built against took **507s across 40 packages and 2361 tests**, with `internal/participants` alone at 59s, and 390 tests over half a second.

## Losing the installation on purpose

CI drops the schema of a populated Convia and puts it back from a `pg_dump`, as `.github/scripts/restore_exercise.sh`. **A backup nobody has restored is a file**, and the day it is needed is not the day to find out which step is wrong.

**What it is built to catch is the restore that looks like it worked**, which is the normal failure rather than a rare one, so it never checks that the dump exists. It signs in as somebody who existed before the loss, reads back what they wrote, writes something new, and asks for presence — the password digests, the rows, the identity sequences, and Redis, in that order.

The loss is not only PostgreSQL. Presence lives in Redis and the media plane holds rooms for calls that are now over, so the restore is handed a **new, empty Redis and no media plane**: an installation that only works because its cache survived has not been recovered.

Proved by breaking the restore twice:

- `--schema-only` — `pg_restore` says nothing, the schema version is right, and Convia refuses to start because it cannot read the journal's floor.
- `--exclude-table-data=messages` — `pg_restore` says nothing, the schema version is right, Convia starts, `/health` is green, **the sign-in succeeds**, and the history comes back empty. Nothing but reading it back notices.

The procedure for a person at three in the morning is [`docs/runbooks/restoring-from-a-backup.md`](runbooks/restoring-from-a-backup.md).

## Not built

- **Coverage per critical behaviour** rather than per package (`M24-009`'s other half). Knowing that `rooms` is at 57% does not say whether the moderation rules are covered.
- **Flake *trends*.** Durations are reported per run (above), and quarantine is enforced (above), but nothing compares a run with the one before it, so a test that gets slowly slower is still noticed by whoever loses patience first. That needs somewhere to keep a run's numbers between runs, which this repository has nowhere to put.
- **Point-in-time recovery.** The restore exercise covers a logical dump, which recovers to the moment it was taken. Recovering to the second before a bad write needs WAL archiving, and where the segments live and how long they are kept is a deployment decision Convia does not make.
