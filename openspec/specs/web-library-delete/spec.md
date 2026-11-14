# web-library-delete Specification

## Purpose
Console and Manage library cards offer an audited, confirmed delete that routes through the server mutation and reports busy and error states.

## Requirements

### Requirement: Library cards offer an audited delete

Each library card in Console > Libraries and Manage > Libraries SHALL offer a danger-styled
"Delete library" control, separate from Rescan and Fix unmatched, rendered by one shared component.
Confirming the delete SHALL call the `DeleteLibrary` operation (`deleteLibrary(libraryID)`), the
server's audited soft delete.

#### Scenario: Owner deletes a library

- **WHEN** an owner with an open Admin Session confirms the delete in Manage > Libraries
- **THEN** the client sends `DeleteLibrary` with the library's id, the dialog closes, the library
  list reloads without that library and focus moves to the heading of the libraries section

#### Scenario: Owner deletes a library in Console

- **WHEN** an owner with an open Admin Session confirms the delete in Console > Libraries
- **THEN** the client sends `DeleteLibrary` with the library's id, the dialog closes, the library
  list reloads without that library and focus moves to the deletion notice, which offers Undo

#### Scenario: Cancel keeps the library

- **WHEN** the owner presses Cancel or Escape in the dialog
- **THEN** no mutation is sent, the dialog closes and focus returns to the "Delete library" control

### Requirement: The delete dialog states what deletion does

The "Delete library" control SHALL open an alert dialog titled with the library name. Its copy SHALL
state that the library disappears on every device, that files on disk are not touched, that watch
history stays on the server with past plays listed in Play History without their titles, and that
the library can be restored from "Recently deleted" in Console > Libraries for 30 days. It SHALL
NOT state that a deleted library cannot be restored.

#### Scenario: Owner opens the dialog

- **WHEN** the owner activates "Delete library" on a library named "Films"
- **THEN** an alert dialog titled with "Films" opens, focus moves into it, and its description
  covers devices, files on disk, watch history and the restore path

#### Scenario: Dialog names the restore path

- **WHEN** the owner activates "Delete library" on a library named "Films"
- **THEN** the dialog description says the library can be restored from "Recently deleted" for
  30 days

### Requirement: Delete failures stay recoverable in the dialog

While the delete runs the dialog's buttons SHALL be disabled. If the mutation fails the dialog SHALL
stay open, show the error and allow the owner to retry.

#### Scenario: Server refuses the delete

- **WHEN** the `DeleteLibrary` mutation returns an error
- **THEN** the dialog stays open showing the error message, the buttons are enabled again and the
  library list is not reloaded
