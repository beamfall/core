-- ACCESS-1: per-filter visibility mode for certification ceilings.
-- 'hide' (default) = blocked Media absent from browse/search (today's behavior).
-- 'locked' = blocked Media visible-but-unplayable; server denies the byte gate.
ALTER TABLE profile_certification_ceiling
  ADD COLUMN filter_visibility TEXT NOT NULL DEFAULT 'hide';
