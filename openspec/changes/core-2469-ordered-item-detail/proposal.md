# Ordered items on the web Media detail page, in search and on cards

Tickets: CORE-2469, CORE-2471 and CORE-2472 (`queue:core:main`).

## Why

A Media item that sits in an ordered container (a TV episode in a season, for film and TV) opens a
detail page that reads like a film's: the H1 is the stored title or code, the hero shows the
container's poster, no position, air date or stream badges appear, and the closing rows list the
same container's items under a person's name and under Related. Search lists such items in rank
order, so one container's items scatter through the results. A card whose plugin stores a score of
0 with no votes shows "★ 0.0".

## What changes

- `media(id)` gains the item's `sequence` and `index` ordinals, a `headline` rendered from the
  plugin's MediaSubtitle segments that name no ordinal (the item's own title, when stored), the
  `classLabel` its plugin declares for the item's class, and the stored `videoWidth` and
  `videoHeight`. The client-core wire contract 2.26.0 is re-published additively.
- The web Media detail page, for an item with a container credit and ordinals: the grouping above
  the container links first, then "<container kind> N · <class label> M" with the container part
  linked, then the headline as the H1 (falling back to the title). The hero uses the item's `thumb`
  art in a wide frame, with the poster as a fallback. The facts row adds the air date, a resolution
  badge, an HDR badge and the default audio track's codec and layout when known. A "More in
  <container>" rail lists the nearest container's other items in order, and the More with and
  Related rows leave out that container's items.
- The grouping above the container is found also when the grouping's own item count is not
  reported: the one container-archetype Entity of another kind related to the nearest container.
- Search keeps the Media rows that share an ordered container root (the container, or the
  container-archetype Entity it links to) together at the first one's position, ordered by
  `sequence` then `index`.
- A web card shows no score for an `audienceScore` of 0 unless it carries votes.

## Non-goals

- Native clients (follow-up per client).
- A plural container heading and a class-labelled count on the grouping's Entity page: plugins
  declare singular display names only (plugin-sdk v0.4.3), so core cannot pluralise them without
  hardcoding domain words. Needs a plugin-sdk plural display name first.
- Reordering across search pages: grouping applies within the page the server returns.

## Failure modes

- A failed or empty field read leaves the new `media(id)` fields absent; the page renders as before.
- A failed container read hides the "More in" rail and leaves the More with and Related rows whole.
- Search rows whose container read fails keep rank order.
