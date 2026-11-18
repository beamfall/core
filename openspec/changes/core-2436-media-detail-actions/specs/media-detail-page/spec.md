## ADDED Requirements

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
