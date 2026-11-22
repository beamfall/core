# Entity pages list containers once, load fast and drop per-item actions

Ticket: CORE-3058 (`queue:core:main`)

## Why

The web page for a person or category Entity (a cast member, a genre, a studio) is slow and
lists every Media item a container holds next to the container itself: a genre tagged on 2,600
items shows each one instead of the few containers they belong to. On a fixture of 40
containers × 5 inner containers × 12 Media plus 100 standalone Media, the Entity read took about
106 ms and `mediaForEntity` about 55 ms with a 470 KB response. The Entity read spent most of
its time giving each related card a media class by re-reading every linked Entity's Media once
per card, so a person credited on thousands of items paid that for every container card.

The page also offers Mark watched, Add to watchlist and a star rating for a person or a
category. Those act on a thing a viewer watches through, not on a person or a genre.

## What changes

- `mediaForEntity` gains an optional `rollUp` argument. With `rollUp: true`, every non-audio
  Media item a declared container holds is replaced by one Entity card for its outermost
  container, listed once where its first item stood. Media no container holds stay listed.
  Media the context Entity reaches through its own container chain stay listed, so a container's
  page keeps its children. Audio Media stay listed: a person's audio page lists tracks
  (CORE-2362). The roll-up follows the plugin's declared container relations (CORE-2524); core
  names no kind.
- A rolled-up container card carries the earliest year of the Media it stands for and the usual
  card annotations, including its child watch counts.
- An Entity card with no Media of its own still takes the class its linked Entities' Media share,
  but each linked Entity's class span is now read once per request instead of once per card.
- The card container walk reads only the relations the items' Libraries hold Media through, so a
  heavily credited Media costs one holder row per container rather than one per credit.
- The web Entity page asks for the rolled-up list and shows a list with container cards as a
  poster grid, each card opening its container's page. Its unwatched count adds the remaining counts
  of rolled-up container cards. Play and On Deck keep working: the next unfinished standalone
  Media item, otherwise the next-up child of the first rolled-up container with children left.
- The web Entity page shows Mark watched, the watchlist toggle and the rating only on
  container-archetype Entities. Person and category pages keep Hide, which already mutes the
  Entity and everything under it (profile-hide).
- Wire contract: the added argument is regenerated into the goldens.

On the same fixture the Entity read now takes about 22 ms, and the rolled-up `mediaForEntity`
about 54 ms with an 18 KB response (about 140 cards instead of about 2,500).

## Non-goals

- No paging of `mediaForEntity`; the rolled-up list is already small.
- No change to the `entity-media` Source, the station seed or any other `MediaForEntity` caller.
- No change to which Media or Entities a session can see.

## Failure modes

- A Library whose plugin declares no containers rolls nothing up; the list is unchanged.
- A container hidden from the session never appears: the containers walked share the visible
  Media's Library, as the card container line already relies on.
