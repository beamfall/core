# Per-Profile pin, hide and reorder of sidebar libraries

Ticket: CORE-2435 (`queue:core:main`), PLEXPAR-10.

## Why

The Viewer sidebar lists libraries in the order the server's navigation gives, and the only way
to hide or reorder one is the server-wide navigation override, which a Manager sets for every
Profile. Each Profile should choose which libraries it keeps pinned in its own sidebar and in
what order, as other media servers allow, without touching what other Profiles see.

## What Changes

- The web sidebar gains an edit mode on its Libraries heading: each library can be pinned or
  unpinned and moved up or down by button, keyboard or drag. Done saves the choice.
- The choice is stored per Profile in the existing `profile_client_prefs` seam under the key
  `web.sidebar-libraries` as JSON `{"order": [libraryID...], "hidden": [libraryID...]}`. No
  graph schema, wire contract or store change: `setClientPref` / `clientPrefs` already carry it.
- The Profile choice layers over the server-wide navigation override: the override is applied
  first by the server (it shapes the manifest navigation); the Profile choice can only reorder or
  unpin libraries the server still lists. Library IDs in the choice that the server no longer
  lists are ignored, and libraries the choice does not name keep their server position after the
  ordered ones.
- Unpinned libraries stay reachable under a "More" disclosure at the end of the list.

## Non-goals

- Aggregate entries (Home) are not reorderable or hideable per Profile.
- Native clients do not read the key yet.
