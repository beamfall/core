## MODIFIED Requirements

### Requirement: Known progress offers Start over

When the primary action reads "Resume", the Resume button SHALL carry a progress bar along its
bottom edge, exposed as a progressbar with the percentage watched, and a secondary "Start over"
action SHALL open the player from the beginning (`?t=0`). The page SHALL NOT draw the progress as a
row of its own.

#### Scenario: Known progress

- **WHEN** the opening card carried position 600 seconds of 2400
- **THEN** the action reads "Resume", the progress bar reports 25 percent and Start over opens
  `/watch/m1?t=0`

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

### Requirement: Facts row

Under the title the page SHALL show one meta line: the item's date or year, the runtime when it is
at least a minute, the content rating and the community score, in that order and as text separated
by dots, followed by the stream badges. Under the meta line it SHALL show the item's
category-archetype credits as plain text links: grouped by kind in credit order, the first two
kinds, at most two distinct credits of each, with the kinds separated by a dot and no count of the
remaining credits, which Details lists. Category credits of one kind are distinct by title: a name
that reads the same shows once. Each category credit with an entity id SHALL be a
keyboard-focusable link to that Entity's page whose accessible name is the credit's title. Unknown
facts SHALL be omitted.

#### Scenario: Facts from server and card

- **WHEN** the item has year 2001, a rating "PG-13", one category credit "Drama" and the card
  carried a duration of 5400 seconds
- **THEN** the meta line reads "2001", "1 h 30 min", "PG-13" in that order and the category line
  reads "Drama"

#### Scenario: Category links without overflow chips

- **WHEN** the item has two credits of one category kind and three of another
- **THEN** the category line shows both credits of the first kind and the first two of the second,
  shows no "+1", and each credit links to `/entity/<its entity id>`

### Requirement: One stage surface and restrained motion

The header SHALL be one surface with no card nested inside it. When the item has a backdrop slot it
SHALL be drawn behind the header under a gradient scrim of the root background and SHALL fade in by
opacity only, with no transition under reduce-motion. The page SHALL draw no blur or ambient effect
of its own. The poster's top SHALL align with the top of the copy column. The copy column SHALL read
in this order: parents and position, title, meta line, categories, overview, crew, actions. A long
overview SHALL be clamped with a More control that expands it. At phone width the header SHALL be
one column with the primary action full width.

#### Scenario: Long overview

- **WHEN** the overview is longer than the clamp threshold
- **THEN** it is clamped and a More control expands it and then reads Less

#### Scenario: Overview before actions

- **WHEN** the item has an overview
- **THEN** the overview comes before the Play button in document order

### Requirement: The detail page carries the item's actions

The web Media detail page SHALL show the calling Profile's watched, watchlist and rating controls for
the Media item, acting on the `{mediaID}` target through the item-state API, and a More menu that
offers Add to playlist and Copy link. The actions SHALL be grouped: first the playback actions (Play
or Resume, Start over, Next, Watch together, Play on and the More menu) on one row, then the track
pickers, then the Profile's own controls, drawn quieter than the playback row. Play or Resume SHALL
stay the only accent-filled action. Each change SHALL show at once and SHALL come back from the
server after a reload.

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
