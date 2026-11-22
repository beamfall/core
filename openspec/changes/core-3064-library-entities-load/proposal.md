# The Library tab's Entity-kind view loads in a fraction of a second

Ticket: CORE-3064 (`queue:core:main`)

## Why

The TV Library tab (the Entity-kind view of `libraryEntities`) took 3.5 to 4 seconds per 100-card
page on the owner's server, on every visit. The page read itself costs under 110 ms. The rest is
card annotation: the class and the watched counts of each container card join through its ordered
outgoing links to shared Entities (categories, people, studios) and read every Media of those
Entities, about 294,000 rows a page. The watched counts then discard those rows for a declared
container, which counts only its parts. The class read repeats them for every card that links to
the same shared Entity. The web app keeps no read, so going back to the Library tab waits again.

## What changes

- The watched counts classify each card first. A declared container reads only the parts linked to
  it, and any other kind reads only its ordered outgoing links. The counts are unchanged.
- The class read folds each linked Entity's class span once per batch instead of once per card
  that links to it. The class is unchanged.
- The web app caches recent `libraryEntities` reads. A Library opened again paints its kinds and
  pages from the cache at once and reads them afresh behind it (stale-while-revalidate). Callers
  asking for the same page at once share one request. A read made before an engagement change
  (watched state, ratings, hides, Hide watched) is never served from the cache.

Measured against a copy of the owner's catalog (456 shows): annotating a 100-card page went from
about 2.0 to 2.4 s to about 60 ms, and the request from about 2.1 to 2.5 s to about 130 to 150 ms.
The results are identical card for card.

## Non-goals

- No server-side memoization: with the annotation fixed, a page costs well under 200 ms, so a
  cache invalidated on every Library change is not worth its staleness risk.
- No change to the wire, to what a card reports, or to which cards a session can see.

## Failure modes

- A failed fresh read after a cached paint shows the grid's existing retry state; a failed
  Library read keeps the cached kinds.
- An engagement change between a read's start and its answer leaves that answer uncached.
