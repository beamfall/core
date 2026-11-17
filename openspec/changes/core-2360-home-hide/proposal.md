# Hide a Library from Home

Ticket: CORE-2360 (`queue:core:main`).

## Why

The owner asked: "I want to be able to hide certain libraries from the home page." Home mixes every
visible Library into its shelves (continue watching, watchlist, recently added, most played and
each Library's smart-collection rows). A Library the household keeps for occasional use, or one
whose items crowd the shelves, cannot be kept off Home without restricting it, and restricting it
also hides it from navigation and search.

## What Changes

- A catalog migration adds `library.show_on_home INTEGER NOT NULL DEFAULT 1`. Existing and new
  Libraries are shown on Home.
- The store gains `Library.ShowOnHome` and `SetLibraryShowOnHome`, and `SourceQuery.Home`. When an
  aggregate source query (no `libraryID`) is marked as a Home query, the store narrows it to
  Libraries that are shown on Home. Every other aggregate query (history, profile feeds, see-all,
  ambient) and every Library-scoped query is unchanged.
- The admin GraphQL mutation `setLibraryShowOnHome(libraryID: String!, enabled: Boolean!)` returns
  the `LibraryInfo`. It needs an Admin Session and writes a `library.show_on_home` activity record.
  `LibraryInfo` gains `showOnHome`.
- `sourceItems` gains an optional `home: Boolean` argument. The web client passes `home: true` for
  the sections it renders on Home and for "See all" opened from a Home shelf.
- Console > Libraries gets a "Show on Home" switch per Library, beside "Preview clips". Strings ship
  in every shipped locale.

The Navigation Manifest is unchanged. A hidden Library's smart-collection sections stay in the Home
layout and come back empty, and clients already hide empty sections (ADR-0014). The Library keeps its
navigation entry, its own page and its search results.

## Wire compatibility

All additions are additive and optional: a new admin operation, a new optional field on
`LibraryInfo` and a new optional argument on `sourceItems`. The wire contract is republished as a
minor version so the management manifest lists the new admin operation. A client that does not send
`home` keeps the previous unfiltered aggregate rows; native clients honour the setting once they
send `home: true` for their Home shelves (consumer follow-up).

## Non-goals

- Per-profile Home preferences. The setting is a server-wide Library policy set by a manager.
- Reordering Home or hiding individual shelves.
- Changing the manifest's Home layout.

## Capabilities

### New Capabilities

- `library-home-visibility`: a manager can keep a Library off Home while it stays reachable from
  navigation and search.

### Modified Capabilities

None.
