# A flapping live watch backs off and warns once

Ticket: CORE-1170 (`queue:core:main`).

## Why

The quality sweep (CORE-1213) made a Library root's live watch restart itself with a capped
exponential backoff after it fails. The backoff and the degrade warning both reset as soon as the
watch comes back up. A root whose watch comes up and dies again straight away (a failing disk, a
network mount that accepts watches and then drops them) therefore restarts every second forever
and writes a warning and a "restored" line each time.

## What Changes

- The restart backoff is only forgotten once a re-established watch has stayed up for one minute.
  A watch that dies sooner keeps climbing the backoff, up to the existing five-minute cap.
- The degrade warning for a root is written at most once per five minutes. Failures in between are
  counted and reported on the next warning (`failures_since_last_warning`); each is still
  available at debug level.
- "live watch restored" is written only for an outage whose warning was written.
- The event-queue overflow line is written at most once per five minutes per root; every overflow
  is still recovered.

Watch status reported to clients is unchanged and still reflects every failure.

## Wire compatibility

None; restart timing and operator logs only.

## Capabilities

### New Capabilities

- `live-watch-recovery`: how a failing live watch backs off and reports.

### Modified Capabilities

None.
