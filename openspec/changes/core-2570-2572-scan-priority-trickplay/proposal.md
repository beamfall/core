# Prioritize scans and clarify background job progress

Tickets: CORE-2570, CORE-2571, CORE-2572 (`queue:core:main`).

## Why

Generation sweeps can hold job workers while a library scan waits. Trickplay generation also
decodes unnecessary frames, and the Console describes elapsed time across waits and yields as
running time. Scans should get timely access to workers, and operators should see honest timing.

## What Changes

- CORE-2570: generate trickplay on one thread, decoding keyframes first with verified VA-API/QSV
  hardware, then retry software keyframes, then software every-frame decode for file output.
  Preserve the existing `trickplay:v1` layout and FFmpeg implementation.
- Run the full Generated Assets sweep nightly at 03:00 through the existing schedule/job machinery,
  one item at a time, instead of after every scan and at boot. Manual run-now remains a full sweep.
- After a successful scan, enqueue a non-blocking catch-up only if the catalog's maximum Media
  rowid advanced. Start above the pre-scan watermark and checkpoint the last completed rowid;
  yield to scans and realtime transcodes under the existing sweep governor. A watermark error
  logs a warning and skips catch-up rather than accidentally scanning the full catalog.
- Skip trickplay when sheets already exist for the fingerprint, no video source is available,
  duration is shorter than the 10-second default interval, or the source is a disc image
  (`.iso`, `.img`, a `BDMV`/`VIDEO_TS` path segment, or the `iso` container, case-insensitive).
- CORE-2571: give scans priority 5 (above sweeps at 0, below foreground/playback demand at 10).
  Running or runnable queued scans preempt the artwork, generated-assets, decode-verification
  and skip-detection sweeps. Sweeps retain checkpoints and resume after scans finish. Scans
  continue yielding to realtime transcodes; convert and precompute work keep their existing policy.
- CORE-2572: label running jobs with time since first start and show repeated attempts; label
  finished multi-attempt jobs with elapsed duration over their attempts, in all five locales.

## Impact

- Governor, job-worker ticker, runnable-job store lookup, insertion-order Media reads, scan job
  construction, Generated Assets handlers and built-in schedule registration.
- Console presentation only: existing timestamps and attempts, no wire or GraphQL changes.
- Deltas extend background-job-playback-yield, console-job-queue and web-playback-start
  (which already covers trickplay manifests). No migrations or trickplay layout changes.
- Local checks follow the bounded task brief; commits, full gates and publication are excluded.

The canonical `openspec/specs/` search contains no requirement for a Generated Assets sweep after
scans or at boot; the scheduling requirements below are additions, not a conflicting second rule.
