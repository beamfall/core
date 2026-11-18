# Prioritize scans and clarify background job progress

Tickets: CORE-2570, CORE-2571, CORE-2572 (`queue:core:main`).

## Why

Generation sweeps can hold job workers while a library scan waits. Trickplay generation also
decodes unnecessary frames, and the Console describes elapsed time across waits and yields as
running time. Scans should get timely access to workers, and operators should see honest timing.

## What Changes

- CORE-2570: decode only keyframes for sprite sheets; use verified VA-API/QSV hardware decode
  with a software retry, preserving the `trickplay:v1` layout. This implementation is already
  present in the checkout and is preserved by this change.
- CORE-2571: give scans priority 5 (above sweeps at 0, below foreground/playback demand at 10).
  Running or runnable queued scans preempt the artwork, generated-assets, decode-verification
  and skip-detection sweeps. Sweeps retain checkpoints and resume after scans finish. Scans
  continue yielding to realtime transcodes; convert and precompute work keep their existing policy.
- CORE-2572: label running jobs with time since first start and show repeated attempts; label
  finished multi-attempt jobs with elapsed duration over their attempts, in all five locales.

## Impact

- Governor, job-worker ticker, runnable-job store lookup and scan job construction.
- Console presentation only: existing timestamps and attempts, no wire or GraphQL changes.
- Deltas extend background-job-playback-yield, console-job-queue and web-playback-start
  (which already covers trickplay manifests). No migrations or trickplay layout changes.
- Local checks follow the bounded task brief; commits, full gates and publication are excluded.
