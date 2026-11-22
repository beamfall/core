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
default on double-click. The collapsed icon rail and the phone drawer SHALL NOT offer it.

#### Scenario: Keyboard resize

- **WHEN** a person focuses the separator and presses ArrowRight twice
- **THEN** the sidebar is 232 px wide and the separator reports `aria-valuenow` 232

#### Scenario: Phone layout

- **WHEN** the viewport is phone width
- **THEN** the sidebar opens as a drawer with no resize separator

### Requirement: The sidebar width changes without dragging

A single click or tap on the sidebar separator without dragging SHALL open a width control that
narrows, widens and resets the sidebar with single presses, so no width needs a dragging movement.

#### Scenario: Resize without dragging

- **WHEN** a person clicks the separator without moving it and presses Wider twice
- **THEN** the sidebar is 232 px wide and the width persists like a dragged width

#### Scenario: A drag does not open the width control

- **WHEN** a person drags the separator to a new width and releases it
- **THEN** the sidebar takes the dragged width and no width control opens

### Requirement: The sidebar width persists per browser

The chosen sidebar width SHALL persist per browser and be restored on load, clamped to the limits.
When storage is unavailable or holds an invalid value the sidebar SHALL use the default width.

#### Scenario: Width survives a reload

- **WHEN** a person resizes the sidebar to 280 px and reloads the page
- **THEN** the sidebar opens at 280 px

#### Scenario: Out-of-range stored width

- **WHEN** the browser's stored width is 900 px
- **THEN** the sidebar opens at the 360 px maximum

### Requirement: The sidebar lists the Profile's own lists

For a signed-in Profile the web Viewer sidebar SHALL show a "Your stuff" group linking to the
existing Playlists, Favorites and History pages, on the expanded sidebar, the collapsed icon rail
and the phone drawer, so each is one activation from any Viewer page. The account menu SHALL keep
its links to the same pages.

#### Scenario: Playlists from any page

- **WHEN** a signed-in Profile on a desktop-width Viewer page activates "Playlists" in the sidebar
- **THEN** the Playlists page opens and its sidebar entry carries `aria-current="page"`

### Requirement: Profiles pin, unpin and reorder the Your stuff group

The sidebar editor SHALL let the Profile unpin and reorder the "Your stuff" entries, by keyboard
as well as by drag. Unpinned entries SHALL leave the sidebar. The choice SHALL be stored in
`web.sidebar-libraries` under an optional `personal` member; values stored without it SHALL still
parse, and a choice that leaves the group untouched SHALL serialize without it.

#### Scenario: Profile hides and reorders the group

- **WHEN** a Profile moves "History" first, unpins "Favorites" and presses Done
- **THEN** the group lists "History" then "Playlists", and Favorites stays in the account menu

#### Scenario: Preference stored before the group existed

- **WHEN** the stored `web.sidebar-libraries` value holds only `order` and `hidden`
- **THEN** the library choice applies unchanged and the group shows all three entries in default order

### Requirement: Phone width offers a bottom tab bar

At the Viewer's phone width the shell SHALL show a bottom navigation landmark with Home, Search,
Libraries and Your stuff. Home and Search SHALL be links that carry `aria-current="page"` on their
own page; Libraries and Your stuff SHALL open the navigation drawer with focus in that group. The
bar SHALL NOT cover page content and SHALL respect safe-area insets. Wider layouts SHALL NOT show it.

#### Scenario: One tap at 375 px

- **WHEN** a Profile on a 375 px wide Viewer page taps Home, Search or Libraries
- **THEN** Home or Search opens directly, or the drawer opens with focus on the first library

### Requirement: Old doubled Library node addresses still resolve

The web viewer SHALL resolve an address that names a Library node by its legacy doubled ID
(`/n/library-library-films`) to the node now named `library-films`, and SHALL read a Library pivot
remembered under the legacy node ID when none is remembered under the current one. An exact match
on a live node's ID SHALL win over the legacy collapse.

#### Scenario: An old link opens the Library

- **GIVEN** the manifest has the node `library-films`
- **WHEN** the viewer opens `/n/library-library-films`
- **THEN** it shows the `library-films` node

#### Scenario: A pivot remembered before the change is kept

- **GIVEN** the pivot `/all` was remembered for `library-library-films`
- **AND** nothing is remembered for `library-films`
- **WHEN** the viewer reads the remembered pivot for `library-films`
- **THEN** it is `/all`
