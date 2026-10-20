-- Per-stream inventory captured at scan (ADR-0010 §4, PLAY-1). ffprobe results
-- are schema: every stream's type, codec, profile, language, default/forced
-- flags, channel layout, bit depth, and the HDR fields (Dolby Vision profile
-- 5/7/8, HDR10/HDR10+ transfer + primaries + HDR10+ dynamic-metadata flag) —
-- plus muxed font attachments.
-- Track menus, subtitle conversion, and the HDR decision table all read from
-- here. Re-probing a library later is a full rescan (no partial backfill); scan
-- deletes-then-reinserts every row for a content fingerprint, so the inventory
-- always reflects the latest probe.
--
-- These rows hang off media_file by content_fingerprint (the file-plane
-- identity, ADR-0009): the inventory is a property of the bytes, not of the
-- Media row a file currently backs, and survives a re-key/merge cleanly.
-- ON DELETE CASCADE reaps the inventory when a media_file row is dropped.
--
-- This is generic container shape, not domain vocabulary (invariant #1): stream
-- type / codec / channel layout / HDR are properties every video or audio file
-- has regardless of what plugin reads them.
CREATE TABLE IF NOT EXISTS media_file_stream (
  content_fingerprint TEXT NOT NULL
    REFERENCES media_file(content_fingerprint) ON DELETE CASCADE,
  -- stream_index is ffprobe's stream order (0-based); (fingerprint, index) is
  -- the natural key, so a re-probe can address the same logical stream.
  stream_index INTEGER NOT NULL,
  -- stream_type is the ffprobe codec_type: 'video' | 'audio' | 'subtitle' |
  -- 'attachment' | 'data'.
  stream_type TEXT NOT NULL,
  codec TEXT NOT NULL DEFAULT '',
  -- profile is the codec profile ('Main 10', 'High'); '' when ffprobe reports
  -- none (or 'unknown', which scan normalises to '').
  profile TEXT NOT NULL DEFAULT '',
  -- level is the codec level ffprobe reports (H.264 4.1 => 41, HEVC 5.1 =>
  -- 153); 0 when absent.
  level INTEGER NOT NULL DEFAULT 0,
  -- language is the ISO-639 tag from the stream tags ('eng', 'spa'); '' when
  -- untagged.
  language TEXT NOT NULL DEFAULT '',
  is_default INTEGER NOT NULL DEFAULT 0,
  is_forced INTEGER NOT NULL DEFAULT 0,
  is_hearing_impaired INTEGER NOT NULL DEFAULT 0,
  is_visual_impaired INTEGER NOT NULL DEFAULT 0,
  is_commentary INTEGER NOT NULL DEFAULT 0,
  -- channels / channel_layout describe audio streams (8, '7.1'); 0 / '' on
  -- non-audio streams.
  channels INTEGER NOT NULL DEFAULT 0,
  channel_layout TEXT NOT NULL DEFAULT '',
  -- bit_depth is the coded sample bit depth; 0 when ffprobe omits it.
  bit_depth INTEGER NOT NULL DEFAULT 0,
  -- color_transfer / color_primaries carry the HDR transfer characteristics
  -- ('smpte2084' = PQ/HDR10, 'arib-std-b67' = HLG) and primaries ('bt2020');
  -- '' when absent (SDR streams).
  color_transfer TEXT NOT NULL DEFAULT '',
  color_primaries TEXT NOT NULL DEFAULT '',
  -- hdr10_plus marks streams with HDR10+ dynamic metadata (SMPTE ST 2094-40).
  hdr10_plus INTEGER NOT NULL DEFAULT 0,
  -- dovi_profile is the Dolby Vision profile (5/7/8) from the stream's DOVI
  -- configuration record; 0 when the stream carries no DV. dovi_bl_compat_id is
  -- the base-layer signal compatibility id (6 = HDR10-compatible) that tells
  -- profile-8 variants apart.
  dovi_profile INTEGER NOT NULL DEFAULT 0,
  dovi_bl_compat_id INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (content_fingerprint, stream_index)
);

-- Muxed font attachments (ADR-0010 §4/§5): libass renders ASS subtitles with
-- full fidelity using fonts pulled from these. Also hang off media_file by
-- content_fingerprint, full-replaced on every rescan like the streams above.
CREATE TABLE IF NOT EXISTS media_file_font_attachment (
  content_fingerprint TEXT NOT NULL
    REFERENCES media_file(content_fingerprint) ON DELETE CASCADE,
  filename TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (content_fingerprint, filename)
);
