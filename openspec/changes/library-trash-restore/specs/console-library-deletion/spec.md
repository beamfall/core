## MODIFIED Requirements

### Requirement: Confirmation states the consequences

The confirmation SHALL be an alert dialog titled with the library's title. It SHALL state that the
library and everything in it disappear from Beamfall on every device, that the media files on disk
are not deleted or changed, that watch history and progress are kept on the server but hidden with
the library, and that the library can be restored from "Recently deleted" in Console > Libraries
until Beamfall removes it for good after 30 days. It SHALL NOT state that a deleted library cannot
be restored.

#### Scenario: Dialog names the library and what is kept

- **WHEN** an admin chooses "Delete library" on "Films"
- **THEN** the dialog title names "Films" and the body says the files on disk stay untouched

#### Scenario: Dialog names the restore path

- **WHEN** an admin chooses "Delete library" on "Films"
- **THEN** the dialog body says the library can be restored from "Recently deleted" for 30 days
