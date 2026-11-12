## ADDED Requirements

### Requirement: Delete a library from its card

Every library card in Console > Libraries and Manage > Libraries SHALL offer a "Delete library"
control whose accessible name includes the library's title. The control SHALL be styled as a danger
action and SHALL NOT delete anything until the admin confirms in a dialog.

#### Scenario: Control is present and named

- **WHEN** an admin opens Console > Libraries with a library titled "Films"
- **THEN** the "Films" card has a control named "Delete library Films"

### Requirement: Confirmation states the consequences

The confirmation SHALL be an alert dialog titled with the library's title. It SHALL state that the
library and everything in it disappear from Beamfall on every device, that the media files on disk
are not deleted or changed, that watch history and progress are kept on the server but hidden with
the library, and that Beamfall cannot restore a deleted library.

#### Scenario: Dialog names the library and what is kept

- **WHEN** an admin chooses "Delete library" on "Films"
- **THEN** the dialog title names "Films" and the body says the files on disk stay untouched

### Requirement: Cancelling changes nothing

Focus SHALL move into the dialog when it opens. Escape and Cancel SHALL close it without calling
the server and SHALL return focus to the control that opened it.

#### Scenario: Admin cancels

- **WHEN** an admin opens the dialog for "Films" and presses Escape
- **THEN** the dialog closes, no mutation is sent and "Films" is still listed

### Requirement: Confirmed delete runs once and reports its result

Confirming SHALL send the named GraphQL operation `DeleteLibrary` for that library and SHALL
disable the dialog's buttons while it runs. When the server succeeds, the dialog SHALL close and
the library list SHALL reload without a manual refresh. When the server refuses or the request
fails, the dialog SHALL stay open, show the error, and let the admin retry or cancel.

#### Scenario: Delete succeeds

- **WHEN** an admin confirms deleting "Films" and the server returns true
- **THEN** the dialog closes and the reloaded list no longer shows "Films"

#### Scenario: Delete fails

- **WHEN** an admin confirms deleting "Films" and the server returns an error
- **THEN** the dialog stays open and shows the error, and "Films" is still listed
