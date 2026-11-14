# Libraries carry an icon

Ticket: CORE-1200 (`queue:core:main`).

## Why

The web sidebar draws every Library as a letter tile. With five or more Libraries the rail reads
"F, T, M, P, P": the owner has to read each letter, and two Libraries can share one. The manifest
tells a client nothing about what a Library holds except its plugin id, which a generic client must
not interpret. The owner asked to be able to set icons for Libraries.

## What Changes

- Core gains one small fixed vocabulary of Library icon names. The names are shape and medium words
  (`film`, `tv`, `video`, `note`, `mic`, `broadcast`, `headphones`, `book`, `image`, `camera`,
  `people`, `star`, `heart`, `sparkles`, `layers`, `folder`). Clients compile in one symbol per
  name, as they do for the other fixed vocabularies.
- A Library stores an optional owner-chosen icon in the existing `server_config` settings table
  under `library.icon.<libraryID>` (no key means "no choice"). No catalog migration: the schema is
  frozen for 1.0, and a per-entity `server_config` key is how plugin profile links are already
  stored. The wire surface does not depend on the storage, so the choice can move to a column later.
- The Navigation Manifest's `LibrarySummary` gains an optional `icon`: the owner's choice, else a
  default. The default comes from the bound plugin's declared media classes: `film` for video,
  `note` for audio, `image` for image, `layers` for more than one class, `folder` for a Library
  with no plugin or an unknown one.
- The admin mutation `setConsoleConfig` accepts `libraryID` with `libraryIcon` to set or clear the
  choice. It needs an Admin Session, rejects a name outside the vocabulary, and writes a
  `library.icon` activity record for the Library. The `libraries` query returns `icon`,
  `iconChoice` and `defaultIcon`.
- The web sidebar draws the icon in the Library's accent tile. A name the client does not know
  falls back to the existing collision-aware monogram.
- Console > Libraries gets an icon picker per Library: a radio group with a "Default" option and
  one named option per icon. It saves on selection, updates at once and restores the previous
  selection with the server's error when the save fails.
- Strings ship in en, fr, de, es and pt.

## Wire compatibility

Both additions are additive and optional. A client that ignores `icon` keeps working; a client that
reads it against an older server sees it absent and keeps its own fallback.

The write rides the existing `setConsoleConfig` operation instead of a new `setLibraryIcon` one
because of the state of the wire contract. Every admin GraphQL operation must be listed in the
management manifest, and that manifest must equal the one inside the newest published wire contract
(1.13.2). The next publication has to be the 2.0.0 major that is already staged as an unpublished
candidate, and a major publishes only in a coordinated window with every client repin. A new admin
operation therefore cannot land before that window; a new variable on an existing operation can.
The new fields join the unpublished 2.0.0 candidate. Promoting the write to a dedicated
`setLibraryIcon` operation is a follow-up for the 2.0.0 window (only the embedded web Console calls
it, so the move costs no compatibility).

## Non-goals

- A plugin-declared icon. plugin-sdk v0.3.2 `Bundle` has `MediaClasses` but no icon, so three
  first-party audio plugins share the `note` default until an owner picks another. A
  `Bundle.Icon` field drawn from this vocabulary is the follow-up; core will not map plugin ids or
  domain words to icons.
- Custom uploaded icons or emoji.
- Icons for aggregate navigation entries (Home, Ambient): each client already draws those itself.
- Native client rendering. Apple, Android, Windows and Linux clients keep their current tiles until
  they adopt the field.

## Capabilities

### New Capabilities

- `library-icons`: every Library has an icon from a fixed vocabulary, defaulted from its media
  classes and changeable by an admin.

### Modified Capabilities

None.
