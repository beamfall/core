-- My-Data full footprint (ADR-0036 §2.4): record which Profile authored a
-- shared library artifact so My-Data delete can DISOWN it (clear the column)
-- rather than delete the row. Markers and SmartCollections are server-global
-- contributions — deleting them on a per-Profile data wipe would degrade the
-- library for every other Profile, so the column is nulled instead. Nullable:
-- legacy and operator-created rows have no authoring Profile. No FK cascade —
-- removing a Profile must not delete shared artifacts (that is the whole point
-- of disown); the column is left dangling and read defensively.
ALTER TABLE marker ADD COLUMN created_by_profile_id TEXT;
ALTER TABLE smart_collection ADD COLUMN created_by_profile_id TEXT;

CREATE INDEX IF NOT EXISTS marker_created_by_idx ON marker(created_by_profile_id);
CREATE INDEX IF NOT EXISTS smart_collection_created_by_idx ON smart_collection(created_by_profile_id);
