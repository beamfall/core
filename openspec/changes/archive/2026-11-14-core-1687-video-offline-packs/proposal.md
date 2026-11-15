# Video offline packs with a download quality

Ticket: CORE-1687 (`queue:core:main`). Status: proposed.

## Why

The offline pack substrate (ADR-0157, ADR-0208) builds audio-only packs. A viewer cannot download a
season or a whole series for a flight, pick a smaller download for a phone, or keep the next few
unwatched items of a series on the device. Jellyfin clients queue season and series downloads at a
chosen transcode quality; Beamfall should do the same and add an automatic "up next" pack.

## What Changes

- **Video Media in packs.** A pack build accepts audio and video Media and still refuses every other
  class. This lifts the audio-only launch scope of ADR-0157 §7 for video.
- **Download quality.** `createOfflinePack` takes an optional `quality`: `original` (the selected
  Version's bytes, unchanged), `high` (at most 1080p), `medium` (at most 720p) or `low` (at most
  480p). The non-original qualities transcode each video item server-side to H.264/AAC MP4 through
  three new audited FFmpeg presets before it is hashed and sealed. Audio items always keep their
  original bytes. The 4 GiB per-item bound and the build byte budget apply to the transcoded size.
- **Background builds.** A request that names a `quality` is built by a background job, not inline
  in the request. The mutation returns at once with the pack's pre-assigned ID and state `building`;
  the pack appears in `offlinePacks` under that ID when the build finishes. A repeat request for the
  same Profile, target and quality while a build is active returns the same ID. A request without
  `quality` keeps today's inline build.
- **Profile-aware resolvers.** A resolver may implement an optional `ResolveForProfile`, so a target
  can depend on the requesting Profile's watch state.
- **Film & TV pack kinds.** The composition root adapts the Film & TV plugin's season, series and
  next-unwatched resolvers into the pack service, as it already does for music. Core code names no
  domain kind.
- **Build byte budget.** The default cumulative budget per build rises from 4 GiB to 64 GiB so a
  series fits in one pack.

## Non-goals

- Client UI (queueing a season or series, the quality picker, auto-download of the next N items,
  the Apple Wi-Fi-only setting). Those are child tickets per client.
- Transcoding items in an encrypted Library. Such a request fails; a follow-up ticket covers it.
- Persisting build progress or failure on a pack row. A failed background build leaves no pack and
  the job's failure in the job log; a follow-up ticket adds a pending row with progress.

## Failure modes

- An unknown `quality` fails the mutation before any job is queued.
- A non-original quality with no transcoder configured, or an item in an encrypted Library, fails
  the build and writes no pack.
- A transcode that fails or exceeds the per-item bound or the budget fails the build; the pack
  directory, including temporary transcodes, is removed.
- A video item whose original bytes exceed 4 GiB fails at `original` quality; a smaller quality fits.
