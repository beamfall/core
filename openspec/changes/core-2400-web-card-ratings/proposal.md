# Content rating and audience score on browse cards

Ticket: CORE-2400 (`queue:core:main`), following CORE-2398 (wire) and CORE-2399 (detail page).

## Why

Client wire 2.24.0 carries `certification` and `audienceScore` on every `SourceItem`, and the
detail page already shows them. The web browse cards, which share one component across the Library
grid, Home shelves, search and entity rows, still select neither, so a rating is only visible after
opening a title.

## What changes

- Web only; no wire or schema change. The card queries (`sourceItems`, `mediaForEntity`, `search`,
  `searchResult`) also select `certification` and `audienceScore`, and `SourceItem` types them.
- `MediaItemCard` shows a compact rating line under the title block: the rating text and, when
  present, a star and the audience score with one decimal. Nothing renders when both are absent.
- The card's accessible name gains the rating, for example "Rated PG-13, audience score 7.8".
- Score formatting is shared with the detail page; the new strings exist in every shipped locale.

## Impact

Affected spec: new capability `web-card-ratings`. No server, plugin or contract change.
