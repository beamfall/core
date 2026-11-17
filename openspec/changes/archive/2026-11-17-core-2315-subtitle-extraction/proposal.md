# Per-file muxed subtitle extraction with a bounded wait

Ticket: CORE-2315 (`queue:core:main`).

## Why

The muxed-subtitle byte route held one process-wide lock for the whole ffmpeg extraction. One slow
extraction (a full read of a large file) queued every other subtitle request on the server behind
it, and each track of a file paid its own full read. A player waiting on the request had no way to
tell "still working" from "broken" and hung.

## What Changes

- Extraction is single-flight per file: requests for the same file join one extraction, requests
  for other files never wait on it. At most two extractions run at once.
- The first request for any text track of a file extracts every text track of that file that is
  not cached yet in one ffmpeg pass (`Runner.ExtractSubtitlesToWebVTT`), falling back to per-track
  extraction when the pass fails so one bad track does not block the rest.
- A request waits at most 20 s. After that it gets `503` with `Retry-After: 5` and the body
  `subtitle extraction in progress; retry`; the extraction keeps running and a retry is served from
  the cache.
- The Encrypted Library subtitle path locks per (file, stream) with the same bounded wait.
- Track metadata already carries `forced` and `sdh` on every `captionTracks` entry. A per-track
  title needs a stored stream title (store column plus probe tag), which this change does not add.
