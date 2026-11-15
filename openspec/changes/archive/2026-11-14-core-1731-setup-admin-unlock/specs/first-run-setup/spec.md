## ADDED Requirements

### Requirement: A resumed wizard can reopen a lapsed admin window

When a resumed setup wizard cannot read the saved plugin choices because no admin session is
active, the Plugins step SHALL ask for the admin password, reopen the admin session with it, and
then load the saved choices and continue.

#### Scenario: Correct admin password

- **WHEN** the saved choices are refused for want of an admin session and the owner enters the
  admin password
- **THEN** the admin session reopens, the saved choices load, and Continue saves them

#### Scenario: Wrong admin password

- **WHEN** the owner enters a password the server does not accept
- **THEN** the step says the admin password was not accepted, clears the field, and keeps the
  saved choices locked
