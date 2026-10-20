-- ADR-0054 — pre-ADR-0011-freeze reservation for per-Profile Mute
-- (user-facing "Not interested"): a self-authored soft discovery suppressor of
-- any Entity or any Tag (§2.3), distinct from operator-set enforced filters and
-- security-grade Restrictions.
--
-- Per-Profile personal preference, so it lives in the events database alongside
-- media_favorite (0009) — it is the person's own data (My Data export/clear,
-- §2.8) and syncs to clients with the Snapshot. target_kind is 'entity' or 'tag';
-- target_id is the entity id or tag id. include_descendants honors hierarchical
-- Genre/Tag targets (§2.3), reusing the modifier already in the filter vocabulary.
--
-- This reservation is the *storage* only. The negative Source predicate that reads
-- this list (exclude Media related to a muted Entity or carrying a muted Tag, with
-- "specificity wins" overrides, §2.5-2.6) is feature work — it is runtime query
-- code over existing columns and is not expensive to add after the freeze.
CREATE TABLE profile_mute (
  profile_id TEXT NOT NULL,
  target_kind TEXT NOT NULL,
  target_id TEXT NOT NULL,
  include_descendants INTEGER NOT NULL DEFAULT 0,
  muted_at_ms INTEGER NOT NULL,
  PRIMARY KEY (profile_id, target_kind, target_id)
);
