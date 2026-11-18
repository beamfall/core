# Cast characters, key crew and a Cast & Crew view

Tickets: CORE-2503 and CORE-2432 (`queue:core:main`).

## Why

Media detail shows cast headshots but no character under them, no director or writers, and no
way to see everyone credited. The credited-in relation already declares `character`, `billing`
and `department` attributes, but the host never wrote relation attributes from a scraper, so the
attributes were always empty. Credits past a scraper's person-lookup cap were dropped, and the
billing order came from document position only.

## What Changes

- The scrape engine reads plugin-sdk's new `EntityMap` fields (plugin-sdk change
  `entity-map-relation-attributes`): `RelationAttributeMap` values resolve against the list element
  (never the per-person lookup document), `ElementFilter` skips elements whose values are not
  listed, `OrdinalField` may name a relation attribute, and `OrdinalOffset` shifts the map's sort
  positions so a crew map can follow the cast. Elements past `MaxLookups` hydrate from the
  element itself instead of being dropped.
- Identification writes the resolved attributes on the relation row. Links of one Entity to the
  Media under one relation (a person credited twice, or in cast and crew) merge into one row:
  distinct attribute values join with ", ", the smallest sort order wins. An Entity hydrated
  without fields is created if absent and otherwise left alone (`store.EnsureEntity`), so a
  name-only credit never erases a headshot lookup's fields.
- `media(id).credits` keeps its existing order (relation sort order, then title), which is now the
  provider's billing order.
- Web: the Media detail page names its directors and writers, linked to their person pages; the
  People row shows the leading group with each person's character or job and a "See all" link to
  a Cast & Crew page that lists every credit grouped by department in billing order.

## Impact

- Code: `internal/scrape`, `internal/identify`, `internal/store/entity.go`, web viewer
  (`people.ts`, `CastRow.tsx`, `MediaDetailPage.tsx`, new `CastCrewPage.tsx`, strings).
- Needs plugin-sdk with the new `EntityMap` fields; core's go.mod repins after that SDK release.
- Existing items show characters and crew after they are identified again.
