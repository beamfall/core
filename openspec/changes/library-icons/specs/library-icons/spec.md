## ADDED Requirements

### Requirement: A fixed, domain-agnostic icon vocabulary

Core SHALL define Library icons as one closed list of lowercase names that describe a shape or a
medium, never a plugin or a domain. The list SHALL be `film`, `tv`, `video`, `note`, `mic`,
`broadcast`, `headphones`, `book`, `image`, `camera`, `people`, `star`, `heart`, `sparkles`,
`layers` and `folder`. Core source SHALL keep passing the vocabulary firewall.

#### Scenario: A name outside the list is not an icon

- **WHEN** core is asked whether `spaceship` is a Library icon
- **THEN** it answers no

### Requirement: The manifest carries each Library's icon

Every `LibrarySummary` in the Navigation Manifest SHALL carry `icon`: the owner's choice when one is
stored, otherwise the default for the Library. The default SHALL be `film` when the bound plugin
declares only the video media class, `note` for only audio, `image` for only image, `layers` when
it declares more than one class, and `folder` when the Library has no plugin or its plugin is not
registered. The field SHALL be optional on the wire so that payloads without it stay valid.

#### Scenario: Default from one media class

- **WHEN** a Library is bound to a plugin that declares only the audio media class and the owner
  has chosen no icon
- **THEN** its `LibrarySummary.icon` is `note`

#### Scenario: Default for a mixed plugin

- **WHEN** a Library is bound to a plugin that declares the video and audio media classes
- **THEN** its `LibrarySummary.icon` is `layers`

#### Scenario: Library with no plugin

- **WHEN** a Library has no plugin
- **THEN** its `LibrarySummary.icon` is `folder`

#### Scenario: The owner's choice wins

- **WHEN** the owner has chosen `headphones` for a Library whose default is `note`
- **THEN** its `LibrarySummary.icon` is `headphones`

### Requirement: An admin sets or clears a Library's icon

The GraphQL mutation `setConsoleConfig`, given `libraryID` and `libraryIcon`, SHALL require an
Admin Session. Given a name in the vocabulary it SHALL store that name as the Library's choice;
given an empty `libraryIcon` it SHALL clear the choice. It SHALL reject any other name, an unknown
Library, and a call that also carries other console settings, and change nothing. Each successful
call SHALL write a `library.icon` activity record for the Library.

#### Scenario: Choosing an icon

- **WHEN** an admin calls `setConsoleConfig` with `libraryIcon: "tv"` for a Library
- **THEN** the Library's stored choice is `tv` and the activity log has a `library.icon` record
  for that Library

#### Scenario: Unknown name

- **WHEN** an admin calls `setConsoleConfig` with `libraryIcon: "spaceship"` for a Library
- **THEN** the server returns an error and the Library's icon is unchanged

#### Scenario: Not an admin

- **WHEN** a session without an open Admin Session sends the same call
- **THEN** the server answers "admin session required" and the Library's icon is unchanged

### Requirement: The management API reports the choice and the default

The `libraries` query SHALL return, for each Library, `icon` (the effective icon), `iconChoice`
(the stored choice, empty when the Library follows its default) and `defaultIcon`.

#### Scenario: A chosen icon

- **WHEN** an admin has chosen `tv` for a Library whose default is `film`
- **THEN** the Library reports `icon: "tv"`, `iconChoice: "tv"` and `defaultIcon: "film"`

#### Scenario: Returning to the default

- **WHEN** an admin then sends an empty `libraryIcon` for that Library
- **THEN** the Library reports `icon: "film"` and an empty `iconChoice`

### Requirement: The web sidebar draws the icon

The web sidebar SHALL draw each Library's `icon` as a symbol inside the Library's accent tile. When
the manifest gives no icon, or a name this client does not know, the tile SHALL show the
collision-aware monogram instead. The symbol SHALL be decorative: the link's accessible name stays
the Library's title.

#### Scenario: Known icon

- **WHEN** the manifest lists a Library "Films" with `icon: "film"`
- **THEN** its sidebar tile shows the film symbol and no letter, and the link is named "Films"

#### Scenario: Unknown icon

- **WHEN** the manifest lists a Library "Films" with `icon: "spaceship"`
- **THEN** its sidebar tile shows the monogram "F"

### Requirement: The Console offers an accessible icon picker

Each Library card in Console > Libraries SHALL offer an icon picker exposed as a radio group named
for the Library, with a "Default" option and one named option per vocabulary icon. The stored
choice (or "Default") SHALL be the checked option. Arrow keys SHALL move the selection within the
group, and only the checked option SHALL be in the tab order.

#### Scenario: Current selection is shown

- **WHEN** an admin opens Console > Libraries and "Films" has the stored choice `tv`
- **THEN** the "Films" icon group has the "TV" option checked

#### Scenario: Keyboard

- **WHEN** focus is on the checked option and the admin presses the right arrow key
- **THEN** the next option is checked and focused

### Requirement: The picker saves on selection and recovers from failure

Selecting an option SHALL send the named GraphQL operation `SetLibraryIcon` and show the new
selection at once. Selections made in quick succession SHALL settle into one write of the last
one. After a successful write the web client SHALL re-read the manifest without ending the
session, so the sidebar draws the new icon without a reload. When the server refuses or the
request fails, the picker SHALL restore the previous selection and show the error.

#### Scenario: Choosing an icon

- **WHEN** an admin selects "Headphones" in the "Audiobooks" icon group
- **THEN** `SetLibraryIcon` is sent with `libraryIcon: "headphones"` and "Headphones" is checked
  without waiting for the reply

#### Scenario: Walking the group with the arrow keys

- **WHEN** an admin presses the right arrow key three times in quick succession
- **THEN** one `SetLibraryIcon` is sent, carrying the option the selection came to rest on

#### Scenario: Sidebar follows the choice

- **WHEN** the write for "Audiobooks" succeeds
- **THEN** the manifest is read again and the sidebar tile for "Audiobooks" draws the new icon,
  with playback state and the signed-in identity untouched

#### Scenario: Save fails

- **WHEN** the server answers the mutation with an error
- **THEN** the previously checked option is checked again and the error is shown
