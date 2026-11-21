## MODIFIED Requirements

### Requirement: Listening wording for audio Media

The web viewer SHALL use listening wording in audio Media card menus and item actions.
An audio Entity listing SHALL NOT show a remaining count, and the item selected for the
primary Play or Resume action SHALL be labelled “Up next”. Audio item actions SHALL omit
the watchlist button. Video surfaces SHALL retain watching wording and the watchlist button.

#### Scenario: Audio card menu

- **WHEN** a listener opens the menu of an audio Media card
- **THEN** the entry reads “Mark played”

#### Scenario: Audio item actions

- **WHEN** a listener opens an audio item with no completed play
- **THEN** the action reads “Mark played” and the watchlist button is absent

#### Scenario: Audio Entity listing

- **WHEN** a listener opens an audio Entity with unplayed children
- **THEN** no remaining count is shown and the next child is labelled “Up next”
