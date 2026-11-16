-- CORE-2055: the routed encrypted-library stream inventory (0002) named the
-- stream kind `type` and lacked most of catalog 0050's per-stream columns, so
-- the shared inventory writer failed with "no such column: stream_type" on
-- every probed import into an Encrypted Library. Mirror catalog 0050's columns
-- additively; the frozen 0002 columns stay, and the writer keeps the legacy
-- `type` (and the font attachment `stream_index`) filled on this plane.
ALTER TABLE media_file_stream ADD COLUMN stream_type TEXT NOT NULL DEFAULT '';
ALTER TABLE media_file_stream ADD COLUMN profile TEXT NOT NULL DEFAULT '';
ALTER TABLE media_file_stream ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_file_stream ADD COLUMN is_forced INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_file_stream ADD COLUMN channel_layout TEXT NOT NULL DEFAULT '';
ALTER TABLE media_file_stream ADD COLUMN bit_depth INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_file_stream ADD COLUMN color_transfer TEXT NOT NULL DEFAULT '';
ALTER TABLE media_file_stream ADD COLUMN color_primaries TEXT NOT NULL DEFAULT '';
ALTER TABLE media_file_stream ADD COLUMN hdr10_plus INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_file_stream ADD COLUMN dovi_profile INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_file_stream ADD COLUMN dovi_bl_compat_id INTEGER NOT NULL DEFAULT 0;
UPDATE media_file_stream SET stream_type = type;

-- The shared writer upserts font attachments on (content_fingerprint,
-- filename), catalog 0050's key.
CREATE UNIQUE INDEX media_file_font_attachment_filename_idx
  ON media_file_font_attachment(content_fingerprint, filename);
