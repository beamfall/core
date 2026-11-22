# Media detail page: one reading order, one meta line, grouped actions

Tickets: CORE-2930 (`queue:core:main`).

## Why

The web Media detail page reads as a pile of equal-weight parts (owner report, 2026-11-21):

- Back sits above the stage and is half hidden under the sticky top bar.
- The poster is aligned to the bottom of the lockup and floats low and left, with empty space
  above it.
- 4K and HDR10 appear twice: once in the facts row, again as the track pickers' badges.
- Date, runtime, audio, rating, studios and genres are all same-weight chips, with "+1" pills.
- Progress sits on its own row with "1% complete".
- Actions are spread over five rows: Resume and Start over; Watch together, Play on and More;
  Subtitles; Mark watched and Add to watchlist; then the stars.
- The synopsis comes last, after every control.

## What changes

- **Back.** Back moves inside the stage, at its top-left, as a small quiet control. It scrolls
  with the stage and is never under the top bar.
- **Lockup.** The poster's top aligns with the top of the copy column, and the backdrop leaves
  less empty space above the lockup.
- **Meta line.** Date, runtime, content rating and community score read as one line of text,
  separated by dots. The content rating keeps its bordered badge. Resolution, HDR and audio
  follow as small outline badges, shown once and taken from the shown Version, so a Version pick
  updates them. The track pickers no longer draw their own badges.
- **Categories.** Category credits become one line of plain text links, with each kind separated
  by a dot and no "+N" pills. Details still lists every credit.
- **Order.** Parents and position, then the title, the meta line, categories, overview, crew, and
  last the actions.
- **Actions.** Actions sit in three groups, each tighter inside than the space between them:
  - Playback: Play or Resume, Start over, Next, Watch together, Play on and More. Resume carries
    the progress as a thin bar along its bottom edge, still exposed as a progressbar with the
    percentage. Play on becomes the same quiet button as Watch together.
  - The track pickers, label beside the select.
  - The Profile's own state (watched, watchlist, stars), drawn quieter than playback.
- **Phone.** One column: a small poster beside the title, and Play or Resume full width.

## Impact

- `internal/web/app/src/areas/viewer/pages/MediaDetailPage.tsx`, `media-detail.css`
- `internal/web/app/src/areas/viewer/components/MediaTrackPickers.tsx`
- `internal/web/app/src/areas/viewer/remote/PlayOnButton.tsx`
- `internal/web/app/src/areas/viewer/lib/streamFacts.ts` (takes the shown Version)
- No wire, schema or contract change.
