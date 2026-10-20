-- HANDSHAKE-0063-COMPAT-DECISION-DURABILITY-1 (ADR-0063) — durable tombstones
-- for invalidated compatibility subjects.
--
-- The compatibility decision registry is in-memory: it is constructed empty at
-- every boot. That made "this subject was invalidated" and "this subject never
-- handshook" the same observable (ErrNoDecision) across a restart, and the
-- Stream Token verify seam treats the latter as benign. An already-issued,
-- unexpired PRE-BINDING (unstamped) token therefore became usable again after a
-- restart for as long as the signing key stayed active.
--
-- Only the REFUSAL side is persisted. Accepted decisions stay in memory on
-- purpose: losing an acceptance is fail-safe (the client re-handshakes), while
-- losing a refusal is fail-open. One row per subject, so the set is bounded by
-- the number of distinct credentials, and every row carries its own expiry so
-- the set cannot grow without bound even if a subject never returns.
--
-- Retention must exceed the longest Stream Token lifetime a pre-binding token
-- could still carry, or the tombstone would expire before the token it exists
-- to reject; capabilities.InvalidationRetention states that bound.
--
-- Subject strings are opaque credential identities minted by the server
-- ("session:<id>", "api-key:<id>", "admin-token:<id>"). Core compares them and
-- never parses them for meaning; the row holds no media, catalog, or domain
-- reference (invariant #1).
--
-- Append-only forward migration past the FREEZE-4 marker (ADR-0011 §2): a
-- brand-new table, no rebuild of any existing table. Bumps
-- schemaFreezeCatalogMigrations 101 -> 102 in schema_freeze.go in this same
-- commit. It is read once at boot (full-table rehydration) and written only on
-- the handshake path, so it is off the entity-query hot path entirely.
CREATE TABLE compat_decision_invalidation (
    -- The opaque credential subject whose compatibility decision was refused.
    subject         TEXT PRIMARY KEY,

    -- The closed-vocabulary decision and reason that refused it, retained so a
    -- rehydrated registry can surface the ORIGINAL refusal rather than a
    -- synthesized generic one.
    decision        TEXT NOT NULL DEFAULT '',
    reason_code     TEXT NOT NULL DEFAULT '',

    -- When the refusal was recorded, and when the tombstone stops applying.
    recorded_at_ms  INTEGER NOT NULL,
    expires_at_ms   INTEGER NOT NULL
);

-- Backs the expiry prune, which runs on every rehydration and every write so an
-- abandoned subject's tombstone is reclaimed without a separate sweeper job.
CREATE INDEX compat_decision_invalidation_expiry_idx
    ON compat_decision_invalidation (expires_at_ms);
