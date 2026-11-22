## ADDED Requirements

### Requirement: Screensaver setting

The web client SHALL offer, in the slideshow setup sheet, a per-device option that opens the
slideshow when the client is idle. The option SHALL be off by default and SHALL be stored in
browser storage. The screensaver SHALL play the slideshow Source this device last played, counting a Source as
played once one of its slides has shown.

#### Scenario: Off by default

- **WHEN** a device has never changed the option
- **THEN** an idle client opens nothing

#### Scenario: Last Source

- **WHEN** a device with the option on last played a Collection's slideshow
- **THEN** the screensaver plays that Collection

### Requirement: Idle trigger

With the option on, the client SHALL open the slideshow in screensaver mode after five minutes with
no key, pointer, wheel, touch or scroll input, counted only while the tab is visible, a browsing
route is shown, no dialog is open, no text field has focus and no text field holds unsaved text. The player, image viewer,
slideshow, Profile picker and remote routes SHALL NOT count as idle.

#### Scenario: Playing video

- **WHEN** a viewer watches a video in the player for ten minutes without input
- **THEN** the screensaver does not open

#### Scenario: Input resets the count

- **WHEN** a viewer moves the pointer four minutes into the idle count
- **THEN** the count starts again from zero

### Requirement: Screensaver mode

In screensaver mode the slideshow SHALL hide its control bar and setup sheet. The first key press,
click, touch, wheel or deliberate pointer move SHALL close it, return to the page it opened over
and do nothing else. When the Source is missing, fails to load or has no images once every page has loaded, the screensaver
SHALL close at once.

#### Scenario: Any key closes it

- **WHEN** a viewer presses the right arrow while the screensaver plays
- **THEN** the screensaver closes and the slide does not step

#### Scenario: Empty Source

- **WHEN** the last Source lists no images for the acting Profile
- **THEN** the screensaver closes and the page underneath stays as it was
