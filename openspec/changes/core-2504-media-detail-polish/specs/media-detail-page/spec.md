## MODIFIED Requirements

### Requirement: Facts row

Under the title the page SHALL show a row of facts: the year, the runtime when the opening card
carried a duration of at least a minute, the content rating, the community score, and the item's
category-archetype credits. Category credits SHALL be grouped by kind in credit order; the row
SHALL show the first two kinds, at most two distinct credits of each, followed by a "+N" chip
counting that kind's remaining credits. Category credits of one kind are distinct by title: a name
that reads the same shows once. Each category chip with an entity id SHALL be a
keyboard-focusable link to that Entity's page whose accessible name is the credit's title. Unknown
facts SHALL be omitted.

#### Scenario: Facts from server and card

- **WHEN** the item has year 2001, a rating "PG-13", one category credit "Drama" and the card
  carried a duration of 5400 seconds
- **THEN** the facts row reads "2001", "1 h 30 min", "PG-13", "Drama" in that order

#### Scenario: Category chips link and overflow

- **WHEN** the item has two credits of one category kind and three of another
- **THEN** the row shows both credits of the first kind, the first two of the second and a "+1"
  chip, and each credit chip links to `/entity/<its entity id>`

### Requirement: Loading, missing and failed states

While the page has nothing to paint it SHALL show a skeleton marked busy with the stage's shape:
backdrop space, poster frame, title, facts, actions and overview. A page painted from the opening
card SHALL keep the backdrop, facts and overview space reserved until the item arrives, and the
Details section SHALL render before rows that load on their own, so loading does not move what is
already painted. When the server reports the item unknown or not visible to the Profile, the page
SHALL show the generic "not available" state without revealing which. When the request fails for
another reason, the page SHALL show an error with a retry control.

#### Scenario: Unknown item

- **WHEN** the server answers `media(id)` with "unknown media"
- **THEN** the page shows "Not available" and no title, artwork or action

#### Scenario: Request fails

- **WHEN** the request fails with a network error
- **THEN** the page shows an error and a "Try again" control that repeats the request

#### Scenario: Skeleton keeps the stage's shape

- **WHEN** the page opens with nothing cached and no opening card
- **THEN** the busy skeleton reserves the backdrop space, a poster frame, the title, a facts row,
  the actions and overview lines

## ADDED Requirements

### Requirement: Details lists the item's credits

The Details section SHALL list each category kind, labelled with the kind's display name, with every
distinct credit (by title, as in the facts row) linked to its Entity page, then person credits
carrying a `job` attribute grouped under the attribute's value and linked, each person once by entity
id so two people sharing a name both appear, then the viewer-facing fields. Empty rows SHALL be omitted.

#### Scenario: Film credits

- **WHEN** a film has two genre credits, one studio credit and a credit with job "Director"
- **THEN** Details lists the genre kind with both genres linked, the studio kind with the studio
  linked and "Director" with that person linked

### Requirement: Details lists the selected Version's file

The Details section SHALL list, for the Version the pickers show (the picked one, else the
remembered one, else the server's default), its label, its video codec with its HDR format, and its
default audio track's language, codec and channel layout, omitting what is unknown. The page SHALL
read the track listing once and share it between the pickers and Details.

#### Scenario: Film file

- **WHEN** a film has one Version with HEVC HDR10 video and a 6-channel E-AC-3 English default
  audio track
- **THEN** Details lists the video as "HEVC · HDR10" and the audio as "English · E-AC-3 · 5.1"

#### Scenario: Listing fails

- **WHEN** the track listing fails
- **THEN** Details omits the Version, video and audio rows and still lists the credits and fields

### Requirement: Bounded reads when the page opens

Opening a Media detail page SHALL send fewer than 15 GraphQL requests. Each read SHALL be sent once
per item, and none SHALL be aborted and sent again while the page stays open.

#### Scenario: Film opens

- **WHEN** a viewer opens a film's detail page with nothing cached
- **THEN** the page sends fewer than 15 GraphQL requests, none repeated and none aborted

## REMOVED Requirements

### Requirement: More from the same section

**Reason**: The shelf showed the rest of whatever row the item was opened from. Opened from
Continue Watching, that row is unrelated to the item (CORE-2507). The rows below the stage now all
relate to the item.

**Migration**: None. Extras, Editions, More with and Related remain.
