## ADDED Requirements

### Requirement: Compatibility sign-in describes the signed-in user

A successful `Users/AuthenticateByName` response SHALL carry the integration token, its expiry, the
server ID, and a `User` object with a user ID, a display name, the server ID, the configured server
name and `Policy.IsAdministrator`. For a Profile sign-in the user ID and name SHALL be the Profile's.
`IsAdministrator` SHALL be true only for the admin setup session. The response SHALL NOT include any
other Profile, Library or media data.

#### Scenario: Admin setup sign-in

- **WHEN** a caller signs in with the admin name and the admin password
- **THEN** the response reports `IsAdministrator` true, the server ID shown by `System/Info/Public`,
  and the setup account ID, not the owner Profile's ID

#### Scenario: Opted-in Profile sign-in

- **WHEN** a caller signs in with an opted-in adult Profile and its entry PIN
- **THEN** the response reports `IsAdministrator` false and the Profile's own ID and display name

### Requirement: The admin setup sign-in reports a setup account

The `User` object of the admin setup sign-in SHALL describe a setup account, not the owner Profile:
the name `admin` and a user ID derived from the server ID that is stable across sign-ins and differs
from every Profile ID.

#### Scenario: The owner signs in with a PIN after setup

- **WHEN** the owner Profile signs in with its entry PIN after an admin setup sign-in
- **THEN** the two responses report different user IDs, and a second admin sign-in reports the first one's ID

### Requirement: Only the admin setup session carries the setup scope

The server SHALL grant the `compat.setup` integration scope only to a session created by the admin
setup sign-in, and SHALL refuse a sign-in whose caller requests it. The store SHALL refuse to create
a session with that scope unless it belongs to the owner Profile, is created under the admin
sign-in's throttle reservation, and expires no later than 15 minutes after that reservation.

#### Scenario: A Profile session asks for the setup scope

- **WHEN** an integration session is created for an opted-in Profile with the setup scope requested
- **THEN** creation fails and no session is stored

### Requirement: Compatibility API key routes never mint a credential

The server SHALL expose POST `Auth/Keys` and GET `Auth/Keys` only to an integration session granting
setup access, and SHALL return the uniform 401 to every other credential. POST SHALL return 204 and
create nothing. GET SHALL list exactly one key: the caller's own token, named after the `Client`
parameter of the caller's MediaBrowser authorization header. The listed token SHALL stop
authenticating when the setup session expires.

#### Scenario: Seerr asks for an API key during setup

- **WHEN** a setup session posts `Auth/Keys?App=Seerr` and then lists `Auth/Keys` with client `Seerr`
- **THEN** the list holds one key named `Seerr` whose token is the setup session's own token

#### Scenario: A Profile session lists API keys

- **WHEN** an opted-in Profile's integration session lists `Auth/Keys`
- **THEN** the server returns the uniform 401

#### Scenario: The listed key after the setup window

- **WHEN** the setup session's 15 minute lifetime has passed
- **THEN** the token listed by `Auth/Keys` gets the uniform 401 on every compatibility route
