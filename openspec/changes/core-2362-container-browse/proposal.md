# Browse a Library by its container Entities

Ticket: CORE-2362 (`queue:core:main`). Folds in the parts of CORE-2327 and CORE-2334 that fall
inside this work.

## Why

The web Library page lists only Media. A TV Library opens on a wall of episodes, a music Library on
a wall of tracks. Viewers expect to start from the shows of a TV Library and from the artists or
albums of a music Library, the way the plugin's own home sections already group them. Square album
art is cropped into a 2:3 poster frame everywhere a track or album card appears, Home rows and
search included, and a track card does not say who performs it.

## What changes

- A new Graph read, `libraryEntities(libraryID, kind, limit, offset)`, lists one page of a
  Library's Entities of one kind for the calling session. It reports the kinds the viewer can
  browse that Library by (`kinds`, each with its plugin-declared label and its total), the kind it
  listed, that kind's `total`, and per item the card fields plus `childCount`, `mediaCount` and up
  to three `parents`.
- The browse kinds come from the Library plugin's declarations only: the Entity kinds its browse
  Layouts list through an `entity-items` Section, in parent-first order taken from the kinds'
  declared relations, and only kinds with at least one Entity in the Library. A kind's children
  are the kinds that declare a relation to it; its parents are the browse kinds it declares a
  relation to. Core names no kind.
- `allLabel` carries the title of the plugin's unfiltered all-items browse Section, so the page can
  name the Media view in the plugin's words.
- Entity cards carry `mediaClass` when every Media linked to the Entity shares one class, so a
  client can treat an album of audio like its tracks.
- An audio Media card carries `byline`: the titles of the person-archetype Entities linked to it.
- Related Entity groups on an Entity page order Entities without a relation sort order by title in
  natural order, so "2" sorts before "10".
- The web Library page gains a view switcher: one view per browse kind, then the Media view. It
  opens on the first browse kind, keeps the view in the URL, and leaves room for further views.
  A kind view is a paged grid of Entity cards whose line names the parents or the Media count.
- A card for audio Media, or for an Entity whose Media are audio, frames its art square (1:1)
  wherever it renders: Library pages, Home rows, Entity pages and search.
- An audio Media card's line names its byline.
- An audio container's page offers Shuffle beside Play, and names its people under the title.
- Wire contract: the new field and the added `SourceItem` keys are regenerated into the goldens.

## Non-goals

- No plugin-declared sorts or filters (CORE-2334's SDK part). Audio Libraries already drop the
  Watched, Resolution and Last watched controls.
- No new artwork on the Entity page header.
- No change to which Media or Entities a session can see.

## Failure modes

- A plugin-less Library, a Library the session cannot see, or a sealed Encrypted Library answers
  no kinds and no items; the page then shows only the Media view.
- An unknown `kind` lists nothing and still reports the kinds.
- Media hidden by the certification ceiling are not counted in `mediaCount`.
