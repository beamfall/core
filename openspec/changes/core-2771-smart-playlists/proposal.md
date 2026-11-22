# Smart playlists: rule-based Smart Collections

Ticket: CORE-2771 (`queue:core:main`).

## Why

Plex lets a listener build a playlist from rules (genre, plays, last played, rating, year, added)
with match all/any and a limit, and the list keeps itself current. Beamfall already stores Smart
Collections (`smart_collection`, ADR-0011 §10) and evaluates them live through
`sourceItems(kind: "smart-collection")`, but the source vocabulary is a single field-equality
filter, `sortExpr` is stored and never applied, and the only editor is a raw JSON box on the manage
page. There is no way for a listener to build or play a rule-based list.

## What Changes

- Extend the Smart Collection `sourceExpr` vocabulary with a `rules` kind:
  `{"kind":"rules","match":"all"|"any","rules":[{"field","op","value"}],"limit":N}`.
  Fields are generic: built-in Media columns (`title`, `duration`, `year`), per-Profile engagement
  (`playCount`, `lastPlayed`, `rating`), plugin-declared Media fields (`field:<name>`) and
  plugin entity relations such as genre or credits (`entity:<kind>`). Core names no domain kind.
- Give `sortExpr` meaning for rule sources: `{"by": key, "dir": "asc"|"desc"}` over the same keys
  plus `added` (catalog insertion order) and `random` (stable per day).
- Evaluate server-side on every read, bounded (at most 20 rules, limit 1..5000), for the reading
  Profile, through the existing Library visibility, certification, extras, file-presence, trash,
  mute and hide-watched filters. Because evaluation is live, a list refreshes as soon as the
  library or the Profile's play history or ratings change.
- Validate rule sources on create and update; a malformed rule set is a validation error.
- Web: the Playlists page lists smart playlists alongside ordered ones, managers get a rule builder
  (create/edit/delete), and a smart playlist page shows the current tracks with Play all.
- No migration and no GraphQL schema change: the existing Smart Collection mutations carry the
  expressions, and `sourceItems` already takes `value`.

## Non-goals

- A Media "added at" timestamp (rules over added date): `added` exists only as a sort key here.
- A server field catalogue for the builder (plugin field labels and types).
- Evaluation inside encrypted libraries, and native client builders.
- Skip counts (CORE-2773), mood, codec and folder rules.

## Failure modes

- Unknown field, operator or a value of the wrong type: create/update returns a validation error;
  a stored rule set that no longer decodes yields an error on read, never a widened list.
- A Profile-less read (no session Profile) evaluates engagement rules as "never played, unrated".
- A Library the reader cannot see yields an empty list.

## Capabilities

### New Capabilities

- `smart-playlists`: rule-based Smart Collection sources, their evaluation and the web builder.

### Modified Capabilities

None.
