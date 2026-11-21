# media-detail-page Specification

## Purpose
Defines the web Media detail route: what a single Media shows, how it loads and how it links back to its library.

## Requirements

### Requirement: One Media item has its own page

The web viewer SHALL draw one Media item at `/media/:mediaId`. The page SHALL show the item's
title as its level-one heading, its summary when known, and the viewer-facing fields returned by
`mediaFields` for the item, each named by its display label. When `media(id)` or the card that
opened the page carries artwork slots, the page SHALL show that artwork. Core SHALL NOT name any
field or use a domain word.

#### Scenario: Header from the server

- **WHEN** a viewer opens `/media/m1` and the server returns the title "Item One", year 2001 and
  a field labelled "Studio" with value "North"
- **THEN** the page heading is "Item One", the year 2001 is shown and the metadata lists "Studio"
  with "North"

#### Scenario: Instant paint from the card

- **WHEN** a viewer opens the page from a card titled "Item One"
- **THEN** the heading "Item One" is shown before the server answers

### Requirement: Only viewer-facing fields reach the page

A field SHALL be viewer-facing when its plugin declared it with a display label, it is not the
`class` discriminant, its type is not `flag`, its value is not empty and the facts row does not
already show it. Fields that are not viewer-facing SHALL stay out of the Viewer and remain listed
in Manage.

#### Scenario: Bookkeeping fields stay in Manage

- **WHEN** the item's fields include an undeclared `index`, the `class` field labelled "Class" and
  a `flag` field labelled "Grouped Locally"
- **THEN** none of them is listed on the page, and the Details section is left out when no other
  field remains

### Requirement: The page plays or resumes the item

The page SHALL offer one primary action that opens the player for the item. It SHALL be the only
accent-filled control on the page and at least 44 pixels tall. When the opening card carried a
position and a duration and the item is not complete, the action SHALL read "Resume"; otherwise it
SHALL read "Play" and no progress bar or Start over is shown.

#### Scenario: No progress known

- **WHEN** the page is opened from a deep link
- **THEN** the action reads "Play" and activating it opens `/watch/m1`

### Requirement: Known progress offers Start over

When the primary action reads "Resume", the Resume button SHALL carry a progress bar along its
bottom edge, exposed as a progressbar with the percentage watched, and a secondary "Start over"
action SHALL open the player from the beginning (`?t=0`). The page SHALL NOT draw the progress as a
row of its own.

#### Scenario: Known progress

- **WHEN** the opening card carried position 600 seconds of 2400
- **THEN** the action reads "Resume", the progress bar reports 25 percent and Start over opens
  `/watch/m1?t=0`

### Requirement: Loading, missing and failed states

While the page has nothing to paint it SHALL show a skeleton marked busy. When the server reports
the item unknown or not visible to the Profile, the page SHALL show the generic "not available"
state without revealing which. When the request fails for another reason, the page SHALL show an
error with a retry control.

#### Scenario: Unknown item

- **WHEN** the server answers `media(id)` with "unknown media"
- **THEN** the page shows "Not available" and no title, artwork or action

#### Scenario: Request fails

- **WHEN** the request fails with a network error
- **THEN** the page shows an error and a "Try again" control that repeats the request

### Requirement: Back navigation

The page SHALL offer a labelled back control at the top-left of the stage, inside it, so that the
sticky top bar never covers it. It SHALL return to the previous page when the viewer arrived from
one within the app, and to Home otherwise.

#### Scenario: Back to the shelf

- **WHEN** a viewer opened the page from Home and activates Back
- **THEN** Home is shown again

#### Scenario: Back inside the stage

- **WHEN** the page renders an item
- **THEN** the back control is a descendant of the stage, before the lockup

### Requirement: Poster cards open the page; rows and explicit Play buttons play

A Media card in a shelf or grid section SHALL open the item's page. A Media row in a list section
SHALL open the player directly. The hero's Play button SHALL open the player directly. Entity cards
SHALL keep opening the entity page.

#### Scenario: Poster in a shelf

- **WHEN** a viewer activates a Media card in a shelf section
- **THEN** `/media/<id>` opens

#### Scenario: Row in a list

- **WHEN** a viewer activates a Media row in a list section
- **THEN** `/watch/<id>` opens

### Requirement: More from the same section

