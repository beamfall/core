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

When the primary action reads "Resume", the page SHALL show a progress bar exposed as a
progressbar with the percentage watched, and a secondary "Start over" action SHALL open the player
from the beginning (`?t=0`).

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

The page SHALL offer a labelled back control. It SHALL return to the previous page when the viewer
arrived from one within the app, and to Home otherwise.

#### Scenario: Back to the shelf

- **WHEN** a viewer opened the page from Home and activates Back
- **THEN** Home is shown again

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

Under the title the page SHALL show a row of facts: the year, the runtime when the opening card
carried a duration of at least a minute, the content rating, the community score, and the titles of
up to three distinct credits whose kind has the category archetype. Unknown facts SHALL be omitted.

#### Scenario: Facts from server and card

- **WHEN** the item has year 2001, a rating "PG-13", one category credit "Drama" and the card
  carried a duration of 5400 seconds
- **THEN** the facts row reads "2001", "1 h 30 min", "PG-13", "Drama" in that order

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
of its own. A long overview SHALL be clamped with a More control that expands it. At phone width the
header SHALL be one column with the primary action full width.

#### Scenario: Long overview

- **WHEN** the overview is longer than the clamp threshold
- **THEN** it is clamped and a More control expands it and then reads Less

### Requirement: The detail page carries the item's actions

The web Media detail page SHALL show the calling Profile's watched, watchlist and rating controls for
the Media item, acting on the `{mediaID}` target through the item-state API, and a More menu that
offers Add to playlist and Copy link. Play or Resume SHALL stay the only accent-filled action. Each
change SHALL show at once and SHALL come back from the server after a reload.

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
