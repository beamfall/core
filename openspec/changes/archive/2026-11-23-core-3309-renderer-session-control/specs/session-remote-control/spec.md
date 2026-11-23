## ADDED Requirements

### Requirement: Monotonic Renderer Session revision and 24-hour command ledger

The server SHALL track a monotonic, never-resetting revision per Renderer Session (keyed by live device `SessionID`, defaulting to `0`) and a command ledger keyed by `commandID` storing `sessionID`, canonical body SHA-256 (`command` plus normalized args JSON), applied result, resulting revision, and creation time. Ledger writes SHALL prune rows older than 24 hours.

#### Scenario: Missing revision row defaults to zero

- **WHEN** a live device session has never been controlled
- **THEN** its revision is `0` and `profileSessions` reports `revision: 0`

#### Scenario: Legacy controlSession advances the revision

- **WHEN** `controlSession` publishes a command for a target session
- **THEN** that session's revision increments by `1` in the same transaction as the outbox event

### Requirement: Serialized idempotent store execution

In one serialized writer transaction, `Store.ControlRendererSession` SHALL prune rows older than 24 hours, look up `commandID`, check `expectedRevision`, and on match append the `session.control` event, increment the revision by `1`, and record the `commandID` row.

#### Scenario: Idempotent replay after revision moved

- **WHEN** a caller replays an applied `commandID` with the same `sessionID`, `command`, and arguments
- **THEN** the store returns the original result with no second outbox event and no second revision bump

#### Scenario: Reusing commandID with a different body fails

- **WHEN** a caller reuses an existing `commandID` with a different command, arguments, or target session
- **THEN** the store returns a typed reuse error and performs no mutation

#### Scenario: Stale expectedRevision returns a typed conflict

- **WHEN** `expectedRevision` does not equal the session's current revision
- **THEN** the store returns a typed conflict with the current revision without writing an event or `commandID`

### Requirement: GraphQL controlRendererSession mutation and conflict snapshot

`controlRendererSession` SHALL enforce the same authorization gates and non-leaking refusals as `controlSession`, require `commandID` (`[A-Za-z0-9_-]{16,128}`) and non-negative integer `expectedRevision`, and return `{status, sessionID, command, revision, snapshot}`. `controlSession` SHALL remain strictly same-Profile per ADR-0240 D3.

#### Scenario: Cross-Profile and unrevealed callers receive non-leaking refusals

- **WHEN** a caller targets another Profile's session or an unrevealed session playing restricted Media
- **THEN** `controlRendererSession` refuses with `"unknown session"` or `"session not controllable"` without leaking revision or ledger state

#### Scenario: Conflict returns reveal-filtered snapshot of target session only

- **WHEN** an authorized caller sends a stale `expectedRevision`
- **THEN** `controlRendererSession` returns `status: "CONFLICT"`, the current `revision`, and a `snapshot` containing only that session's `GrantedSessions` rows with hidden Media identity suppressed
