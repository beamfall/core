## Why

Ticket: CORE-3309 (`queue:core:main`), implementing the Renderer Session control mutation required by ADR-0151 decisions 1–3 and ADR-0240 D3.

Today, cross-device session control (`controlSession`) is same-Profile only and publishes transport commands directly to the event outbox without a monotonic revision check or an idempotency ledger. Controllers cannot detect concurrent state changes, cannot safely retry commands after transient network loss, and cannot read a session's current revision before issuing a command. ADR-0240 D3 requires a revision-checked, idempotent Renderer Session control surface as the prerequisite for any future cross-Profile player display control.

## What Changes

- **Events migration 0030 (`renderer_session_revision`, `renderer_session_command`)**:
  - Per-Renderer-Session monotonic, never-resetting revision keyed by the live device `SessionID`.
  - Command ledger keyed by `commandID` recording target `session_id`, `sha256` of the canonical command body (`command` + normalized canonical args JSON), the applied result, the resulting revision, and `created_at_ms`, pruning rows older than 24 hours on write.
- **Serialized store transaction (`Store.ControlRendererSession`)**:
  - In one `events.db` writer transaction: prunes expired (>24h) ledger rows; checks `commandID` (replay with same session and body hash returns the stored result without a second event or revision bump; reuse with a different target session or body hash returns a typed reuse error); checks `expectedRevision` against the current revision (missing row = `0`; mismatch returns a typed conflict with the current revision); on match, appends the `session.control` outbox event and activity entry, increments the session revision once, and records the `commandID` row.
- **GraphQL mutation `controlRendererSession(sessionID, expectedRevision, commandID, command, args...)`**:
  - Registered alongside `controlSession` with `roleSession`, applying the same authorization gates (same Profile, reveal, active playback for transport verbs, live device session and caller-visible Media for `play-media`) and the same non-leaking refusal messages.
  - Validates `commandID` (required, 16–128 characters of `[A-Za-z0-9_-]`) and `expectedRevision` (required non-negative integer).
  - Returns `{status: APPLIED|CONFLICT, sessionID, command, revision, snapshot}`. On `CONFLICT`, `snapshot` contains only the caller-filtered `GrantedSessions` rows for that session with reveal suppression applied (never exposing hidden Media identity).
- **Legacy `controlSession` and `profileSessions`**:
  - Legacy `controlSession` (and `/v1/playback-control`) atomically bumps the target session's revision on publish so revision-checked callers observe its effects; `controlSession` remains strictly same-Profile as required by ADR-0240 D3.
  - `profileSessions` exposes `revision` (0 when none) on each session row.
- **Wire-contract and schema goldens**: regenerated via `make regen-wire-goldens`.

## Non-goals

- Cross-Profile player display authorization and controller lists (separate follow-up tickets under ADR-0240 D1/D2/D4/D6).
- Multi-session transfer operations (ADR-0151 D4) and web/native client UI callers.
- Publishing a new versioned `wire-contract-v*.json` artifact or modifying `contracts/registry.json` consumer pins (handled at coordinated stack publish).

## Failure modes and rollback

- **Failure modes**: non-atomic revision/event/ledger updates leaving revision out of sync with outbox or ledger; replay returning a stale or wrong result or issuing a duplicate outbox event; `commandID` reuse across different commands or sessions succeeding silently; `CONFLICT` snapshot leaking another Profile's session or reveal-suppressed Media identity.
- **Rollback**: all new storage lives in additive events migration `0030_renderer_session_control.sql` (`renderer_session_revision`, `renderer_session_command`); reverting the GraphQL field, store methods, and regenerated wire goldens leaves existing tables and `controlSession` intact.

## Acceptance evidence

- Store tests covering apply, stale revision conflict, idempotent replay (no second outbox event, no second revision increment), `commandID` reuse with a different body or session, 24h ledger pruning on write, and legacy `controlSession` bumping the revision.
- Graph tests covering authorization gates, non-leaking refusals, `CONFLICT` snapshot reveal filtering and session scoping, `profileSessions` revision exposure, and input validation (`commandID`, `expectedRevision`, command arguments).
- `openspec validate --all --strict`, `gofmt -l`, `golangci-lint run`, and conformance/wire-golden checks pass.
