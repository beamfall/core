-- CORE-1742: an Offline Pack may hold Media from an unsealed Encrypted Library.
-- That Media lives in the Library's own database, not the catalog media table,
-- so 0083's `media_id REFERENCES media(id)` rejected every encrypted pack item
-- with a foreign-key failure. The pack spine keeps its pack FK; Media identity
-- is checked by the offline pack resolver (VisibleMediaBatch) before any row is
-- written, the same visibility read that admits a public item.
--
-- SQLite cannot drop a column constraint, so the table is recreated with the
-- standard rebuild (CREATE _new, INSERT...SELECT, DROP, RENAME). No other table
-- references offline_pack_item, so the rebuild leaves no dangling reference.
-- The shape reproduces 0083 plus the 0103 columns.
--
-- 0083's ON DELETE CASCADE from catalog media is preserved by a trigger: when a
-- catalog Media row is deleted its pack items go with it, exactly as before.

CREATE TABLE offline_pack_item_new (
  pack_id          TEXT    NOT NULL REFERENCES offline_pack(id) ON DELETE CASCADE,
  media_id         TEXT    NOT NULL,
  sort_order       INTEGER NOT NULL DEFAULT 0,
  selected_version TEXT    NOT NULL DEFAULT '',
  plaintext_size   INTEGER NOT NULL DEFAULT 0,
  sha256           TEXT    NOT NULL DEFAULT '',
  ciphertext_path  TEXT    NOT NULL DEFAULT '',
  PRIMARY KEY (pack_id, media_id)
);

INSERT INTO offline_pack_item_new
  (pack_id, media_id, sort_order, selected_version, plaintext_size, sha256, ciphertext_path)
SELECT pack_id, media_id, sort_order, selected_version, plaintext_size, sha256, ciphertext_path
FROM offline_pack_item;

DROP TABLE offline_pack_item;

ALTER TABLE offline_pack_item_new RENAME TO offline_pack_item;

CREATE INDEX IF NOT EXISTS offline_pack_item_order_idx
  ON offline_pack_item(pack_id, sort_order, media_id);

CREATE TRIGGER IF NOT EXISTS offline_pack_item_catalog_media_delete
AFTER DELETE ON media
BEGIN
  DELETE FROM offline_pack_item WHERE media_id = OLD.id;
END;
