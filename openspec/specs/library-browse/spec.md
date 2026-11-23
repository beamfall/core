# library-browse Specification

## Purpose
Let a viewer see every item in a library in the web app, with its count, and sort, filter and play the whole filtered set, backed by a browse read that returns a total and supports those sorts and predicates.

## Requirements

### Requirement: Library browse read reports the whole filtered set

The Graph read `libraryItems` SHALL return one page of a library's Media together with `total`, the
number of items matching the request's filters across all pages. It SHALL apply the same visibility
as the all-items Source: reveal state, library allow-list, certification ceiling, trash, extras and
per-Profile Mute.

#### Scenario: Paging keeps the total

- **WHEN** a client requests `libraryItems` with `limit: 1, offset: 1` for a library of N visible
  items
- **THEN** the response holds one item, the second in the requested order, and `total` is N

#### Scenario: Library outside the session scope

- **WHEN** a session that has not revealed a restricted library requests its `libraryItems`
- **THEN** `total` is 0, `items` is empty and every facet list is empty

#### Scenario: Muted item kept by a Favorite in an Encrypted Library

- **WHEN** an open Encrypted Library holds an item the Profile has favorited and reaches through a
  Mute that includes descendants
- **THEN** the item is in the response with `muted: true`, read from that library's own relations

### Requirement: Library browse sorts server-side

`libraryItems` SHALL order the filtered set by `sort` (`title`, `year`, `added`, `duration`,
`last-watched` or `random`) in `direction` (`asc` or `desc`) before paging. Items that lack the sort
key SHALL come last in both directions. `random` SHALL return the same order for the same `seed`.

#### Scenario: Reversed direction

- **WHEN** a client requests `sort: "title"` once with `direction: "asc"` and once with `"desc"`
- **THEN** the second response lists the same set in reverse title order

#### Scenario: Unknown sort

- **WHEN** a client requests a `sort` the server does not support
- **THEN** the read answers a client error and returns no items

### Requirement: Library browse filters by watch state, grouping, year and quality

`libraryItems` SHALL keep only items matching every supplied filter: `watch` (`unwatched`,
`in-progress`, `watched`, evaluated for the calling Profile), `groupFilters` (a map of grouping kind to
value: items related to each named entity), `yearFrom`/`yearTo` (inclusive bounds) and `quality` (a
resolution tier).

#### Scenario: In-progress filter

- **WHEN** the calling Profile has a resume position on one item and requests `watch: "in-progress"`
- **THEN** the response contains that item and no item the Profile has finished or never started

#### Scenario: Grouping value moved to trash

- **WHEN** a client supplies a `groupFilters` value whose entity is in the trash
- **THEN** the response contains no items, as for a value that does not exist

#### Scenario: Unknown filter value

- **WHEN** a client supplies a `watch` or `quality` value the server does not support
- **THEN** the read answers a client error

### Requirement: Library browse reports the plugin's groupings

`facets` SHALL list the groupings, the decades and the quality tiers present in the library's
visible set before filtering. A grouping SHALL be a category-archetype entity kind declared by the
Library's plugin, reported with its kind, the plugin's display name and its values, in declaration
order. A kind with no value in reach, and a Library without a plugin, SHALL report none.

#### Scenario: Groupings come from the plugin

- **WHEN** a Library's plugin declares two category kinds and its visible items relate to entities
  of both
- **THEN** `facets.groupings` lists both kinds with the plugin's display names and their values
- **AND** a `groupFilters` entry for each kind returns only the items related to both entities

### Requirement: Web library page offers a full browse view

The web viewer SHALL offer, for each library, a browse view that shows the total item count, renders
the set in a virtualized display, and keeps the sort and every active filter in the URL. Active
filters SHALL be shown as removable controls. Every control SHALL be keyboard operable and have an
accessible name.

#### Scenario: Shared URL restores the view

- **WHEN** a user opens a browse URL carrying a sort and a filter
- **THEN** the view loads with that sort and filter applied and shown as active

#### Scenario: Removing a filter

- **WHEN** a user activates an active filter's remove control
- **THEN** the filter leaves the URL and the count and items update to the wider set

### Requirement: Browse display options

