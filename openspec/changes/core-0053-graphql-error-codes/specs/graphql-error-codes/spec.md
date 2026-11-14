## ADDED Requirements

### Requirement: Every GraphQL error carries a code from a closed vocabulary

Every entry in a GraphQL response's `errors` array SHALL carry `extensions.code`, and the code
SHALL be one of `NOT_FOUND`, `FORBIDDEN`, `UNAUTHENTICATED`, `THROTTLED`, `INVALID`, `CONFLICT`
or `INTERNAL`. The message and path SHALL keep their existing meaning.

#### Scenario: Missing session

- **WHEN** a request that needs a session carries none or an invalid one
- **THEN** the error's `extensions.code` is `UNAUTHENTICATED`

#### Scenario: Role gate refuses

- **WHEN** a non-manager device calls a manager-only field
- **THEN** the error's `extensions.code` is `FORBIDDEN`

#### Scenario: Bad argument

- **WHEN** a client omits a required argument
- **THEN** the error's `extensions.code` is `INVALID`

#### Scenario: Internal failure

- **WHEN** a resolver fails for a reason the client did not cause
- **THEN** the message is the generic `<field> unavailable` and `extensions.code` is `INTERNAL`

### Requirement: Error codes never reveal existence

An error about content the caller cannot see SHALL use `NOT_FOUND`, exactly as for content that
does not exist. `FORBIDDEN` SHALL be used only for role, tier, session-kind and Reveal gates.

#### Scenario: Content outside the caller's scope

- **WHEN** a caller names a Media item in a Library its scope cannot see
- **THEN** the error's `extensions.code` is `NOT_FOUND`, the same as for an unknown id

### Requirement: Unmapped error sites fail conformance

A conformance test SHALL enumerate every place `internal/graph` builds a GraphQL error or a
client error, and SHALL fail when an error is built without a code or a client error message
has no explicit mapping.

#### Scenario: New client error without a mapping

- **WHEN** a resolver adds a client error whose message matches no mapping rule
- **THEN** the conformance test fails and names the message and its source position

### Requirement: The wire contract publishes the error shape

The client-core-wire contract SHALL publish the GraphQL error shape and the code vocabulary, so
a consumer pinning a version gets both.

#### Scenario: Consumer reads the contract

- **WHEN** a consumer reads the published wire contract
- **THEN** it finds the error object shape with `extensions.code` and the closed list of codes
