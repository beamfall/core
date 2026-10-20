-- ADR0068-UQID-1 (ADR-0068 §Recommended plan) — the Media-INDEPENDENT Unmatched
-- Record. ADR-0068 makes the Unmatched queue "a single review inbox for failed
-- or uncertain identity, regardless of whether the record came from scan, parser,
-- scraper, migration import, feed, or manual upload," and mandates a
-- "stable Unmatched Record identity separate from Media identity."
--
-- The pre-existing unmatched_queue (0056) is Media-KEYED: media_id is its primary
-- key REFERENCES media(id). That shape only fits scan-time below-threshold items,
-- which already have provisional Media. It cannot represent an import record that
-- resolved to NO Media at all — migration-import unmatched items (ADR-0037 §2.3
-- step 4) currently vanish into a per-run COUNT and are never reviewable. This
-- table is the record identity those import (and future pre-Media scan/parser)
-- records need: an opaque id of its own, and media_id nullable.
--
-- Distinct from unmatched_queue, which is left byte-for-byte unchanged (no rebuild
-- of any existing table). unmatched_queue stays the scan current-state set keyed
-- by Media; unmatched_record is the Media-independent review inbox keyed by its
-- own opaque id, into which import records (and later other origins) are routed.
--
-- Shape is generic (invariant #1): no domain vocabulary. origin, failure_reason,
-- and the source handle are shape words; the importer/plugin id carries species.
-- Nothing here is an entity-query predicate — this is an operator review surface,
-- off the hot path (like unmatched_queue and library_health).
--
-- Append-only forward migration past the FREEZE-4 marker (ADR-0011 §2): a
-- brand-new table, no rebuild of any existing table. Bumps
-- schemaFreezeCatalogMigrations 91 → 92 in schema_freeze.go in this same commit.
-- Off the entity-query hot path, so it does not perturb the benchmarked shape.
CREATE TABLE unmatched_record (
    -- Opaque record id (urec-<hex>), the Media-INDEPENDENT identity. Minted by the
    -- store, never derived from Media — the record exists whether or not any Media
    -- backs it.
    id             TEXT    NOT NULL PRIMARY KEY,

    -- Where the record entered the inbox: 'scan' | 'import' (parser/scraper/feed
    -- origins reuse these as they land). A shape word, never a domain term.
    origin         TEXT    NOT NULL,

    -- Nullable owning Library. An import record may resolve to no Media and no
    -- Library yet; NULL means "operator-global" (visible to any Manager scope, the
    -- operator-surface posture). ON DELETE SET NULL keeps the record reviewable
    -- even if the tentatively-associated Library is later removed.
    library_id     TEXT             REFERENCES library(id) ON DELETE SET NULL,

    -- Nullable tentative/attached Media. NULL for a pure no-Media import record;
    -- set when the record attaches to a provisional Media. ON DELETE SET NULL so a
    -- Media removal does not orphan the record's own identity.
    media_id       TEXT             REFERENCES media(id) ON DELETE SET NULL,

    -- Provenance, verbatim shape-named ids (invariant #1) — never parsed by core,
    -- only compared/grouped for per-run audit and re-run skip-explanation (ADR-0037
    -- §2.4). Empty when the origin is not an import run.
    import_run_id      TEXT NOT NULL DEFAULT '',
    import_importer_id TEXT NOT NULL DEFAULT '',

    -- A generic source handle for the operator to recognize the record: a file
    -- path or an external-id summary. No domain vocabulary; opaque display text.
    source_handle  TEXT    NOT NULL DEFAULT '',

    -- Best-candidate the origin found, so Fix Match can pre-fill the picker.
    -- All empty/zero when the search found no candidate at all.
    candidate_id    TEXT   NOT NULL DEFAULT '',
    candidate_title TEXT   NOT NULL DEFAULT '',
    confidence      REAL   NOT NULL DEFAULT 0,

    -- Why identity failed (ADR-0068 Amendment §A failure-reason taxonomy):
    -- 'no-candidates' | 'below-threshold' | 'ambiguous' | 'library-mismatch' |
    -- 'external-id-conflict'. A shape word the Console renders; never domain.
    failure_reason TEXT    NOT NULL DEFAULT '',

    queued_at_ms   INTEGER NOT NULL,

    -- Resolution audit (ADR-0068 associate-vs-create): stamped once, idempotently,
    -- when the operator resolves the record. resolved_media_id is the Media the
    -- record was associated with or that resolution created. Both NULL while the
    -- record is still pending review.
    resolved_at_ms       INTEGER,
    resolved_media_id    TEXT           REFERENCES media(id) ON DELETE SET NULL
);

-- Pending records newest-first (the inbox read order), scoped to still-unresolved
-- rows so the operator surface never re-scans resolved history.
CREATE INDEX unmatched_record_pending_idx
    ON unmatched_record (queued_at_ms)
    WHERE resolved_at_ms IS NULL;

-- Group/filter by origin for per-source counts, and resolve by tentative Media.
CREATE INDEX unmatched_record_origin_idx ON unmatched_record (origin);
CREATE INDEX unmatched_record_media_idx ON unmatched_record (media_id);
