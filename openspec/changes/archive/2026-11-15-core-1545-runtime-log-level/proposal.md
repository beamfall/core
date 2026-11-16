# Change the server log level without a restart

Tickets: CORE-1545 (`queue:core:main`)

## Why

The log level is fixed at start-up by `--log-level` / `BEAMFALL_LOG_LEVEL`. Seeing every
request's access line during an investigation means restarting the server at Debug, which drops
open streams and loses the state being investigated.

## What changes

- The operator logger reads its level from a process-wide `slog.LevelVar` set from the configured
  level at start-up.
- On SIGHUP the server re-reads `<data dir>/log-level`. A valid level (`debug`, `info`, `warn`,
  `error`, any case, or an offset such as `info+2`) takes effect at once. A missing file returns the
  server to its configured level. An unreadable file or an invalid value is logged at Warn and the
  current level is kept.
- Each change is logged at Warn with the old level, the new level and its source.
- SIGHUP no longer terminates the server. The file is read only on SIGHUP, so a restart returns to
  the configured level. Platforms without SIGHUP (Windows) keep the start-up level.
- The logs runbook documents the procedure.
- `config.ParseLogLevel` is exported so the reload uses the same parsing as the flag.

No console setting, migration, wire or graph change.