The browse display SHALL be one of a poster grid, a landscape thumbnail grid, a compact list
whose rows show the ordinal, title, subtitle, duration and progress, and the table view (see Web
Library browse offers a table view). The display choice SHALL be
remembered per library, SHALL apply to the all-titles view and an open folder, and SHALL fit a
375px viewport without horizontal scrolling.

#### Scenario: Display choice is remembered per library

- **WHEN** a user picks the list display in one library and reopens that library later
- **THEN** the list display is selected there and other libraries keep their own choice

#### Scenario: Earlier grid choice

- **WHEN** a library's remembered choice is the earlier "grid" value
- **THEN** the poster grid is selected

### Requirement: Play all and Shuffle play the current filtered set

The browse view SHALL offer Play all and Shuffle. Play all SHALL start playback at the first item of
the current filtered set in the current order and continue through the set. Shuffle SHALL play the
same set in a random order. Both SHALL leave out items a locked certification ceiling blocks for the
Profile, which stay listed on the page, and SHALL open each item under the title the listing gave.

#### Scenario: Play all with a filter active

- **WHEN** a user activates Play all while a filter is active
- **THEN** playback starts on the first filtered item and the next item played is the second
  filtered item, never an item the filter excludes

#### Scenario: Locked items in the set

- **WHEN** a user activates Play all over a set whose first item is locked for the Profile
- **THEN** playback starts on the first item the Profile may play and continues past every locked
  item to the end of the set

#### Scenario: Later items keep their titles

- **WHEN** Play all advances to the second item of the set
- **THEN** the player shows that item's title

#### Scenario: Play all over a last-watched order

- **WHEN** Play all starts over a set sorted by `last-watched` and playing moves each item in that
  order
- **THEN** playback visits every playable item once, in the order the set had when Play all started

### Requirement: Library browse reports where each title initial starts

When `libraryItems` orders by `title`, the response SHALL include `letters`, one entry per title
initial present in the filtered set, each with the `offset` of the first item carrying that initial
in the requested order and the `count` of items carrying it. The initial SHALL be the title's first character upper-cased when
it is a letter, and `#` otherwise. Entries SHALL be listed in order of increasing offset. For any
other sort, `letters` SHALL be empty.

#### Scenario: Jump offsets match the title order

- **WHEN** a client requests `libraryItems` with `sort: "title"` for a library whose visible titles
  start with "a", "B", "b" and "3"
- **THEN** `letters` is `#` at 0 with count 1, `A` at 1 with count 1 and `B` at 2 with count 2, and
  requesting `offset: 2` returns the first "B" item

#### Scenario: Filters narrow the letters

- **WHEN** a watch-state filter leaves no item starting with "B"
- **THEN** `letters` has no `B` entry and the remaining offsets count only the filtered set

#### Scenario: Other sorts carry no letters

- **WHEN** a client requests `libraryItems` with `sort: "year"`
- **THEN** `letters` is empty

### Requirement: Web browse offers a letter rail on a title sort

The web browse view SHALL show a letter rail while the order is by title and the filtered set holds
more than 60 items. The rail SHALL offer `#` and A to Z; letters absent from the filtered set SHALL
be disabled and skipped by focus, and each letter's accessible name SHALL give its title count.
Choosing a letter SHALL bring its first item into view without loading earlier pages.

#### Scenario: Jump to a letter

- **WHEN** a user activates `S` on the rail of a title-sorted library
- **THEN** the grid scrolls to the first "S" item, only the pages in view load, and focus moves to
  that item

#### Scenario: Short or differently sorted lists

- **WHEN** the user sorts by year, or the filtered set holds 60 items or fewer
- **THEN** no letter rail is shown

#### Scenario: Current letter follows the scroll

- **WHEN** the user scrolls the grid until the first "M" item reaches the top
- **THEN** `M` is marked current on the rail

#### Scenario: Cramped viewport

- **WHEN** the viewport is narrower than 360px or shorter than 520px
- **THEN** a "Jump to" control replaces the rail and opens a sheet of the same letters

### Requirement: The letter rail is keyboard operable

The rail SHALL be a single tab stop with an accessible name. Activating a letter (click or Enter)
SHALL move focus to its first item. Arrow keys, Home and End SHALL move along enabled letters and
scroll the grid while focus stays on the rail. Typing a letter SHALL jump to it, or to the next
letter with titles and announce the substitution.

