# workflow-capabilities Specification

## Purpose
Keep the persisted File conversion and Rename files settings authoritative: the server refuses a
disabled workflow's mutations, and the web Console and Manage surfaces hide or disable its tools.

## Requirements

### Requirement: File workflows honor their persisted capability setting

The server SHALL check the persisted File conversion setting before retrying a
conversion with `jobRetry`. It SHALL check the persisted Rename files setting
before applying `renameManagedPath` or `batchRenameManagedPaths`. Disabling a
workflow SHALL take effect on subsequent requests without a restart. These
operations SHALL use the client-core-wire 2.37.0 schema and error vocabulary.

#### Scenario: A disabled workflow refuses a request

- **WHEN** an authorized caller retries a conversion or submits a single or batch rename while the corresponding workflow is disabled
- **THEN** the server returns the existing GraphQL `CONFLICT` code with a localized message before adding a job or mutating files
- **AND** Console Operations displays the localized message when a conversion retry is refused

#### Scenario: A reenabled workflow accepts requests

- **WHEN** an authorized caller submits a valid request after the workflow is reenabled
- **THEN** the existing retry or rename operation proceeds

### Requirement: Manage uses the existing admin capability read

Manage SHALL use `capabilityStates` to determine workflow visibility when the
current viewer can read it. The background read SHALL leave the current Admin
Session as it is. If the read is unavailable, Manage SHALL preserve the workflow
navigation for managers and present the server's refusal when a mutation fails.

#### Scenario: A manager can read a disabled workflow state

- **WHEN** the existing admin read reports a workflow is disabled
- **THEN** its page is absent from navigation and search, including in Advanced view
- **AND** a direct visit to the disabled page leads to Libraries

#### Scenario: A manager lacks an Admin Session

- **WHEN** the manager opens Manage without an Admin Session
- **THEN** Manage leaves the workflow pages and links available without prompting for an unlock to check their status
- **AND** a refused rename shows the localized server message in the page
