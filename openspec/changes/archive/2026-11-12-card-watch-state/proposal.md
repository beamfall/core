# Watch state and actions on media cards

Tickets: CORE-1193 and CORE-1192 (`queue:core:main`).

## Why

A media or entity card in the web app shows only artwork and a title. Its accessible name carries
no type or playback state, a partly played item looks the same as an untouched one outside Continue
Watching, and the only action is opening the detail page. The owner's comparison with other media
servers on 2026-11-11 found this the largest gap on every shelf and grid.

## What Changes

- Every list response that returns `SourceItem` carries an optional `engagement` object for the
  acting Profile: resume position and duration, a completed flag, and for an ordered container the
  count of visible ordered children and how many remain. Counts never include a child the scope
  cannot see (ADR-0004).
- Every card has an accessible name with its title, its type where the item states one, and its
  playback state.
- Cards draw a progress bar on the artwork for a partly played item, a finished mark for a completed
  one, and a remaining count on an ordered container.
- Hover or focus reveals a Play or Resume control and a More menu; both work by keyboard and touch.
- Card state refreshes after playback without a page reload.

## Non-goals

- The played-state, watchlist and rating mutations themselves (CORE-1194). The More menu calls the
  CORE-1194 played-state action; watchlist and rating stay on the item page.
- Native clients. They read the same wire field in their own tickets.

## Capabilities

### New Capabilities

- `web-card-watch-state`: the playback state a card reports, shows and acts on.

### Modified Capabilities

None.