When the item was opened from a section that is still in the viewer cache, the page SHALL show the
section's other items as a shelf headed with the section's title, without a new request. It SHALL
NOT show the shelf when no such section is cached or when the item is its only member.

#### Scenario: Siblings from cache

- **WHEN** a viewer opens item m1 from a cached section "Recent" holding m1, m2 and m3
- **THEN** the page shows a shelf "More from Recent" with m2 and m3

### Requirement: Facts row

Under the title the page SHALL show one meta line: the item's date or year, the runtime when it is
at least a minute, the content rating and the community score, in that order. The date, runtime and
score SHALL be text separated by dots; the content rating SHALL be a bordered badge, set apart by
space rather than a dot. The stream badges follow. Unknown facts SHALL be omitted.

#### Scenario: Facts from server and card

- **WHEN** the item has year 2001, a rating "PG-13", one category credit "Drama" and the card
  carried a duration of 5400 seconds
- **THEN** the meta line reads "2001", "1 h 30 min", "PG-13" in that order and the category line
  reads "Drama"

### Requirement: Category line

Under the meta line the page SHALL show the item's category-archetype credits as plain text links:
grouped by kind in credit order, the first two kinds, at most two distinct credits of each, with
the kinds separated by a dot and no count of the remaining credits. Credits of one kind are
distinct by title. Each category
credit with an entity id SHALL be a keyboard-focusable link to that Entity's page whose accessible
name is the credit's title.

#### Scenario: Category links without overflow chips

- **WHEN** the item has two credits of one category kind and three of another
- **THEN** the category line shows both credits of the first kind and the first two of the second,
  shows no "+1", and each credit links to `/entity/<its entity id>`

### Requirement: Content rating and community score in the facts row

The content rating SHALL read the `certification-display` field, falling back to `certification`,
and SHALL render as a bordered badge. The community score SHALL read the `community-rating` field
on a 0 to 10 scale, render with one decimal, show only when it and `community-rating-votes` are
both above zero, and carry an accessible name stating the field's label, the score out of 10 and
the vote count. These fields SHALL NOT also appear in the field list.

#### Scenario: Display rating and community score

- **WHEN** the item stores `certification-display` "TV-MA", `community-rating` 7.25 labelled
  "TMDB rating" and `community-rating-votes` 1200
- **THEN** the facts row shows a "TV-MA" badge and a score "★ 7.3" named "TMDB rating 7.3 out of
  10, 1,200 votes"

#### Scenario: No votes

- **WHEN** the item stores `community-rating` 0 and `community-rating-votes` 0
- **THEN** no score is shown

### Requirement: Container credits link up

Each credit whose kind has the container archetype SHALL link to its Entity page. A related Entity
of the nearest such container that is itself an ordered container of another kind, holding at
least as many items, SHALL be linked before it as the grouping above. When the reads behind the
grouping fail, the credit links SHALL remain.

#### Scenario: Grouping and parent

- **WHEN** the item is credited to container "Part Two", which is related to ordered container
  "The Whole" holding 9 items
- **THEN** the page links "The Whole" and then "Part Two" to their Entity pages

### Requirement: Next in the container's order

The page SHALL read the ordered Media of the item's nearest container (the smallest container list
holding the item). When an item follows this one in that order, the page SHALL offer a Next link to
its detail page; otherwise it SHALL offer none.

#### Scenario: A following item

- **WHEN** the nearest container's ordered Media are m0, m1, m2 and the page shows m1
- **THEN** Next opens `/media/m2`

#### Scenario: Last item

- **WHEN** the item is last in its container's order
- **THEN** no Next link is shown

### Requirement: One stage surface and restrained motion

The header SHALL be one surface with no card nested inside it. When the item has a backdrop slot it
SHALL be drawn behind the header under a gradient scrim of the root background and SHALL fade in by
opacity only, with no transition under reduce-motion. The page SHALL draw no blur or ambient effect
of its own. At phone width the header SHALL be one column with the primary action full width, and
a poster (not a wide still) SHALL sit small beside the heading.

#### Scenario: Backdrop under reduce-motion

- **WHEN** the item has a backdrop slot and the viewer prefers reduced motion
- **THEN** the backdrop is drawn behind the header under the scrim with no fade transition, and no
  card is nested inside the header

### Requirement: Copy column order

