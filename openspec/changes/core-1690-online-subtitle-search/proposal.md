# Search online for subtitles and download them as sidecars

Ticket: CORE-1690 (`queue:core:main`), server and web part. Native clients follow.

## Why

A viewer whose file has no subtitle track in their language has no way to get one from inside
Beamfall. Jellyfin and Plex both let a user search OpenSubtitles from the player and add the result
as a track. Beamfall already discovers sidecar subtitle files next to a media file and serves them
as tracks; what is missing is a way to find one online and put it there.

## What Changes

- New core package `internal/subtitlesearch`: a domain-agnostic `Provider` interface (search and
  download) with an OpenSubtitles REST implementation. It never builds an HTTP client; every
  request goes through an injected egress seam. Tests run against recorded JSON fixtures and make
  no live calls.
- Matching sends the OpenSubtitles file hash (the existing OSHash) and file size, plus the Media
  title and year and, when the Library plugin stores them, the `sequence` and `index` ordinals.
  Core reads those as generic Media fields and never names a domain concept.
- Egress is opt-in. The server contacts a provider only when the operator lists it in
  `BEAMFALL_SUBTITLE_SEARCH_PROVIDERS` (or `--subtitle-search-providers`). The default is empty,
  so a stock server makes no subtitle requests. Requests travel on the hardened outbound proxy
  (HTTPS only, private addresses refused, no cross-host redirects, 512 KiB cap) under a
  core-owned policy that declares the provider's origins and approves them only when enabled.
  No install identifier or telemetry is sent; the User-Agent is `Beamfall v<version>`.
- The provider API key resolves through the existing scraper credential source: the operator's
  0600 file `<data-dir>/secrets/opensubtitles-api-key` first, then a key injected into release
  builds, as ADR-0238 §2.5 does for Podcast Index. No key is committed. A server with no key
  reports the provider as needing a key and makes no request.
- New REST routes for a Profile session (no API token or developer key), allowed for an adult
  Profile or a manager device, on a Media the session can see:
  - `GET /api/media/subtitle-search` reports whether search is enabled and which providers.
  - `POST /api/media/subtitle-search` `{mediaId, languages?}` returns results with provider,
    language, rating, download count, release name, hash match, SDH and forced flags.
  - `POST /api/media/subtitle-download` `{mediaId, provider, fileId, language, sdh, forced}`
    downloads the file, checks it is SRT text within the size cap, writes it beside the media
    file as `<stem>.<lang>[.sdh|.forced].srt` (create-exclusive, a numbered name on collision,
    never overwriting), records it as a sidecar caption row so it plays at once, and writes a
    `subtitle.download` activity row. A later rescan finds the same file on disk.
  - Encrypted and custodial Libraries, sealed Media and read-only folders are refused.
- Web: a "Search online…" entry at the end of the player's subtitle menu and in the media detail
  page's More menu opens a dialog listing results (language, rating, hash match, source). Download
  adds the track; the detail page rereads its tracks and the player reloads its track list.

## Non-goals

- A Console toggle and key entry UI (environment and secrets file only in this slice).
- Providers other than OpenSubtitles.
- Storing downloads for read-only or encrypted Libraries in the data directory.
- Native client UI.
- Automatic download on scan.

## Owner decisions

- Whether release builds ship a Beamfall OpenSubtitles API key (the ADR-0238 pattern is ready;
  this change ships none).
- Default state: this change ships search off.
- Which providers to support beyond OpenSubtitles.
