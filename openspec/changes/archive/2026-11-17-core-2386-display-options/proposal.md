# Library display options and plugin-declared card subtitles

Tickets: CORE-2386 and CORE-1226 (`queue:core:main`).

## Why

A folder of numbered Media shows identical cards: on tower, a TV Library folder holding twelve
items shows twelve copies of the same poster, title and year, because each item carries its parent
title and art. Core cannot say what tells the items apart without naming the plugin's domain. The
web Library page also offers only a poster grid and a bare list, and an open folder sorts by file
name even when its items carry ordinals.

## What changes

- plugin-sdk adds `Bundle.MediaSubtitle` (CORE-1226, plugin-sdk branch
  `towerqa/media-subtitle`): ordered template segments over declared Media fields and the
  structural ordinals `sequence`, `index` and `index-end`. plugin-filmtv declares its segments on
  branch `towerqa/media-subtitle`. Core pins those branch commits until both are tagged.
- Media cards on the wire (`SourceItem`) gain `subtitle`, the plugin's template rendered for the
  item in every locale the template declares, plus the numeric `sequence` and `index` the item
  stores. All three are omitted when absent. Core renders the template without knowing its words.
- `libraryFolder` orders the Media directly in a folder by `sequence`, then `index`, with items
  carrying neither after them in the existing file-name order.
- The web Library page offers three displays for Media: a poster grid, a landscape thumbnail grid
  and a compact list (ordinal, title, subtitle, duration, progress). The choice is remembered per
  Library in browser storage, applies to the all-titles view and an open folder, and works at
  375px. A stored "grid" choice from the earlier switch reads as the poster grid.
- Card lines show the subtitle before the year.
- Wire contract: the added `SourceItem` keys are regenerated into the goldens.

## Non-goals

- No stored subtitle: the template renders over fields core already stores, so existing Libraries
  need no rescan.
- No display option for Entity kind views; they stay poster grids.
- No server-side persistence of the display choice.

## Failure modes

- A Library whose plugin declares no template, or an item missing every value, has no subtitle and
  renders as today.
- A plugin bundle that fails to load leaves its items without a subtitle; the read does not fail.
- Unavailable browser storage falls back to the poster grid each visit.

## Acceptance evidence

Store and graph Go tests for the subtitle, ordinals and folder order; regenerated wire goldens;
vitest coverage for each display and the remembered choice; `openspec validate --all --strict`.

## Rollback

Revert the core commits. Clients ignore the added optional keys; the plugin branches stay untagged
until the lander releases them.
