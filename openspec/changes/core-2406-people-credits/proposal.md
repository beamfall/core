# Cast rows with headshots and a person page with a photo and filmography

Ticket: CORE-2406 (`queue:core:main`).

## Why

The owner asked for people and credits to match Jellyfin and Plex. Person Entities already carry
a biography, birth and death dates, a birthplace and a headshot that the plugin fetches from its
provider and core caches as the Entity's poster. But `media(id).credits` drops the relation
attributes and the artwork, the web Media detail page shows no cast row for video, and
`entity(id)` exposes no artwork, so the person page shows no photo. The person's Media come back
in the server's order rather than as a filmography sorted by year.

## What changes

- `media(id).credits` items carry `attributes`, the relation's render-only attributes (names the
  plugin declares, such as a character or a role), and `artworkSlots`, the credited Entity's
  artwork. Both are omitted when empty.
- Credits for one relation come in relation sort order (the provider's billing order) and then by
  title. Before this change they came in title order.
- `entity(id)` carries `artworkSlots` for the Entity itself.
- The web Media detail page shows a cast row for person-archetype credits. Each card shows the
  headshot (or an initial when there is none), the name and a secondary line taken from the
  attributes, and links to the person page. Credits that carry a `department` attribute are grouped
  by it. Otherwise the row is labelled with the kind's display name.
- The web person page shows the headshot beside the name, and lists the person's Media from the
  viewer's Libraries newest year first.
- Wire contract: additive. The goldens are regenerated. The next minor version (2.22.0) is
  published by the lander.

## Non-goals

- Populating the character, role, billing or department attributes. The SDK `EntityMap` has no
  attribute mapping, so plugins cannot fill them yet. That needs an SDK change, a core scrape and
  identify change, and a plugin change (follow-up).
- Crew credits and a Directors/Writers line (they depend on the attribute mapping).
- Hydrating more than the plugin's lookup cap of people per item.
- Collapsing a filmography's items into their containers, and browsing by person.
- Native clients.

## Failure modes

- A credited Entity with no stored artwork has no `artworkSlots`. The web card falls back to the
  name's initial.
- Media or Entities outside the session's Libraries are not credited or listed, as before.
