-- CORE-3016: an Offline Pack carries every Part of a Media's selected Version.
-- A multi-file audiobook is one Media whose audio files are Parts
-- (media_file.part_ordinal), so a pack item is keyed by its Part as well as
-- its Media. Existing items are single-Part and take part_ordinal 0.
--
-- The standard rebuild (CREATE _new, INSERT...SELECT, DROP, RENAME) changes the
-- primary key; the shape otherwise reproduces 0115. The order index and the
-- catalog Media delete trigger are recreated as 0115 left them, with the index
-- extended by part_ordinal.

CREATE TABLE offline_pack_item_new (
  pack_id          TEXT    NOT NULL REFERENCES offline_pack(id) ON DELETE CASCADE,
  media_id         TEXT    NOT NULL,
  part_ordinal     INTEGER NOT NULL DEFAULT 0 CHECK (part_ordinal >= 0),
  sort_order       INTEGER NOT NULL DEFAULT 0,
  selected_version TEXT    NOT NULL DEFAULT '',
  plaintext_size   INTEGER NOT NULL DEFAULT 0,
  sha256           TEXT    NOT NULL DEFAULT '',
  ciphertext_path  TEXT    NOT NULL DEFAULT '',
  PRIMARY KEY (pack_id, media_id, part_ordinal)
);

INSERT INTO offline_pack_item_new
  (pack_id, media_id, part_ordinal, sort_order, selected_version, plaintext_size, sha256, ciphertext_path)
SELECT pack_id, media_id, 0, sort_order, selected_version, plaintext_size, sha256, ciphertext_path
FROM offline_pack_item;

-- The 0115 trigger on media names offline_pack_item, so it goes before the
-- table and is recreated below.
DROP TRIGGER IF EXISTS offline_pack_item_catalog_media_delete;

DROP TABLE offline_pack_item;

ALTER TABLE offline_pack_item_new RENAME TO offline_pack_item;

CREATE INDEX IF NOT EXISTS offline_pack_item_order_idx
  ON offline_pack_item(pack_id, sort_order, media_id, part_ordinal);

CREATE TRIGGER IF NOT EXISTS offline_pack_item_catalog_media_delete
AFTER DELETE ON media
BEGIN
  DELETE FROM offline_pack_item WHERE media_id = OLD.id;
END;
