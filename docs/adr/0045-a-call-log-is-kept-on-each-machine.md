# 0045 — A call log is kept on each machine

## Status

Accepted

## Date

2026-10-21

## Context

A missed call is a line of the conversation on the machine of the person who missed it alone ([ADR 0044](0044-a-call-rings-for-30-seconds.md)). A person also wants to find the calls they made and answered, as a telephone lists them, and what reading and presence say stays on the reader's machine ([ADR 0032](0032-reading-and-presence-stay-on-the-readers-machine.md)).

## Decision

**Every call that ends is kept in a call log on this person's machine**: whom it was with, which of the two called, whether the camera was on, how it ended, when it rang and, if it was answered, when, and when it ended. **The log is never sent**, nor is any line of it: each machine keeps the calls it took part in, as it saw them.

**The interface shows the log as a tab of its own**, the latest call first, a missed one apart; from it a person calls back. A missed call stays a line of the conversation too.

## Consequences

- The two people of a call each keep their own line of it, which may differ: one may have missed what the other saw unanswered.
- Deleting a conversation's messages leaves its calls in the log.
