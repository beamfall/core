-- METAPARITY-7 (ADR-0068 §A) — SCOPED suppression for the Unmatched review
-- inbox. Before this table "Ignore" was a single undifferentiated act: the
-- operator could clear a queue row (dismissUnmatched) but could not say WHICH
-- identity they meant to stop hearing about, and could not undo one decision
-- without undoing the others.
--
-- ADR-0068 requires three INDEPENDENT scopes, defaulting to the source record:
--
--   'source-record' — skip this Unmatched Record on re-import. An independent
--                     scan may still surface its file; that is a different
--                     identity and a different decision.
--   'file'          — skip this stable file identity on re-scan. Other files and
--                     other source records stay eligible.
--   'candidate'     — exclude ONE candidate for ONE Unmatched Record. Ignoring a
--                     wrong suggestion must never suppress the whole record.
--
-- Because each scope is its own row keyed by (scope, scope_key), resetting one
-- cannot reset a sibling — the independence is structural, not a code
-- convention. Provenance (who, when, and the exact identity) is carried on the
-- row so every active suppression can state its own reason for existing and
-- offer its own reset.
--
-- Shape is generic (invariant #1): scope is a shape word, and scope_key holds an
-- opaque identity string (record id, content fingerprint, or record+candidate
-- pair) that core compares but never parses for meaning. Paths are never the
-- durable identity here — they are display context only.
--
-- Append-only forward migration past the FREEZE-4 marker (ADR-0011 §2): a
-- brand-new table, no rebuild of any existing table. Bumps
-- schemaFreezeCatalogMigrations 99 -> 100 in schema_freeze.go in this same
-- commit. It is an operator review surface off the entity-query hot path (like
-- unmatched_record and library_health), so it does not perturb the benchmarked
-- shape.
CREATE TABLE unmatched_suppression (
    -- Which identity the operator suppressed: 'source-record' | 'file' |
    -- 'candidate'. Never inferred from a generic "Ignore" — the caller states it.
    scope       TEXT NOT NULL,

    -- The opaque identity within that scope. For 'source-record' the Unmatched
    -- Record id; for 'file' the stable content fingerprint; for 'candidate' the
    -- record id and candidate id joined, so the same candidate stays eligible
    -- for every OTHER record.
    scope_key   TEXT NOT NULL,

    -- Provenance the operator surface renders alongside the reset control. The
    -- record id is kept separately (not only inside scope_key) so a candidate
    -- suppression can name the Unmatched Record it belongs to; NULL when the
    -- scope carries no record.
    record_id   TEXT,
    -- Display handle (path or external-id summary) captured at suppression time
    -- so provenance stays readable even after the source moves. Context only.
    source_handle TEXT NOT NULL DEFAULT '',
    -- Which Profile performed the suppression; NULL for an ops/token actor.
    actor_profile_id TEXT,
    created_at_ms INTEGER NOT NULL,

    PRIMARY KEY (scope, scope_key)
);

-- List the active suppressions for one record's review panel (all three scopes
-- of a record resolve through record_id).
CREATE INDEX unmatched_suppression_record_idx ON unmatched_suppression (record_id);
