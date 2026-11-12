# Console dashboard: live session detail, host graphs, play history

Ticket: CORE-1195 (`queue:core:main`).

## Why

An owner cannot answer "what is my server doing and who is using it" from the console. Live
sessions say only "direct play" or "transcoding", with no reason, no bitrate and no progress bar,
and they do not refresh on their own. Nothing graphs the host's CPU, memory or network load.
Play history exists only as raw engagement events: there is no list of plays, no top users and no
most-played items.

## What Changes

- The playback service remembers, in memory, the last playback decision it made for each
  (session, item): the play method (`direct`, `remux` or `transcode`), a cause from a closed
  vocabulary saying why direct play was refused, and the delivered bitrate. `liveSessions` reports
  these as `playMethod`, `playReason` and `bitrate`. The `playbackInfo` wire `reason` is unchanged.
- A host sampler in the server process records CPU, memory and network throughput every ten
  seconds into a ring that holds a little over one hour. A new admin field `hostMetrics` returns
  the ring. Samples never leave the server and are not persisted.
- A new admin field `playHistory` lists plays derived from the existing playback events in the
  events database, filterable by profile, library and date range. A play is a run of progress
  events for one profile and item with no gap longer than 30 minutes.
- A new admin field `playStats` returns top profiles and most-played items for a window of 7, 30
  or 90 days.
- The console Live sessions page shows profile, item, device, play method with its reason,
  bitrate and a progress bar, and refreshes on a timer. It shows CPU, memory and network graphs.
  A new History page shows the play list with its filters and the two rankings.

## Non-goals

- No migration and no new persisted data. The play method of a past play is not recorded, so
  history does not show it; a ticket covers persisting it.
- No per-process or per-stream resource attribution. The graphs are host-wide.
- No change to the Activity page or the Manage locked state, which other lanes own.

## Impact

- `internal/playback`, `internal/hostmetrics` (new), `internal/store`, `internal/graph`,
  `internal/server` wiring, `internal/web/app` console.
- Hidden or restricted items keep the existing reveal rules: a play of an item the operator has
  not revealed is counted but carries no title.
