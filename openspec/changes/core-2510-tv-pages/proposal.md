# Container pages, card links and readable entity addresses in the web viewer

Tickets: CORE-2510, CORE-2511, CORE-2512, CORE-2522 (`queue:core:main`), web only.

## Why

A middle container page (a season of a show, for example) heads itself with its own title only and
repeats its parent as a one-card rail. Its Media list plays on click, repeats each title in the
plugin subtitle, and the On Deck line names the next child twice. Cards are buttons, so a viewer
cannot open one in a new tab. Entity pages live at `/entity/<id>`, which says nothing. A top-level
container page ends after its people, with no similar containers, and a browse Layout's shelves of
containers read in plain title order.

## What Changes

- A container whose own playable Media sit under an ancestor container (CORE-2510):
  - names that ancestor above its title, linked to it, and drops the ancestor's one-card rail;
  - lists its video Media as rows instead of a detail Layout that only lists them. Each row shows
    the still, the ordinal the siblings do not share plus the runtime, a two-line summary and the
    title once. The row opens the item's detail page; a separate button plays it with the
    container's queue.
- A middle container lists its ancestor's containers of its own kind as a row of links under its
  title, its own marked current (CORE-2522), so a viewer switches between siblings without going
  back to the ancestor.
- An Entity with no builtin summary reads its first `text` field as its overview, clamped to three
  lines with More/Less, instead of printing it in full among its fields (CORE-2522).
- The On Deck line names the next child once: its plugin subtitle, with the title appended only
  when the subtitle does not already contain it.
- Every navigating card is an anchor with an href (CORE-2511): the shared `MediaItemCard` renders a
  router link instead of a button, so modifier and middle clicks open a new tab. Its More menu
  stays a sibling control. Container child rows were links already.
- Entity pages get readable addresses (CORE-2511): `/entity/<words>-<token>`, with the ancestor's
  title leading a middle container's words (`/entity/3-body-problem-season-1-<token>`). One path
  segment keeps the existing `/entity/:entityId` route and its UI-state inventory.
  The token is the 32-digit hex of the content-derived Entity ID, so the client resolves it with no
  server lookup. A bare `/entity/<id>` keeps working and is rewritten in place; a Revealed session
  keeps the bare form.
- A top-level container page gains a similar-containers rail after its people (CORE-2512): other
  Entities of the same kind that share its category and person Entities, ranked by how many they
  share, hidden when there are none.
- Catalog shelves (`entity-items` and `all-items` shelf Sections) of a browse Layout are ranked on
  the client (CORE-2512): items in progress first, then unstarted, then finished, newest year first
  within each tier, server order last. Grids and See All keep the server's order.

## Non-goals

- Server-side slug resolution without a token, and a Library segment in entity addresses: the
  `entity` wire carries no Library, and a plain slug cannot be turned back into an Entity ID.
- An Extras rail on a container page: Extras are Media-to-Media relations, and no Entity-level
  Extras read exists on the wire.
- An air date on rows: Source items carry no generic date field.
- Any Go, schema, contract or plugin change.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `web-container-entity`: ancestor heading, sibling switcher, clamped text overview, Media rows,
  On Deck wording, similar containers and ranked shelves.
- `web-media-addresses`: card links and readable entity addresses.
