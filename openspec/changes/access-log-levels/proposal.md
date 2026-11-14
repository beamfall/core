# Access lines are quiet unless a request failed or was slow

Ticket: CORE-1159 (`queue:core:main`).

## Why

The server writes one `http request` line at Info for every request (change
`operator-diagnosability`, CORE-0733). A single web page load is dozens of requests, so at the
default log level the lines an operator needs (a failure, a refused sign-in, a slow page) are
buried under routine successes, and the log grows with traffic instead of with trouble.

## What Changes

- The access line keeps its fields and is still produced once per request, but its level now
  follows the outcome:
  - Debug for a routine request (status below 500, response started within one second);
  - Info for a request answered 5xx;
  - Warn for a request whose response took one second or longer to start, whatever its status.
- "Slow" is measured to the start of the response, not to its end, so a long download, a playback
  byte stream or an open event stream is not reported as slow.
- The line gains `first_byte_ms`, the time to the start of the response.
- A line whose level is disabled costs no field formatting.
- A request the client abandoned (status 499) is a routine request.
- `--log-level debug` (or `BEAMFALL_LOG_LEVEL=debug`) restores a line for every request.

The request counters on `/metrics` still count every request.

## Wire compatibility

None; this changes operator logs only. An operator who parses every access line from the default
log level must run at debug level to keep seeing routine requests.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `operator-diagnosability`: the access line's level follows the outcome. The capability's first
  change is not yet archived, so this delta adds the level requirement beside it.
