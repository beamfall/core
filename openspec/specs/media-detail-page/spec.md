# media-detail-page Specification

## Purpose
Defines the web Media detail route: what a single Media shows, how it loads and how it links back to its library.

## Requirements

### Requirement: One Media item has its own page

The web viewer SHALL draw one Media item at `/media/:mediaId`. The page SHALL show the item's
title as its level-one heading, its year and summary when known, and every field returned by
`mediaFields` for the item, each named by its display label (the field name when it has none).
When the card that opened the page carried artwork slots, the page SHALL show that artwork.
Core SHALL NOT name any field or use a domain word.

#### Scenario: Header from the server

- **WHEN** a viewer opens `/media/m1` and the server returns the title "Item One", year 2001 and
  a field labelled "Studio" with value "North"
- **THEN** the page heading is "Item One", the year 2001 is shown and the metadata lists "Studio"
  with "North"

#### Scenario: Instant paint from the card

- **WHEN** a viewer opens the page from a card titled "Item One"
- **THEN** the heading "Item One" is shown before the server answers

### Requirement: The page plays or resumes the item

The page SHALL offer one primary action that opens the player for the item. When the opening card
carried a position and a duration and the item is not complete, the action SHALL read "Resume" and
the page SHALL show a progress bar exposed as a progressbar with the percentage watched; otherwise
the action SHALL read "Play" and no progress bar is shown.

#### Scenario: Known progress

- **WHEN** the opening card carried position 600 seconds of 2400
- **THEN** the action reads "Resume" and the progress bar reports 25 percent

#### Scenario: No progress known

- **WHEN** the page is opened from a deep link
- **THEN** the action reads "Play" and activating it opens `/watch/m1`

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
