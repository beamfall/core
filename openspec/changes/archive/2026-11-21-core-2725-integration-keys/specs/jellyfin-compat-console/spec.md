## ADDED Requirements

### Requirement: Integration keys are durable, Profile-bound and revocable

The server SHALL let an admin mint a named integration key for an adult, enabled Profile whose
integration sign-in is on, and SHALL refuse any other Profile. A key SHALL have no expiry. It SHALL
authenticate compatibility requests only while it is not revoked, its Profile is adult and enabled,
and its Profile's integration sign-in is on. Revoking a key SHALL make the next request with it get
the uniform 401. Turning a Profile's integration sign-in off SHALL revoke that Profile's keys.

#### Scenario: A key outlives the integration session lifetime

- **WHEN** a key is minted and the server clock moves forward more than 24 hours
- **THEN** compatibility requests with the key still authenticate

#### Scenario: Operator revokes a key

- **WHEN** an admin revokes a key that a tool is using
- **THEN** the tool's next compatibility request gets the uniform 401

### Requirement: Integration keys are scope-capped and stored as digests

A key SHALL carry only the `compat.read` and `compat.refresh` scopes, and the store SHALL refuse a
key with the `compat.setup` scope. The server SHALL store only a digest of the key and SHALL return
the plaintext once, from the mint. The key list SHALL show each key's name, Profile, creation time
and last use, and SHALL NOT show any token material.

#### Scenario: A key asks for the setup scope

- **WHEN** the store is asked to create a key carrying `compat.setup`
- **THEN** creation fails and no key is stored

#### Scenario: Operator lists keys

- **WHEN** an admin lists integration keys after a tool has used one
- **THEN** the list shows that key's last use and no token material

### Requirement: Integration keys are minted only from the Console

The server SHALL mint an integration key only through the admin Console mutation. No compatibility
route SHALL create a key, and the admin password SHALL NOT mint one: a setup session that calls
`Auth/Keys` SHALL leave the key table unchanged. A key SHALL NOT authenticate any native Beamfall
API, and a compatibility request that arrives through the relay SHALL be refused whatever
credential it carries.

#### Scenario: A setup session posts Auth/Keys

- **WHEN** the admin setup session posts `Auth/Keys?App=Seerr`
- **THEN** no integration key exists afterwards

#### Scenario: A key on a native route

- **WHEN** a request to the native GraphQL API presents an integration key as its bearer
- **THEN** the request is not authenticated

### Requirement: A key mint is the only field of its operation

The server SHALL refuse a mutation operation that carries `createJellyfinCompatKey` with any other
field, before minting anything, so a failure in another field can never discard a token that is
shown only once.

#### Scenario: A mint batched with another field

- **WHEN** one mutation operation carries `createJellyfinCompatKey` and another field
- **THEN** the operation is refused and no key exists afterwards

## MODIFIED Requirements

### Requirement: App Store binaries carry no compatibility surface

An App Store build SHALL NOT advertise or dispatch the compatibility settings or integration key
graph fields, SHALL serve 404 for every route under the compatibility prefix whatever the stored
settings say, and its Console host SHALL NOT show the compatibility page. Every compatibility
settings surface SHALL show a statement that Beamfall is not affiliated with or endorsed by the
Jellyfin project.

#### Scenario: App Store build with compatibility stored as on

- **WHEN** an App Store build starts with compatibility mode stored as on
- **THEN** every route under the compatibility prefix returns 404 and the settings and key fields are unknown to the graph
