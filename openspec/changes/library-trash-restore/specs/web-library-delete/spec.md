## MODIFIED Requirements

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