The poster's top SHALL align with the top of the copy column. The copy column SHALL read in this
order: parents and position, title, meta line, categories, overview, crew, actions. A long overview
SHALL be clamped with a More control that expands it.

#### Scenario: Long overview

- **WHEN** the overview is longer than the clamp threshold
- **THEN** it is clamped and a More control expands it and then reads Less

#### Scenario: Overview before actions

- **WHEN** the item has an overview
- **THEN** the overview comes before the Play button in document order

### Requirement: The detail page carries the item's actions

The web Media detail page SHALL show the calling Profile's watched, watchlist and rating controls for
the Media item, acting on the `{mediaID}` target through the item-state API, and a More menu that
offers Add to playlist and Copy link. Each change SHALL show at once and SHALL come back from the
server after a reload.

#### Scenario: Actions grouped with one accent

- **WHEN** a viewer opens the detail page of a playable item
- **THEN** the playback actions (Play or Resume, Start over, Next, Watch together, Play on and the
  More menu) sit on one row, the track pickers follow, then the Profile's own controls drawn
  quieter, and Play or Resume is the only accent-filled action

#### Scenario: Mark watched and rate

- **WHEN** a viewer opens the detail page of an unwatched, unrated item, chooses Mark watched and
  then four stars
- **THEN** the page calls `setWatched` and `setRating` for that `mediaID`, shows Mark unwatched and
  four filled stars, and a fresh load of the page shows the same state

#### Scenario: Add to playlist from the More menu

- **WHEN** a viewer opens the More menu and chooses Add to playlist
- **THEN** the playlist dialog opens for that Media item

#### Scenario: State load fails

- **WHEN** the item-state read fails
- **THEN** the controls report the failure with a retry, and Play still starts the item

### Requirement: More with the first billed person

The web Media detail page SHALL show a row titled with the name of the item's first
person-archetype credit in the server's order, listing at most 20 other Media credited to that
person. The row SHALL leave out Media the profile muted, as other passive-discovery rows do, and
SHALL NOT list the current item, and SHALL NOT be shown when no other Media remain or the read fails. Choosing a
card SHALL open that Media item's detail page.

#### Scenario: Lead person with other titles

- **WHEN** a Media item's first person credit is credited on two other Media
- **THEN** the page shows a "More with" row named for that person with those two cards and not the
  current item

#### Scenario: Muted title left out

- **WHEN** the profile marked one of the lead person's other Media "Not interested"
- **THEN** that Media is not in the "More with" row

#### Scenario: Lead person with no other titles

- **WHEN** the first person credit is credited only on the current item
- **THEN** the page shows no "More with" row

### Requirement: Related row

The web Media detail page SHALL show the Media `relatedMedia` returns for the item as a Related
row, and SHALL NOT show the row when none are returned or the read fails.

#### Scenario: Nothing related

- **WHEN** `relatedMedia` returns no Media for the item
- **THEN** the page shows no Related row

### Requirement: Ordered item fields on the Media read

The `media(id)` read SHALL carry the item's stored `sequence` and `index` ordinals, a `headline`
locale map rendered from the owning plugin's MediaSubtitle segments that name no ordinal
placeholder, the `classLabel` the plugin declares for the item's class, and the stored
`videoWidth` and `videoHeight`. Each SHALL be omitted when unknown.

#### Scenario: Ordered item with a stored title

- **WHEN** an item stores sequence 1, index 3 and the field the plugin's title segment names, and
  its class has a declared label
- **THEN** `media(id)` returns sequence 1, index 3, that title as `headline` and the class label

### Requirement: Ordered item hero

For an item with a container credit and an `index`, the page SHALL link the grouping above the
nearest container first, then show a position line of the nearest container's kind name and the
item's `sequence`, linked to that container, and the item's class label and `index`, and SHALL use
the `headline` as the H1, falling back to the title. The hero SHALL use the item's `thumb` artwork
in a wide frame when present, and the poster otherwise.

#### Scenario: Ordered item with a headline

- **WHEN** the item is credited to container "Part Two" of kind "Part", grouped under "The Whole",
  with sequence 2, index 5, class label "Chapter" and headline "Arrival"
- **THEN** the page links "The Whole", shows "Part 2 · Chapter 5" with "Part 2" linking to the
  container, and the H1 reads "Arrival"

#### Scenario: No headline

- **WHEN** the same item has no headline
- **THEN** the H1 reads the item's title

