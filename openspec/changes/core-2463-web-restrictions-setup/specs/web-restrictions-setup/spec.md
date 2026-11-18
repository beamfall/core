## ADDED Requirements

### Requirement: Manage Restrictions explains setup when no PIN is set

When the session reports `revealPinSet: false`, Manage › Restrictions SHALL explain the two setup
steps (set a PIN, then mark Libraries restricted in Console › Libraries) and SHALL offer a Set PIN
form instead of an unlock field. When the session reports `revealPinSet: true`, or does not report
it, the page SHALL keep the unlock field.

#### Scenario: Fresh server

- **WHEN** a manager opens Manage › Restrictions and no Restrictions PIN is set
- **THEN** the page shows the setup steps and a Set PIN form, and no unlock field

#### Scenario: Older server without the flag

- **WHEN** the session does not report `revealPinSet`
- **THEN** the page shows the unlock field

### Requirement: A manager can set and change the Restrictions PIN

The Set PIN form SHALL take the new PIN twice, SHALL refuse two different values without calling
the server, SHALL clear both fields once read, and SHALL call `setRevealPin`. On success it SHALL
say the PIN was saved and reload the session. Once the session is unlocked the page SHALL offer
Change PIN with the same form and a link to Console › Libraries.

#### Scenario: Mismatched confirmation

- **WHEN** the two PIN fields differ and the manager submits
- **THEN** the page says the PINs don't match and `setRevealPin` is not called

#### Scenario: PIN saved

- **WHEN** the manager submits matching PINs and the server accepts them
- **THEN** the fields are empty, the page says "PIN saved." and the session is reloaded

#### Scenario: Profile with no reveal path

- **WHEN** the server refuses because the profile has no reveal path
- **THEN** the page says this profile can't have a Restrictions PIN

### Requirement: An admin can mark a Library restricted from Console Libraries

Console › Libraries SHALL show a Restricted switch for each Library reflecting its `restricted`
value. The switch SHALL be disabled, with help pointing to Manage › Restrictions, while the session
is not unlocked. Changing it SHALL call `setLibraryRestricted`, then refresh the Navigation Manifest
and reload the Library list; a refusal SHALL show fixed error copy.

#### Scenario: Locked session

- **WHEN** an admin opens Console › Libraries without unlocking
- **THEN** each Restricted switch is disabled and its help says to unlock first

#### Scenario: Restrict a Library

- **WHEN** an unlocked admin turns Restricted on for a Library
- **THEN** `setLibraryRestricted` is called with `true`, the manifest is refreshed and the Library
  then disappears for an unrevealed session until it is unlocked

### Requirement: Settings search finds both controls

`settingsIndex` SHALL list `manage.restrictions.pin` on Manage › Restrictions and
`console.libraries.restricted` on Console › Libraries.

#### Scenario: Search for the PIN

- **WHEN** a manager searches settings for the Restrictions PIN
- **THEN** the result opens Manage › Restrictions at the PIN control
