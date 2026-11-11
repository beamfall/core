## ADDED Requirements

### Requirement: Token-byte cross-origin list holds only token-authorized routes

The server SHALL send `Access-Control-Allow-Origin: *` to a foreign origin on a private-UI byte GET
only when that route is authorized by a signed token in its path. Session-authenticated byte routes,
including media and entity artwork, SHALL apply the same-origin policy.

#### Scenario: Foreign origin requests artwork

- **WHEN** a GET to `/api/media/artwork/` or `/api/entity/artwork/` carries an `Origin` that is not
  the server's own host
- **THEN** the server answers `403` without an `Access-Control-Allow-Origin` header

#### Scenario: Originless artwork request

- **WHEN** a GET to an artwork route carries no `Origin` header
- **THEN** the access policy admits it and the handler authenticates the session as before

### Requirement: Offline-pack content is bound to the pack root

The server SHALL open an offline-pack member's ciphertext beneath the configured pack root, only
when the stored path names a file directly inside that pack's directory, the final component is a
regular file, and the opened descriptor is the same file that was checked. Any failure SHALL answer
`404` without serving bytes.

#### Scenario: Member replaced by a symlink

- **WHEN** a pack member's ciphertext file is replaced by a symlink to a file outside the pack root
- **THEN** the content route answers `404` and serves none of the target's bytes

#### Scenario: Pack directory replaced by a symlink

- **WHEN** a pack's directory is replaced by a symlink to a directory outside the pack root
- **THEN** the content route answers `404` and serves none of that directory's bytes

### Requirement: Partial scan enqueue reports queued jobs

When `POST /v1/scans` fails to enqueue a root after enqueueing earlier roots, the server SHALL
answer `500` with a body carrying both the `error` message and `jobIDs`, the IDs of the jobs already
queued.

#### Scenario: Second root fails

- **WHEN** a Library has two roots and the second enqueue fails
- **THEN** the reply is `500` with `jobIDs` holding the first root's job ID

#### Scenario: First root fails

- **WHEN** the first enqueue fails
- **THEN** the reply is `500` with an empty `jobIDs` array
