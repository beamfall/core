# Mark watched reaches a container's nested parts

Ticket: CORE-3056 (`queue:core:main`).

## Why

On the web viewer, Mark watched on a TV show card (badge "8 unplayed") does nothing: the badge
stays and the episodes stay unplayed. `setWatched` on an Entity walks only same-Kind children under
an `EntityMap.ParentRelation`. A show's seasons are another container Kind that nest in it by a
declared container relation (`season-of`), so the walk stops at the show, which holds no Media of
its own: nothing is marked and `itemState` reads unwatched. The card's remaining count already
follows declared container nesting, so the two disagree.

## What Changes

- An Entity target of `setWatched` and `itemState` also reaches child Entities of another Kind that
  nest in it by a relation the Library's plugin declares between two container Kinds, within the
  same Library. Every declared parent Kind of a relation counts; a same-Kind relation stays a
  parent link only under an `EntityMap.ParentRelation`.
  Marking a show watched marks every visible non-Extra Media of its seasons; marking it unwatched
  clears them; the show reads watched once all of them are.
- Ordinary links and undeclared different-Kind relations still do not widen the mark.

## Non-goals

- Changing the card's remaining count or the web viewer; both already follow container nesting.
- Changing the watchlist or rating, which store the target itself.