#### Scenario: Arrow keys scan without leaving the rail

- **WHEN** focus is on `A` and the user presses ArrowDown
- **THEN** focus moves to the next letter with titles and the grid scrolls to its first item

#### Scenario: Typed letter with no titles

- **WHEN** no title starts with `J` and the user types "j" on the rail
- **THEN** the rail jumps to the next letter with titles and announces that none start with J

### Requirement: Web Library page offers pivot tabs

Every web Library page SHALL show, under the Library name, links to four pivots in this order:
Recommended (`/n/:nodeId`, the plugin's hub Layout), Library (`/n/:nodeId/all`), Collections
(`/n/:nodeId/collections`) and Categories (`/n/:nodeId/categories`). The open pivot SHALL carry
`aria-current="page"`, and each pivot SHALL be reachable directly by its address.

#### Scenario: Deep link to a pivot

- **WHEN** a viewer opens `/n/:nodeId/categories` for a Library node
- **THEN** the page shows the Library name, the four pivots with Categories marked current, and the
  Categories view

#### Scenario: Keyboard navigation

- **WHEN** a keyboard user tabs to the pivot row and activates the Collections link
- **THEN** the Collections pivot opens and its link is marked current

### Requirement: Collections pivot lists the Library's own Collections

The Collections pivot SHALL list only the Collections of the open Library, in curated order with
their item counts, each linking to its Collection page.

#### Scenario: Collections of another Library

- **WHEN** the household has Collections in Libraries A and B and the viewer opens A's Collections
  pivot
- **THEN** only A's Collections are listed

### Requirement: Categories pivot tiles each grouping value

The Categories pivot SHALL show one tile per value of each grouping the Library's plugin reports in
`libraryItems.facets.groupings`, grouped under the grouping's title. Activating a tile SHALL open
the Library pivot's Media view filtered to that grouping value.

#### Scenario: Open a category

- **WHEN** the plugin reports a grouping `genre` with value `Drama` and the viewer activates the
  `Drama` tile
- **THEN** the browser opens `/n/:nodeId/all?view=media&group.genre=Drama` and the grid lists only
  Media in that grouping value

#### Scenario: No groupings

- **WHEN** the Library's plugin reports no grouping
- **THEN** the Categories pivot shows an empty-state message

### Requirement: Entity-kind browse sorts and filters by reached Media

`libraryEntities` SHALL accept the `libraryItems` browse arguments (`sort`, `direction`, `seed`,
`watch`, `groupFilters`, `yearFrom`, `yearTo`, `quality`, `minRating`) with the same validation.
It SHALL judge each listed Entity by the visible Media linked to it or to its child Entities: the
earliest year, the latest added, the total duration, the latest watched and the highest Profile
rating. `total` SHALL count the filtered Entities while kind totals stay unfiltered.

#### Scenario: Sort an Entity kind by year

- **WHEN** a client asks for an Entity kind sorted by `year` descending
- **THEN** Entities order by the earliest year of the Media they reach, newest first, and Entities
  with no year come last

#### Scenario: Unwatched Entities

- **WHEN** a client asks for an Entity kind with `watch: "unwatched"`
- **THEN** only Entities that reach at least one Media the Profile has not watched are listed

#### Scenario: Group filter on an Entity kind

- **WHEN** a client filters an Entity kind by a grouping value
- **THEN** an Entity linked to that value, or reaching Media linked to it, is listed, and others are
  not

#### Scenario: Invalid argument

- **WHEN** a client sends an unknown sort to `libraryEntities`
- **THEN** the read fails with a client error

### Requirement: Entity-kind browse reports a letter index

For a title sort, `libraryEntities` SHALL answer `letters`, the offset and count of each title
initial in the filtered set (`#` for titles that do not open with a letter); any other sort SHALL
answer an empty list.

#### Scenario: Jump to a letter

- **WHEN** a client requests the offset a letter reports
- **THEN** the first Entity of the page opens with that initial

### Requirement: Web Entity-kind view offers the browse toolbar and letter rail

The web Entity-kind view SHALL offer the Media view's sort, direction and filter controls and its
letter rail, keeping their state in the URL with the same keys as the Media view.

#### Scenario: Shared filtered address

- **WHEN** a viewer opens `/n/:nodeId/all?view=<kind>&sort=year&dir=desc&watch=unwatched`
- **THEN** the Entity-kind view opens sorted by year, newest first, showing only unwatched Entities

### Requirement: Library browse sorts by release date, audience score and plays

`libraryItems` and `libraryEntities` SHALL accept the sort keys `release-date` (the `release-date`
field, else 1 January of the year), `audience-score` (the 0 to 10 `community-rating` field) and
`plays` (the Profile's play count, unplayed counting 0). Items without the key SHALL sort last in
both directions. An Entity SHALL take the earliest release, highest score and total plays of the
visible Media it reaches.

#### Scenario: Newest release first

- **WHEN** a client asks for a Library sorted by `release-date` descending
- **THEN** Media order by release date, newest first, a Media with only a year sorts as 1 January
  of that year, and Media with neither come last

#### Scenario: Best audience score first

- **WHEN** a client asks for a Library sorted by `audience-score` descending
- **THEN** Media order by their audience score, highest first, and unscored Media come last

#### Scenario: Most played first

- **WHEN** a Profile asks for a Library sorted by `plays` descending
- **THEN** Media order by that Profile's play count, most played first, with unplayed Media last

### Requirement: Web Library browse offers a year range

The web Library browse SHALL offer a release-year range kept in the URL as `from` and `to`, sent as
`yearFrom` and `yearTo`. Setting a range SHALL clear the decade filter and setting a decade SHALL
clear the range. An active range SHALL show a chip that clears it.

#### Scenario: Shared year range

- **WHEN** a viewer opens `/n/:nodeId/all?from=1990&to=1999`
- **THEN** the browse reads Media released from 1990 to 1999 and shows a chip naming that range

### Requirement: Web Library browse offers a table view

The web Library browse SHALL offer a table view mode: dense rows without artwork that show each
item's title, year, runtime and audience score where it has them, remembered per Library with the
other view modes.

#### Scenario: Switch to the table view

- **WHEN** a viewer picks the table view
- **THEN** the Library's items render as dense rows with their year and runtime, and the choice is
  kept for that Library on the next visit

### Requirement: Web viewer grids offer a poster size

The web Library toolbar SHALL offer Small, Medium and Large poster sizes, Large being the default
and the size the grids had before. The choice SHALL be kept per browser and SHALL set the column
floor of every viewer poster grid, so a smaller size fits more columns. The letter rail, keyboard
focus, card titles and lazy image loading SHALL keep working at every size.

#### Scenario: Smaller posters fit more per row

- **WHEN** a viewer picks Small in a Library toolbar
- **THEN** that grid and every other viewer poster grid show more columns than at Large

#### Scenario: The size is remembered

- **WHEN** a viewer picks Medium and reloads the page
- **THEN** the grids still draw at Medium

### Requirement: Library browse narrows by title text

`libraryItems` and `libraryEntities` SHALL accept an optional `query` string of at most 200
characters and keep only rows whose title or sort title contains every word of it, ignoring case,
accents and punctuation. A blank `query` SHALL keep every row. Facets SHALL describe the library
without the text filter.

#### Scenario: Words in any order

- **WHEN** a client requests `libraryItems` with `query: "bunny BIG"` and the library holds an item
  titled "Big Buck Bunny"
- **THEN** that item is in the response and `total` counts only matching items

#### Scenario: Too long

- **WHEN** a client requests `libraryItems` with a `query` longer than 200 characters
- **THEN** the read answers a client error and returns no items

### Requirement: Web browse toolbar is one row with a Filters panel

The web Library browse view SHALL show one toolbar row holding a labelled search box, the view
switcher, a Filters button showing the number of active filters, one Sort control for field and
direction, the poster size and the display choice. The filters SHALL open in a popover on wide
screens and a bottom sheet on phones.

#### Scenario: Filters panel

- **WHEN** a viewer activates the Filters button
- **THEN** the panel opens, the button reports it is expanded, and Escape closes it and returns
  focus to the button

### Requirement: Web browse search and active filter chips

The web Library search text SHALL be kept in the URL as `q` and sent to the server after the viewer
stops typing; the search box SHALL have its own clear button. Active filters SHALL show as chips
named "Remove <filter>", with a Clear all control that clears the filters and the search.

#### Scenario: Search narrows the library

- **WHEN** a viewer types a word into the library search
- **THEN** the URL carries `q`, the count and grid show only matching items, and the search box's
  clear button removes it

#### Scenario: Search matches nothing

- **WHEN** a viewer searches for text no title holds
- **THEN** the view says nothing matches the search and offers to clear it

### Requirement: Category counts use the visible browse unit

The Categories pivot SHALL count distinct outermost container IDs when every visible member of a category belongs to the same plugin-declared browse container kind. A category of loose Media, mixed units, or a kind without a browse label SHALL retain its item count. Raw Media counts SHALL remain available, and visibility filtering SHALL precede counting.

#### Scenario: Several children of two containers

- **WHEN** a category contains several visible Media from two outermost containers of one browse kind
- **THEN** its tile shows two with the plugin's label, irrespective of the number of children

#### Scenario: Loose Media

- **WHEN** a category contains three visible Media without containers
- **THEN** its tile shows three items

### Requirement: Category count labels use the plugin's names

A container count SHALL be named with the plugin's browse label for that kind, and with the plugin's singular display name when the count takes the display locale's singular form. Without a singular name, a singular count SHALL read as an item count.

#### Scenario: One container

- **WHEN** a category's visible Media all belong to one outermost container of a kind whose plugin declares a singular display name
- **THEN** its tile shows one with the singular display name, not the plural browse label

### Requirement: Relation-based Entity browse eligibility

A Library plugin's compiled browse rules SHALL restrict eligibility for the declared Entity kinds by their relations in the Library. A candidate SHALL be eligible when any configured source matches. Kind totals, listed items, filtered totals and letter offsets SHALL use the same eligibility in catalog and unsealed Encrypted Libraries. Kinds without a rule SHALL keep their existing eligibility. Browse eligibility SHALL leave Entity detail, search and stored relations intact.

#### Scenario: Music Artists browse

- **WHEN** an untrashed artist is linked as an album artist from an untrashed album, or is credited on an untrashed track with no album-track relation
- **THEN** the Artists browse view includes that artist

#### Scenario: Credit on a track belonging to an album

- **WHEN** an artist is only credited on tracks with an album-track relation
- **THEN** the Artists view omits that artist and search and track credits retain the artist Entity

### Requirement: Web reopens a Library on its last pivot

The web SHALL remember, per Library navigation node in that browser, the last pivot shown. The
sidebar link to a Library SHALL open its remembered pivot, else Recommended. An address that names a
pivot, including the Library's own address for Recommended, SHALL still open that pivot. Blocked
storage or an unknown stored value SHALL fall back to Recommended.

#### Scenario: Returning to the Library pivot

- **WHEN** a viewer opens a Library's Library pivot, goes Home, then picks that Library in the sidebar
- **THEN** `/n/:nodeId/all` opens with the Library pivot marked current

#### Scenario: Explicit address wins

- **WHEN** the remembered pivot is Library and the viewer opens `/n/:nodeId` directly
- **THEN** the Recommended pivot opens

#### Scenario: Storage blocked

- **WHEN** browser storage throws on read
- **THEN** the sidebar link opens `/n/:nodeId`

### Requirement: Categories pivot cards have a designed fallback

Every featured Categories card SHALL keep one shape whatever its artwork. A card without a backdrop SHALL show a mosaic of up to four distinct container covers from the items it reads, and without covers SHALL show a surface tinted from the Library accent and a theme palette tone chosen deterministically from the value's name, with the value's name set large in the theme's primary text colour. The featured grid SHALL show at least two cards per row at phone widths without horizontal scrolling.

#### Scenario: A value without artwork

- **WHEN** a featured value's items carry no backdrop and no cover
- **THEN** its card shows the tinted surface with the value's name and count
- **AND** the same name gets the same tint on every visit

#### Scenario: A value with container covers only

- **WHEN** a featured value's items carry no backdrop and belong to four containers with covers
- **THEN** its card shows those four covers as a mosaic under the label

### Requirement: Web poster size is one app-wide preference offered on every pivot

The web viewer SHALL keep one Small, Medium or Large poster size preference for the whole app.
Every Library pivot (Recommended, Library, Collections, Categories) SHALL offer the control for it
whenever it shows its pivots, in every view mode, and a change on any page SHALL reach every page.

#### Scenario: One choice across pivots

- **WHEN** a viewer picks Medium on the Recommended pivot and opens the Library pivot
- **THEN** the Library pivot's control shows Medium and its grid draws at Medium

#### Scenario: A choice on any pivot reaches Home

- **WHEN** a viewer picks Small on a Library's Categories pivot and opens Home
- **THEN** Home's shelf cards draw at the Small width

### Requirement: Every web thumbnail surface follows the poster size

Every viewer thumbnail surface SHALL draw at the size the poster size preference sets: poster
grids, shelf cards on Home, Recommended and detail rows, the Collections poster grid, the
Categories cards, the pivot tiles, the cast row, List thumbnails and detail Media rows. Large
SHALL keep each surface's prior size.

#### Scenario: Smaller shelf posters on Recommended

- **WHEN** a viewer picks Small on a Library's Recommended pivot
- **THEN** that page's shelf cards draw narrower than at Large

#### Scenario: Large keeps today's render

- **WHEN** a viewer has never picked a size
- **THEN** every thumbnail surface draws at the size it had before this change

### Requirement: The web poster size roams with the Profile

The web viewer SHALL save a poster size pick to the signed-in Profile as the per-Profile client
preference `web.poster-size`, and SHALL draw a Profile's saved size in any browser once that
Profile's preferences load. A Profile with no saved size SHALL keep the browser's own value, and
the web viewer SHALL NOT write a size to a Profile the viewer did not pick it under.

#### Scenario: A pick follows the Profile to another browser

- **WHEN** a viewer picks Small in one browser and signs in to the same Profile in another
- **THEN** the other browser draws Small once the Profile's preferences load

#### Scenario: No saved size keeps the browser's value

- **WHEN** a Profile with no saved size opens the viewer in a browser whose value is Medium
- **THEN** the viewer draws Medium and saves nothing to the Profile

#### Scenario: A Profile switch drops a queued save

- **WHEN** a save is still queued as the identity changes (Profile switch, reveal or relock)
- **THEN** the save is not sent under the new session

### Requirement: Library browse sorts by a plugin-declared text Media field

`libraryItems` SHALL accept `sort: "field:<name>"` for a text Media field the Library's plugin
declares Sortable, ordering by its value ignoring case, then by title. Items with no value SHALL
come last in both directions, and the letter index SHALL be computed from the value, leaving them
out. Any other field sort SHALL answer a client error, as SHALL a field sort on `libraryEntities`.

#### Scenario: Ordered by artist

- **WHEN** a client requests `libraryItems` with `sort: "field:artist"` on a Library whose plugin
  declares `artist` as a Sortable text field
- **THEN** items come back ordered by artist, items without an artist come last, and `letters`
  report where each artist initial starts

#### Scenario: Undeclared field

- **WHEN** a client requests `libraryItems` with `sort: "field:tempo"` and the plugin does not
  declare `tempo` as a Sortable text field
- **THEN** the read answers a client error and returns no items

### Requirement: Library browse text matches Searchable Media fields

The `libraryItems` text filter SHALL also keep an item when every word of `query` appears in its
title together with the values of the text Media fields the Library's plugin declares Searchable,
ignoring case, accents and punctuation.

#### Scenario: Artist and title in one search

- **WHEN** a client requests `libraryItems` with `query: "abba waterloo"` and an item titled
  "Waterloo" has the Searchable field `artist` set to "ABBA"
- **THEN** that item is in the response

### Requirement: The web karaoke Library opens on a songbook and the party

On the web, a karaoke Library SHALL open on a Songbook pivot in place of Recommended and Library,
keeping Collections and Categories. The Songbook SHALL search artist and title together, group
songs under letter headers with the letter rail, and offer Play and Add for a singer on each song.
A party rail beside it SHALL start a party, or show the join code, stage, requests and rotation.

#### Scenario: Host adds a song for a singer

- **WHEN** a host searches the karaoke Library for an artist and chooses Add for a song
- **THEN** they pick a recent singer or type a new name, and the song joins the rotation for that
  singer without leaving the Library page

#### Scenario: No party yet

- **WHEN** the host opens the karaoke Library with no party running
- **THEN** the rail offers to start one over this Library
