# Subscribers falling behind

**Alert:** `convia.event.streams.ended{reason="behind"}` above zero.

**What happened:** a subscriber stopped reading its event stream faster than Convia filled it, the queue reached its depth, and Convia ended the stream rather than discarding events silently.

**Why this one is worth waking for.** It is the only condition in Convia that is invisible everywhere else. The subscriber reconnects, asks the journal for what it missed, and every other signal — error rate, latency, saturation — goes back to looking healthy. Nothing else will tell you it happened.

## What it means for the people using it

**Somebody's view of a conversation had a hole in it.** Between the stream ending and the reconnect completing, an application or a person's client was not told about messages, calls or people arriving. The `Ending` says so explicitly, which is what lets a client recover rather than quietly believing it saw everything.

**It is recoverable and it is not automatic.** Reconnecting with the cursor of the last event received fills the gap from the journal — but the journal keeps a day. A subscriber that was behind and then stayed disconnected past that is told its cursor is too old, and it has to read the current state through the API instead.

## Find out which

```
convia.event.streams.ended{reason="behind"}
```

carries nothing about whose stream it was, deliberately — the metric counts the installation's behaviour, not anybody's use of it. The log line is where the detail is. Every stream logs when it opens and when it closes, with how many events it delivered, and the closing line is correlated by `request_id` and `trace_id`.

Search the logs for the stream closing and read backwards from it.

## Decide which of three it is

1. **One subscriber, once.** A garbage collection pause, a slow network, a laptop that slept. The queue exists for exactly this and it was not enough. Nothing to do.
2. **One subscriber, repeatedly.** That application or client is not reading fast enough — it is doing work in the loop that reads the stream, or it is on a connection that cannot keep up. It is their side to fix, and the delivered count in the closing line is the evidence.
3. **Many subscribers at once.** Convia is publishing faster than anything can consume, which is a Convia problem. Look at the event rate and at what is producing it: a tenant in a loop, or a room with an unusual number of people in it.

The third is the one that is Convia's. The first two are reported, not fixed.

## What not to do

**Do not raise the queue depth to make the alert stop.** A deeper queue does not make a subscriber read faster; it makes the gap larger when it finally gives up, and it spends memory per stream to do it. The depth is a decision about how long a hesitation may last, not a dial for quietness.

## Related

- [`../events.md`](../events.md) — the limits, and what a client does with an ending.
- [`../observability.md`](../observability.md) — the instruments this alert is written over.
