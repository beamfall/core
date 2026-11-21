# web-viewer-navigation Specification

## Purpose
Keep the web viewer easy to navigate: each page names the browser tab after what it shows, so history, bookmarks and tab strips are readable.

## Requirements

### Requirement: Viewer pages name the browser tab

Each web viewer page SHALL set the document title to its main heading followed by the product
name, and the player SHALL use the playing item's title. A Revealed session SHALL show the product
name alone.

#### Scenario: Library page

- **WHEN** a viewer opens the Library "Music"
- **THEN** the tab title is "Music · Beamfall"

#### Scenario: Revealed session

- **WHEN** a Revealed session opens any viewer page
- **THEN** the tab title is "Beamfall"

### Requirement: Profiles pin, unpin and reorder sidebar libraries

The web Viewer sidebar SHALL let the signed-in Profile pin, unpin and reorder its libraries, by
keyboard as well as by drag, and SHALL store that choice per Profile as the client preference
`web.sidebar-libraries`. Unpinned libraries SHALL remain reachable from a "More" disclosure.

#### Scenario: Profile reorders and unpins

- **WHEN** a Profile moves "Music" above "Films", unpins "Photos" and presses Done
- **THEN** the sidebar lists "Music" then "Films", "Photos" appears only under More, and the
  choice is saved as `web.sidebar-libraries` for that Profile only

### Requirement: Profile sidebar choice layers over the server navigation override

The Profile's sidebar choice SHALL apply after the server-wide navigation override: it SHALL only
reorder or unpin libraries present in the server's navigation, SHALL ignore library IDs the server
no longer lists, and SHALL place libraries it does not name after the ordered ones, in server order.

#### Scenario: Server override hides a library

- **WHEN** the server-wide navigation override hides a library the Profile had pinned
- **THEN** the sidebar does not list it, pinned or under More

#### Scenario: New library

- **WHEN** a library the Profile's saved choice does not name is added
- **THEN** it appears pinned after the libraries the choice orders

### Requirement: The desktop sidebar is resizable

The expanded desktop Viewer sidebar SHALL default to 200 px and SHALL offer a focusable separator on
its inline-end edge that resizes it between 168 px and 360 px by pointer drag and by keyboard,
exposes its width through `aria-valuenow`, `aria-valuemin` and `aria-valuemax`, and resets to the
default on double-click. A single click or tap on the separator without dragging SHALL open a
width control that narrows, widens and resets the sidebar with single presses, so no width needs a
dragging movement. The collapsed icon rail and the phone drawer SHALL NOT offer it.

#### Scenario: Keyboard resize

- **WHEN** a person focuses the separator and presses ArrowRight twice
- **THEN** the sidebar is 232 px wide and the separator reports `aria-valuenow` 232

#### Scenario: Resize without dragging

- **WHEN** a person clicks the separator without moving it and presses Wider twice
- **THEN** the sidebar is 232 px wide and the width persists like a dragged width

#### Scenario: A drag does not open the width control

- **WHEN** a person drags the separator to a new width and releases it
- **THEN** the sidebar takes the dragged width and no width control opens

#### Scenario: Phone layout

- **WHEN** the viewport is phone width
- **THEN** the sidebar opens as a drawer with no resize separator

### Requirement: The sidebar width persists per browser

The chosen sidebar width SHALL persist per browser and be restored on load, clamped to the limits.
When storage is unavailable or holds an invalid value the sidebar SHALL use the default width.

#### Scenario: Width survives a reload

- **WHEN** a person resizes the sidebar to 280 px and reloads the page
- **THEN** the sidebar opens at 280 px

#### Scenario: Out-of-range stored width

- **WHEN** the browser's stored width is 900 px
- **THEN** the sidebar opens at the 360 px maximum
