## ADDED Requirements

### Requirement: File workflows honor their persisted capability setting

The server SHALL check the persisted File conversion and Rename files settings
before admitting a conversion or applying a single or batch rename. Disabling
a workflow SHALL take effect on subsequent requests without a restart.

#### Scenario: Disabled workflow refuses a new operation

- **WHEN** a manager submits a conversion or rename request after the corresponding setting is disabled
- **THEN** the server returns a GraphQL `CONFLICT` with reason `CAPABILITY_DISABLED` before adding a job or mutating files

#### Scenario: Enabled workflow accepts new requests

- **WHEN** a manager reenables a workflow and submits a valid request
- **THEN** the server admits the request through the conversion queue or managed rename operation

### Requirement: Manage shows available workflow pages

Manage SHALL use the workflow states from the capability query to decide if
Convert and Rename are available. Authorized manager sessions SHALL be able to
read those workflow states. Changing a setting SHALL require an Admin Session.

#### Scenario: Manage opens with a disabled workflow

- **WHEN** a manager opens Manage with a workflow disabled
- **THEN** its page is absent from navigation, including in Advanced view
- **AND** a direct visit to the disabled page leads to Libraries
