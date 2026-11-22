# Per-Profile Home layout

Ticket: CORE-1972 (Plex parity: reorder, hide and pin Home hubs).

## Why

Home's sections come from the core Home layout (Continue Watching, Next Up,
Watchlist, Recently Added, Most-Played, Smart Collection shelves) plus the
owner's server-level layout override. A Profile cannot reorder or hide them,
which Plex and Jellyfin both allow.

## What changes

- Each Profile can save a Home layout on the server: an order of section ids
  and a set of hidden section ids, keyed by the generic manifest section id.
- The Navigation Manifest's Home layout follows that preference for the
  Profile, so every client that renders server order honours it. Sections not
  named in the saved order (a Library or Smart Collection added later) appear
  at the end; saved ids that no longer exist are ignored.
- With no saved order, the server puts Continue Watching first (the CORE-2825
  ordering moves from the web Viewer to the manifest, so all clients get it).
- GraphQL: `homeLayout` read, `setHomeLayout(sectionOrder, hiddenSectionIDs)`
  and `resetHomeLayout` mutations, all scoped to the session's own Profile.
- The preference only orders and hides sections the Profile already receives:
  a managed or child Profile, or a section the owner hid server-wide, gains
  nothing by customising.
- The web Viewer adds an "Edit home" mode with move up/down, drag reorder,
  visibility toggles and "Reset to default", all keyboard operable.

## Out of scope

- Pinning per-Library Recently Added shelves: the web splits the single
  `recent` section into per-Library shelves client-side, so those shelves have
  no server section id yet (follow-up ticket).

## Impact

- Storage: the existing `nav_override` table's `profile` scope (migration
  0077), no new migration. A Profile's row is removed with its footprint.
- Wire: three additive GraphQL fields (wire contract regenerated).
- Clients: Apple and Android already render manifest Home sections in order.