### Requirement: Air date and stream badges

The meta line SHALL show the item's first date field as a date and, after the text facts, the shown
Version's server resolution label as a resolution badge, an HDR badge when that Version reports
HDR, and its default audio track's codec and channel layout, each only when known. Each badge SHALL
appear once on the page.

#### Scenario: A UHD HDR item

- **WHEN** the item's only Version reports resolution `4K` and HDR10 and its default audio track is
  E-AC-3 with 6 channels
- **THEN** the meta line shows "4K", "HDR10" and "Dolby Digital Plus 5.1", and no other "4K" or
  "HDR10" badge is on the page

#### Scenario: A Version pick changes the badges

- **WHEN** an item has a 4K Version and a 1080p Version and the viewer picks the 1080p Version
- **THEN** the meta line shows "1080p" and no "4K"

### Requirement: More in the nearest container

For an item in an ordered container, the page SHALL show a rail headed "More in" and the nearest
container's title listing that container's other items in order, and the More with and Related rows
SHALL leave out that container's items.

#### Scenario: Container rail replaces repeats

- **WHEN** the item's container holds m1, m2 and m3 and the page shows m1, whose first billed person
  also appears in m2
- **THEN** a "More in" rail lists m2 and m3 and the More with row does not list m2

### Requirement: Grouping found without an item count

When no related Entity of the nearest container qualifies by its item count, the page SHALL take
the one related Entity of another kind with the container archetype as the grouping above it.

#### Scenario: Grouping without a count

- **WHEN** the nearest container relates to one container-archetype Entity of another kind that
  reports no item count
- **THEN** the page links that Entity before the container

### Requirement: Resolution and HDR badges

Each Version listed by `mediaTracks` SHALL carry the resolution label of its stored frame size when
that size is known. The web Media detail page SHALL show the shown Version's resolution label and,
when the Version is HDR, its HDR format (HDR10, HDR10+, HLG or Dolby Vision) as badges, and SHALL
show no badge for an unknown resolution or an SDR Version.

#### Scenario: 4K Dolby Vision title

- **WHEN** a title's only Version is 3840x2160 Dolby Vision
- **THEN** the detail page shows `4K` and `Dolby Vision` badges

#### Scenario: Unknown frame size

- **WHEN** the Version's frame size is not stored
- **THEN** no resolution badge is shown

### Requirement: Poster cards offer a one-click Play

A playable, unlocked Media card drawn as a poster or thumbnail SHALL offer a Play button that is a
sibling of the card link and its More trigger. It SHALL show on hover and keyboard focus, stay
visible without hover, be named Play or Resume (from the card's progress) with the item's title, and
open the player through the card's play path with the card's queue.

#### Scenario: Play from a poster

- **WHEN** a viewer activates the Play button on a Media poster card
- **THEN** `/watch/<id>` opens with the card's queue, and the card link still opens `/media/<id>`

#### Scenario: Keyboard

- **WHEN** a keyboard user tabs from the card link
- **THEN** focus reaches the card's Play button, which shows and is named "Play <title>" or
  "Resume <title>"

#### Scenario: Rows and locked items

- **WHEN** a card is drawn as a list or table row, or its item is locked
- **THEN** it shows no extra Play button

### Requirement: Change poster control

For a viewer who may edit metadata (a manager device on an adult Profile), the page SHALL offer a
labelled "Change poster" control over the poster. It SHALL be reachable by keyboard, shown on focus
and on hover, and stay visible where the device cannot hover. Other viewers SHALL NOT see it, nor
SHALL it show when the frame holds a wide still instead of a poster.

#### Scenario: Viewer without edit rights

- **WHEN** a child Profile or a non-manager device opens the page
- **THEN** no Change poster control is rendered

### Requirement: Poster picker dialog

The Change poster control SHALL open a dialog listing the item's poster candidates from the
existing artwork candidates query, with the applied one marked. Choosing a candidate SHALL pin it
through the existing pin mutation and reload the item, and focus SHALL return to the control when
the dialog closes.

#### Scenario: Admin picks a new poster

- **WHEN** a manager on an adult Profile opens Change poster and chooses a candidate
- **THEN** the candidate is pinned to the poster slot, the dialog closes and the item is reloaded

#### Scenario: No candidates

- **WHEN** the candidates query returns none
- **THEN** the dialog says no candidates are available
