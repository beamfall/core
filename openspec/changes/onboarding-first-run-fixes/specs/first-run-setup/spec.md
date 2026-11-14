## ADDED Requirements

### Requirement: A new admin password has a minimum length

The server SHALL refuse to set an admin password shorter than 8 characters (counted as Unicode code
points) on every path that sets one, with a message stating the minimum. Verification SHALL NOT apply
the minimum, so an existing shorter password keeps working until it is next changed.

#### Scenario: Short password at setup

- **WHEN** first-run setup submits a 7-character admin password
- **THEN** it is refused with `400` and a message that the password must be at least 8 characters

#### Scenario: Existing short password

- **WHEN** an owner whose stored password is shorter than 8 characters signs in with it
- **THEN** the sign-in succeeds

### Requirement: The setup wizard shows a refused folder beside the folder field

When creating the first Library is refused, the wizard SHALL show the server's reason next to the
folder field, keep the field editable, and let the owner correct the path and continue without
creating a second Library.

#### Scenario: Mistyped folder

- **WHEN** the owner submits a folder that does not exist
- **THEN** the reason is shown beside the field and a corrected path creates the Library

### Requirement: The wizard is consistent across a reload

The wizard SHALL show the same number of steps whether the token step is shown, skipped on the LAN or
resumed after a reload, and a reload SHALL return to the step in progress with the plugin and Library
choices the server has saved.

#### Scenario: Reload after choosing plugins

- **WHEN** the owner switches a plugin off, continues, and reloads the page
- **THEN** the step count is unchanged and the plugin remains off

### Requirement: First-run startup output ends with one setup block

On first run the server SHALL print the setup address(es) as one block after the rest of startup
output, SHALL log a single summary line for migrations applied to a fresh data directory, and SHALL
NOT log an auth-refused warning for a first page load that presents no device credential.

#### Scenario: Fresh data directory

- **WHEN** the server starts on an empty data directory
- **THEN** one migrations summary line is logged and the setup block is the last startup output

#### Scenario: Anonymous page load

- **WHEN** a browser with no device credential loads the first page
- **THEN** no auth-refused warning is logged for the session renewal attempt
