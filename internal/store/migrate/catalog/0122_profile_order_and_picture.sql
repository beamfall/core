-- CORE-2477: the manager-chosen order of Profiles in every picker. Existing
-- Profiles keep their old name order.
ALTER TABLE profile ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0;
UPDATE profile SET sort_order = (
  SELECT COUNT(*) FROM profile AS earlier
  WHERE earlier.name < profile.name OR (earlier.name = profile.name AND earlier.id < profile.id)
);

-- CORE-1240: a Profile's uploaded picture, normalized by the server to a
-- square JPEG with no metadata. One row per Profile that has one.
CREATE TABLE profile_picture (
  profile_id TEXT PRIMARY KEY REFERENCES profile(id) ON DELETE CASCADE,
  image BLOB NOT NULL,
  updated_at_ms INTEGER NOT NULL
);
