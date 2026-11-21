# Edit details sheet, server-side artwork locks, provider poster over embedded video covers

Tickets: CORE-2905 (web sheet), CORE-2904 (pin and upload lock the slot), CORE-2901 (provider
poster outranks an embedded video cover) — all `queue:core:main`. Client follow-ups: CORE-2902
(Apple), CORE-2903 (Android).

## Why

An admin saw "A Quiet Place: Day One" on Continue Watching with a foreign release poster. The
TMDB poster candidate was recorded but never applied: identification imports the cover a release
group embedded in the video file whenever the poster is missing or generated, and then skips the
provider poster because a non-generated poster exists. Fixing it by hand needs
`/manage/items/:mediaID`, reached only by typing a Media ID, with no upload. A pick there is
locked only because the page sends a second `setMediaFieldLock` call, and a pick on a slot that
holds a local sidecar is refused, contrary to ADR-0072 (a manual pick outranks every lock).

## What Changes

- For video Media an embedded cover is only the fallback before a frame grab: identification and
  refresh apply the provider poster over it unless the slot is locked. Audio and image Media keep
  embedded covers as curated artwork. Affected items heal on Refresh metadata.
- `pinArtworkCandidate` and `POST /api/media/artwork/upload` import as a manual pick (outranking a
  local lock) and set the slot's manual lock in the server, for every client.
- The web detail page's More menu offers **Edit details** to a manager device on an adult
  profile. The sheet edits title, sort title, year, summary and the plugin-declared fields, shows
  each artwork slot's current image with provider candidates and an upload control, and offers
  **Revert to provider** on locked fields (unlock, then refresh from the last identification).
  `/manage/items/:mediaID` uses the same editor.

## Non-goals

- More than one provider candidate per slot (the film/TV plugin requests a single poster).
- A Library setting to prefer embedded artwork on video.
- Apple and Android sheets (CORE-2902, CORE-2903).

## Capabilities

### New Capabilities

- `media-metadata-editing`: admin editing of one Media's details and artwork, and the artwork
  source precedence it depends on.

## Impact

`internal/identify`, `internal/artwork`, `internal/graph`, `internal/server`, web viewer and manage
areas. No wire shape changes, no migration.
