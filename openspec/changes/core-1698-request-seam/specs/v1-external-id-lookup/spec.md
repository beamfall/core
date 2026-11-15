## ADDED Requirements

### Requirement: Media lookup by external identifier

`GET /v1/media` with the query parameters `scheme` and `value` SHALL return, under the
`read-catalog` scope, the Media visible to the caller that carry that external identifier, in a
`media` array of at most 20 items with the fields of `GET /v1/media/{mediaID}`. Media the caller
cannot see SHALL be omitted exactly as Media that do not exist.

#### Scenario: Visible match

- **WHEN** a key with `read-catalog` looks up a scheme and value that a visible Media carries
- **THEN** the response lists that Media with its external identifiers

#### Scenario: Hidden match

- **WHEN** the only Media carrying the identifier is outside the key's Library or Reveal scope
- **THEN** the response is `{"media":[]}` with status 200

#### Scenario: Missing parameter

- **WHEN** a client omits `scheme` or `value`
- **THEN** the response is 400

#### Scenario: No match

- **WHEN** no Media carries the identifier
- **THEN** the response is `{"media":[]}` with status 200

### Requirement: Media detail carries external identifiers

`GET /v1/media/{mediaID}` SHALL carry an `externalIDs` array of `{scheme, value}` pairs, sorted by
scheme then value and capped at 32, when the Media has any external identifiers, and SHALL omit the
member when it has none.

#### Scenario: Detail read

- **WHEN** a client reads a Media that carries two external identifiers
- **THEN** the response's `externalIDs` lists both, sorted by scheme

### Requirement: New external identifiers announce a Media update

Attaching an external identifier that a Media did not already carry SHALL append one
`media.updated` event for that Media, delivered on `/v1/events` and to webhook endpoints that
subscribe to it. Re-attaching an identifier the Media already carries SHALL append no event.

#### Scenario: Late identifier

- **WHEN** hydration attaches a new identifier to existing Media
- **THEN** subscribers receive exactly one `media.updated` for that Media

#### Scenario: Repeat attach

- **WHEN** the same identifier is attached to the same Media again
- **THEN** no further event is appended
