-- HANDSHAKE-0063-COMPAT-DECISION-DURABILITY-1 (ADR-0063) — durable staging for
-- a generation-invalidation batch.
--
-- A generation advance invalidates every previously accepted subject at once.
-- Writing those tombstones as independent rows left a partial-durability
-- window: process death between row writes rehydrated only the committed rows,
-- so an omitted invalid subject read back as "never handshook" (ErrNoDecision)
-- — the exact fail-open observable `compat_decision_invalidation` exists to
-- prevent.
--
-- This table makes the batch replayable. The whole batch is staged here in one
-- transaction FIRST; promotion into `compat_decision_invalidation` plus the
-- staging clear is a second single transaction. Rehydration promotes any rows
-- still staged before it answers, so a batch that was durably staged is always
-- completed before resolution serves traffic — there is no crash point after
-- staging that loses part of the batch. A newer batch supersedes an older one:
-- staging clears the table in the same transaction that fills it.
--
-- Columns mirror `compat_decision_invalidation` exactly; rows exist only for
-- the instants between staging and promotion, so the table is empty in steady
-- state and needs no expiry index.
--
-- Subject strings are opaque credential identities minted by the server; the
-- row holds no media, catalog, or domain reference (invariant #1). Append-only
-- forward migration past the FREEZE-4 marker (ADR-0011 §2): a brand-new table,
-- no rebuild of any existing table. Bumps schemaFreezeCatalogMigrations
-- 102 -> 103 in schema_freeze.go in this same commit. It is written and read
-- only on the invalidation path, so it is off the entity-query hot path
-- entirely.
CREATE TABLE compat_decision_invalidation_staged (
    subject         TEXT PRIMARY KEY,
    decision        TEXT NOT NULL DEFAULT '',
    reason_code     TEXT NOT NULL DEFAULT '',
    recorded_at_ms  INTEGER NOT NULL,
    expires_at_ms   INTEGER NOT NULL
);
