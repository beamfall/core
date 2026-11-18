# Library scans yield to live playback

Ticket: CORE-2393 (`queue:core:main`).

## Why

ADR-0013 lets a realtime transcode preempt background generation, but library scans never took a
governor lease. On the Tower, three concurrent scans and their ffmpeg probes kept a 4K live
transcode from writing its first segment inside the client's stall timeout (19:08 failure with
scans running against the 19:19 success with scans paused, 2026-11-17). Playback starved behind
work that could have waited.

## What Changes

- New `Governor.BeginPreemptible`: a lease that is refused while a transcode is active and
  cancelled with `ErrPreempted` when one begins, but takes no background-CPU budget slot, so
  scans and generation sweeps keep running side by side on an idle server.
- The library scan job runs under that lease. A scan refused at admission re-queues with its
  checkpoint after the 30 s generation retry delay; a scan preempted mid-run yields with its
  latest per-file checkpoint and resumes from there once playback ends.
- The server logs `background job yielded to live playback` (job id, kind, retry delay) at Info
  each time a running job is preempted. Admission refusals are not logged, so a two-hour movie
  does not write a line every 30 s.
- Governed generation (sweeps, convert, precompute, skip detect, audio ads) shares the same
  wrapper; its behaviour is unchanged.

## Non-goals

- Throttling the I/O of other non-job work (HTTP artwork fetches, API requests).
- Avoiding the re-walk a resumed scan does up to its checkpoint (CORE-2353).
