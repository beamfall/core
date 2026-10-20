-- Extras + Editions (ADR-0009 §3, and the Edition half of §2). Two related
-- file-plane shapes, both modelled WITHOUT any domain vocabulary (invariant #1):
--
--   * Extra  — a typed child Media that is excluded BY DEFAULT from the
--     discovery surfaces (browse Sources, continue-watching, dedupe, stats),
--     while staying directly addressable. The TYPE of extra (trailer / BTS /
--     deleted-scene / interview / sample) is plugin display vocabulary; Core
--     carries only the generic SHAPE bit "this Media is an extra, exclude it
--     from the default surfaces". That bit is `media.extra`.
--
--   * Edition — a separate cut of the same work (a different runtime, markers,
--     and resume) is a SEPARATE Media linked to its siblings by an `edition-of`
--     relation and grouped on detail screens (ADR-0009 §2/§3, CONTEXT.md). The
--     parent link of an Extra is the sibling `extra-of` relation. Both are
--     Media->Media relations.
--
-- APPEND-ONLY: this is a NEW forward migration; the catalog max was 0053. It
-- does not edit any applied migration (0003/0004/0005/0010/0053 are already on
-- beta databases). Every existing media row keeps its data: the new `extra`
-- column defaults to 0 (NOT an extra — the pre-migration semantics for every
-- already-imported Media), and `media_relation` starts empty, so no row is
-- reclassified or dropped (invariant #6 — destruction stays recoverable;
-- nothing is silently lost).

-- The generic "this Media is an extra, exclude it from the default discovery
-- surfaces" bit. 0 = a normal Media (the default, and every pre-migration row);
-- 1 = an Extra, suppressed from browse/continue/dedupe/stats Sources unless a
-- caller explicitly asks for extras. Named by SHAPE, never by domain: there is
-- no trailer/BTS/scene column — the extra's display type is plugin vocabulary.
ALTER TABLE media ADD COLUMN extra INTEGER NOT NULL DEFAULT 0;

-- Partial index for the "extras of X" direct-address read and for the negative
-- "exclude extras" predicate's planner: only the (few) extra rows are indexed.
CREATE INDEX media_extra_idx ON media(library_id) WHERE extra = 1;

-- Media->Media relations, named per a fixed Core vocabulary of shapes (NOT
-- domain labels): `edition-of` (a cut grouped with its sibling cuts) and
-- `extra-of` (an Extra child grouped under its parent Media). Mirrors the
-- entity_relation table (0010), but both endpoints are Media rows. attributes
-- is reserved JSON, rendered never filtered (the entity_relation precedent,
-- ADR-0011 §2); v1 writers leave it NULL.
CREATE TABLE media_relation (
  from_media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  relation TEXT NOT NULL,
  to_media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  attributes TEXT,
  -- A Media can never relate to itself under any relation (an edition is not an
  -- edition-of itself; an extra is not an extra-of itself).
  CHECK (from_media_id <> to_media_id),
  -- One row per (from, relation, to): re-asserting the same link replaces it.
  PRIMARY KEY (from_media_id, relation, to_media_id)
);

-- Reverse-lookup index: "what relates TO this Media under this relation" — the
-- read that fetches a parent's extras (to_media_id = parent, relation =
-- 'extra-of') and a cut's edition siblings (to_media_id = peer, relation =
-- 'edition-of').
CREATE INDEX media_relation_to_idx ON media_relation(to_media_id, relation);
