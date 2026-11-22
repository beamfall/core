# Media detail page: change the poster from the poster

Tickets: CORE-2933 (`queue:core:main`).

## Why

The owner wants to "hover over the poster, click a button and have that open a modal where I can
pick a new one". Today an admin has to leave the page for the Manage artwork screen to change a
poster.

## What changes

- A "Change poster" button sits over the detail page's poster for viewers who may edit metadata
  (a manager device on an adult Profile). It shows on hover and on focus, is always shown on
  touch screens (`hover: none`), and is reachable by Tab.
- The button opens a dialog listing the item's poster candidates from the existing
  `artworkCandidates(mediaID, "poster")` query. Choosing one pins it with the existing
  `pinArtworkCandidate` mutation, closes the dialog and reloads the detail, so the new poster
  shows without a page reload.
- No new artwork path, query or mutation. Wide stills (ordered items) get no button, since the
  frame does not show the poster slot.

## Impact

- Web viewer only: `MediaDetailPage`, a new `PosterPickerDialog`, `media-detail.css`, new strings.
