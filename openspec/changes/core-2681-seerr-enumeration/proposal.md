# Seerr read-only enumeration on the Jellyfin compatibility API (CORE-2681)

## Why

Seerr 3.5.2 finishes setup against Beamfall (CORE-2709), then fails library sync. It lists users,
media folders and items to work out what the server already has, and Beamfall answers every
enumeration probe with an empty list. ADR-0171 §2.3 allows these routes as read-only enumeration
over operator-allowlisted Libraries, with Reveal and certification applied by core before any
count.

Lidarr 3.x also posts `Library/Media/Updated` with paths relative to its own root folder. Those
paths never fall under a Beamfall Library root, so the update queues nothing today.

## What changes

- `Users` lists the opted-in, enabled Profiles by ID and name. `Users/{id}` describes one of them
  with an empty `GroupedFolders` configuration. `Users/{id}/Images/Primary` serves that Profile's
  stored picture, or 404.
- `Library/MediaFolders` lists the eligible Libraries the session's Profile can see as collection
  folders, with no path. The collection type comes from the Library plugin's declared interop
  scope. Encrypted Libraries are never listed.
- `Items?ParentId=<library>` pages the top-level items of one eligible Library in a stable order.
  `Items?ids=` returns the named visible items. `Items/Latest` returns the newest visible items.
  `Shows/{id}/Seasons` and `Shows/{id}/Episodes` list a container's children in order.
- Item types, container walks and collection types come from each plugin's `Interop` declaration
  (plugin-sdk v0.4.6). Core compares the declared tokens and never names a domain type. An item
  whose plugin declares no mapping is not listed.
- Provider IDs come from the scraper that identified the item and from stored external IDs. A
  scraper's ID scheme is the provider whose by-ID lookup is the scraper's own lookup.
- Leaf items carry one media source with one video stream holding only the stored width and
  height. No path, stream URL, playback route or file name is exposed.
- An ID that is unknown, not visible or outside every eligible Library gets the same 404.
- `Items` with no `ParentId` and no `ids` stays empty, so the *arr path lookups still disclose no
  server path.
- A media update whose paths are all relative queues a scan of every eligible root, under the
  shared refresh limit.

Item artwork, watch state and every streaming or playback route stay out of scope: Seerr 3.5.2
reads none of them during sync (ADR-0171 §2.1, owner rule 2026-11-19).

The core repin to plugin-sdk v0.4.6 and the matching plugin releases carries CORE-2676's plugin
side, including the Interop declarations this change reads.
