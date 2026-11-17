# Tower QA: web viewer addresses, page titles and track-change motion

Tickets: CORE-2322, CORE-2329, CORE-2331, CORE-2332 (`queue:core:main`).

## Why

A QA pass on a real server found that viewer pages all carry the same tab title, watch and detail
addresses are opaque digests (`/web/watch/media-sha256-…`), and the audio card changes track with
one fixed motion that users cannot choose or vary.

## What Changes

- Viewer pages set the document title from the page heading; a Revealed session shows only the
  product name, so restricted titles stay out of tab titles and browser history (CORE-2322).
- Watch and detail addresses read `/web/<route>/<library>/<words>-<token>` (CORE-2329). The token is
  the digest of a content-addressed Media ID, so the client resolves it without a lookup; the words
  come from generic display fields (the Library name, the item title and its person or container
  credit). Bare-ID links and stale words resolve through the token and are rewritten in place to
  the canonical address. Non-content IDs (an encrypted Library's) and Revealed sessions keep the
  bare-ID address. Until the graph `MediaInfo` exposes `libraryID`, addresses have no Library
  segment.
- The audio card animates its words on a track change in one of three styles (letter rise with
  blur, split-flap roll, masked wipe) and its artwork in one of four (crossfade with scale, flip,
  slide over, blur dissolve), chosen per change or fixed by a per-browser setting in the player
  settings sheet (CORE-2331, CORE-2332). Reduced motion crossfades only.

## Non-goals

- No server lookup by slug and no new graph field in this change; `MediaInfo.libraryID` is an
  additive field for the graph owner (B1) to add.
- No Oscillux effect: the motion is host UI chrome, not a visual effect.
- No change to the video player surface.

## Failure modes

- A token that names no item shows the existing not-found state.
- A blocked `localStorage` keeps the motion setting at "Mix" for the page.
- An artwork that fails to decode falls back to the placeholder through the same transition.

## Acceptance evidence

Vitest: `mediaAddress.test.ts`, `useCanonicalMediaAddress.test.tsx`, `documentTitle.test.tsx`,
`AudioStage.test.tsx`, `trackChangeMotion.test.ts`, the player settings test; `tsc`; Biome.

## Rollback

Revert the web commits; routes still accept bare-ID addresses, so links issued meanwhile keep
working (the token is the ID's digest).
