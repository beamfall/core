# Identification names the episode in its display title

Ticket: CORE-2470 (`queue:core:main`). Related: CORE-1713 (identification keeps a parser-owned
display title), CORE-2026 (existing media keeps old titles).

## Why

Since CORE-1713, `Apply` writes the plugin parser's per-file display title instead of the scraper's,
so an identified episode stays `S03E01` or `S03E02 · I Dont Want to Go to Chelsea`: the parser reads
the filename only. The same identification writes the provider's episode name
(`Smells Like Mean Spirit`, `I Don't Want to Go to Chelsea`) as a plugin Media field, but the title
never sees it (Tower QA 2026-11-18, core 3efdff24, Ted Lasso).

## What Changes

- `scan` gains the optional parser hook `IdentifiedMediaTitler`:
  `IdentifiedMediaTitle(path, probe, fields)` builds the per-file title from the identification's
  plugin field values (bare field name to text).
- `identify.Service` gains the optional `IdentifiedMediaTitleSource`. `Apply` passes it the
  lookup's plugin-declared Media field values (implied constants and `media.<name>` document values,
  the ones `Apply` writes) and writes the non-blank title it returns. A source or parser without the
  hook, a blank title or a failed read behave as before (`MediaTitle`, else the scraper's title).
- `Service.PreviewLookup` returns the looked-up candidate and the title `Apply` would write, so the
  Fix-Match preview proposes the identified episode name too.
- The composition root's parser adapter implements the hook over the Media's primary file.

## Impact

- Code: `internal/scan` (hook interface), `internal/identify` (field values, `PreviewLookup`),
  `internal/graph` (preview), `internal/app` (adapter).
- No store method, migration, GraphQL or wire-contract change.
- The Film & TV plugin implements the hook in its CORE-2470 release; core takes effect once it
  repins. Media identified earlier keeps its title until it is identified again (refresh or Fix
  match); a backfill is CORE-2026's.
