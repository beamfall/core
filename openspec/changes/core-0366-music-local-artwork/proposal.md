# Local and embedded artwork for music tracks and albums

Ticket: CORE-0366 (`queue:core:main`), core part.

## Why

Music albums currently stay without artwork unless MusicBrainz matches and Cover Art Archive
serves remote images:
1. Container sidecar discovery resolved unordered containers (`Album`) through `topLevelFolder`,
   missing `cover.jpg`, `folder.jpg` and `poster.jpg` in `Artist/Album/` and multi-disc
   subfolders (`Artist/Album/CD 1/`), and never applied album folder covers to member tracks.
2. Media-level local artwork ran after `autoIdentify`, allowing provider identification to fetch and
   store Cover Art Archive `thumb` (`front-500`) alongside or over local/embedded audio covers.
3. Poster generation sweeps attempted a video frame grab when an audio file lacked embedded artwork.

## What Changes

- Direct container sidecar resolution (`internal/scan`): when a Media's sole container Entity holds
  it directly without an ordered parent container (such as a Music `Album`), container artwork
  discovery reads `poster`, `folder` and `cover` from the Media's directory (and its parent inside
  the Library root for disc subfolders such as `CD 1` / `Disc 1`), instead of `topLevelFolder`.
- Track-level album cover precedence (`internal/scan`): for Media directly inside such a sole
  unordered container, folder `poster`, `folder` or `cover` fills `poster` when no file-level
  poster sidecar exists, and Media-level local artwork runs before auto-identification so provider
  artwork never supersedes it.
- Provider `thumb` guard and cleanup (`internal/identify`, `internal/artwork`): when an audio
  Media's `poster` holds locally locked or embedded (`APIC`/`covr`) artwork, provider `thumb`
  artwork is skipped instead of overwriting the fallback `thumb`; importing a local `poster` on an
  audio Media removes any previously provider-imported, unlocked `thumb`.
- Audio poster generation (`internal/artwork`): `GeneratePoster` extracts embedded `APIC`/`covr`
  artwork for audio Media (and records dominant color), and returns cleanly without a video frame
  grab when an audio file has no embedded cover.

## Non-goals

- Changing `beamfall-plugin-music` MusicBrainz/Cover Art Archive scraper specs or adding a new
  artwork slot.
- Applying folder posters to unrelated files in shared folders with no sole direct container.

## Acceptance evidence

- MP3 with `APIC` (and M4A with `covr`) and no MBID receives an embedded track poster and inherits
  it onto the album Entity during scan + artwork sweep.
- Album folder with `cover.jpg` (or `folder.jpg`) applies it to both the album Entity and its
  tracks over embedded `APIC`/`covr` and over Cover Art Archive `poster` and `thumb`.
- Regression tests for `TestLocalArtworkFolderSidecarSkipsSharedFolders` and
  `TestScanImportsContainerArtworkThroughIdentification` stay green.

## Rollback

Revert the commits on `wave/core-0366`. No schema, migration or wire contract changes are included.
