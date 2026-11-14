## ADDED Requirements

### Requirement: Folder browser shows the subfolders of the current folder

The web path picker SHALL list the subfolders of the folder being browsed, never the folder itself,
and SHALL open a subfolder when it is activated. With no folder chosen it SHALL list the server's
browsable locations.

#### Scenario: Browse a mount point

- **WHEN** an admin browses `/mnt` and the server reports the child folder `/mnt/user`
- **THEN** the list shows `user` and does not show `mnt`

#### Scenario: Open a subfolder

- **WHEN** the admin activates `user`
- **THEN** the picker shows the subfolders of `/mnt/user`

### Requirement: The picker shows where you are and lets you go up

The picker SHALL show the current location as a breadcrumb whose earlier segments are links, and
SHALL offer an Up control that opens the parent folder, or the list of locations from a top-level
location.

#### Scenario: Go up from a subfolder

- **WHEN** the picker shows `/mnt/user/tv` and the admin activates Up
- **THEN** the picker shows `/mnt/user`

### Requirement: One action commits the current folder

The picker SHALL offer a single action that commits the folder being shown, disabled with the
server's reason when that folder cannot be used. In Console > Libraries that action SHALL add the
folder to the library without a second step.

#### Scenario: Add a folder to a library from the browser

- **WHEN** an admin browses to `/mnt/user/tv` in a library's Folders section and activates
  "Add this folder"
- **THEN** `/mnt/user/tv` is saved as a folder of that library and the browser closes

### Requirement: Every picker state says what happened

The picker SHALL show a distinct message while loading, when a folder has no subfolders, when the
request fails (with a retry), when the server cannot read the folder, when the server cannot see the
path, and when the path is outside the browsable locations. The outside-locations message SHALL
offer to allow browsing there.

#### Scenario: Path outside the browsable locations

- **WHEN** an admin types `/data/films` and opens the browser, and the server answers
  `outside-roots`
- **THEN** the picker explains that the server does not browse there yet and offers
  "Allow browsing here"

### Requirement: The picker is operable by keyboard and screen reader and fits a phone

The folder list SHALL be reachable and operable by keyboard alone (arrow keys move, Enter opens,
Backspace goes up, Escape closes), SHALL expose a labelled list and announce location changes, and
SHALL fit a 375 px wide viewport without horizontal page scroll. Opening it SHALL NOT move
neighbouring controls out of their row.

#### Scenario: Keyboard descent

- **WHEN** focus is on a folder row and the admin presses Down then Enter
- **THEN** the next folder opens and focus moves to its first subfolder
