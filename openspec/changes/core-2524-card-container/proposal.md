# Name a card's container, and find and group containers in search (CORE-2524, CORE-2525)

## Why

On a Library home a Media item held by a nested container reads only as its own code and title
("S03E01 · From Hell, S3 · E1 · From Hell"): nothing says which container it belongs to, the
Recently Added shelf repeats one container's new items card after card, and an item with a zero or
vote-less audience score shows "★ 0.0" (CORE-2524). Searching a container's title does not return
the container, and items found under it neither name it nor sort within it (CORE-2525).

## What changes

- `SourceItem` (and so search cards) gains optional `container { id title artwork }`: the outermost
  container-archetype Entity holding a Media item, reached from the container whose kind declares a
  single-holder relation to the Media and walking out through relations to other container kinds.
  Absent when no such container holds the item. Additive client-core-wire minor.
- An outermost container kind (a container-archetype kind that declares no relation to another
  container kind) is title-searchable by default, so keyword search returns it.
- Keyword search ranks a Media item by the better of its own title and its container's title, and
  breaks a rank tie by container title, the container before its items, then the item's sequence
  and index.
- A zero audience score, or one whose stored vote count is zero, is no score.
- Web: a card held by a container is headed by the container's title with the item's subtitle (or
  title) as its one second line; the Recently Added shelf collapses items sharing a container into
  one container card with a count; search lists a container's items under the container's row,
  collapsed behind a count.

## Out of scope

The film-tv plugin's shared home Layout declares "All Titles" as every Media item, a class-split
"Episodes" grid and a class-split "Movies" grid for every Library it serves. Listing containers in
"All Titles", dropping the Episodes grid and routing unmatched files out of "Movies" in a TV-scoped
Library are Layout declarations owned by that plugin and are filed as plugin follow-ups. Season and
show pages (CORE-2510) and related rails are separate changes.

## Impact

`internal/store` (container link reads, score rule), `internal/graph` (card container annotation,
keyword search order), `internal/identify` (shared container relation index), `internal/app`
(container kinds' title search policy), the wire contract artifacts, and the web viewer cards,
Recently Added shelf and search page.
