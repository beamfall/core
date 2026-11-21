## ADDED Requirements

### Requirement: A Profile can hide one Media item

The server SHALL accept `media` as a Mute kind whose target is one Media ID, and SHALL remove that
Media item from the Profile's shelves and library browse while the Mute stands. Other Profiles SHALL
be unaffected.

#### Scenario: Hide a film

- **WHEN** a Profile sets a `media` Mute on a film
- **THEN** that film is absent from the Profile's shelves and library browse
- **AND** another Profile still sees it

#### Scenario: Unhide

- **WHEN** the Profile clears the Mute
- **THEN** the film returns to its shelves and browse

### Requirement: Hiding an Entity hides its descendants

A direct Entity Mute SHALL reach every Media item linked to the Entity or to an Entity below it
along the parent relations the active plugins declare, and Entity browse SHALL omit the muted
Entity whether or not the browse is filtered.

#### Scenario: Hide a show

- **WHEN** a Profile hides a show
- **THEN** none of its episodes appear in Continue Watching, Up Next or Recently Added for that
  Profile
- **AND** the show is absent from that Profile's Entity browse

#### Scenario: Search still finds it

- **WHEN** the Profile searches for the hidden show's title
- **THEN** the show is returned, so it can be opened and unhidden

### Requirement: Mute changes reach the Profile's other sessions

Setting, clearing or clearing all Mutes SHALL append an engagement invalidation for the Profile.

#### Scenario: Second tab

- **WHEN** a Profile hides an item in one session
- **THEN** its other sessions receive an invalidation and refetch

### Requirement: A Profile can hide watched Media

When the Profile's `profile.hide-watched` client pref is `"on"`, the server SHALL omit Media the
Profile has finished from catalog shelves and from library browse that names no watch filter, and
SHALL omit Entities whose reached Media are all finished. The Profile's own engagement lists and an
explicit watch filter SHALL ignore the pref.

#### Scenario: Recently Added

- **WHEN** hide watched is on and the Profile has finished a recently added film
- **THEN** Recently Added omits it

#### Scenario: Continue Watching unaffected

- **WHEN** hide watched is on
- **THEN** Continue Watching, Watchlist and Recently Played are unchanged

#### Scenario: Explicit filter

- **WHEN** hide watched is on and the browse asks for watched Media
- **THEN** the finished Media are returned

#### Scenario: Off

- **WHEN** the pref is absent
- **THEN** finished Media appear as before

### Requirement: The web toggles both with one action

The web SHALL offer Hide and Unhide in the card More menu and on the detail page, with an Undo
after Hide, and a Hide watched switch on the library toolbar and in Profile settings, each taking
effect without a reload.

#### Scenario: Hide from a card

- **WHEN** the user chooses Hide in a card's More menu
- **THEN** the card leaves the shelf and an Undo control appears
- **AND** choosing Undo restores it

#### Scenario: Toggle hide watched

- **WHEN** the user flips Hide watched on the library toolbar
- **THEN** the grid refetches without finished items, and the switch in Profile settings shows the
  same state
