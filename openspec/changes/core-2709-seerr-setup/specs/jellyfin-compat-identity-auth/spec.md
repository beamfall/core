## ADDED Requirements

### Requirement: Compatibility sign-in describes the signed-in user

A successful `Users/AuthenticateByName` response SHALL carry the integration token, its expiry, the
server ID, and a `User` object with the Profile ID, the Profile display name, the server ID, the
configured server name and `Policy.IsAdministrator`. `IsAdministrator` SHALL be true only for the
admin setup session. The response SHALL NOT include any other Profile, Library or media data.

#### Scenario: Admin setup sign-in

- **WHEN** a caller signs in with the admin name and the admin password
- **THEN** the response reports `IsAdministrator` true and the server ID shown by `System/Info/Public`

#### Scenario: Opted-in Profile sign-in

- **WHEN** a caller signs in with an opted-in adult Profile and its entry PIN
- **THEN** the response reports `IsAdministrator` false and the Profile's display name

### Requirement: Only the admin setup session carries the setup scope

The server SHALL grant the `compat.setup` integration scope only to a session created by the admin
setup sign-in, and SHALL refuse to create any other integration session that requests it.

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
