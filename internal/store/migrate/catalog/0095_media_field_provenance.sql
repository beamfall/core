-- Source-aware metadata-field locks (METAPARITY-3). A single provenance record
-- per (media, field) names the highest-precedence source that authored the
-- field, subsuming the media.<field>_identified booleans: total precedence is
-- user/manual > identified(operator) > parsed(scan) > hydrated > machine-derived
-- (importers are behavioral-state-only, ADR-0037 §2.2, and never write here).
-- A write only lands when its source rank >= the stored source's rank, so a
-- re-identify or scheduled re-match cannot overwrite a source='manual' field.
-- The record is authoritative on conflict; the store keeps the legacy booleans
-- in sync as a derived cache so the existing scan-preservation CASE keeps working.
CREATE TABLE media_field_provenance (
  media_id TEXT NOT NULL,
  field    TEXT NOT NULL, -- 'title' / 'summary' / 'year' or a plugin 'media.<name>' field name
  source   TEXT NOT NULL, -- a MutationSource value ('manual','operator','scan','hydrated','machine-derived')
  PRIMARY KEY (media_id, field)
);

-- Backfill from the legacy booleans so existing identification locks survive the
-- subsumption: a set media.<field>_identified maps to the 'operator' (identified)
-- tier. Unset booleans get no row and read back as the parsed/scan default.
INSERT INTO media_field_provenance(media_id, field, source)
  SELECT id, 'title', 'operator' FROM media WHERE title_identified = 1;
INSERT INTO media_field_provenance(media_id, field, source)
  SELECT id, 'summary', 'operator' FROM media WHERE summary_identified = 1;
INSERT INTO media_field_provenance(media_id, field, source)
  SELECT id, 'year', 'operator' FROM media WHERE year_identified = 1;
