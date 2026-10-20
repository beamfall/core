-- Sidecar caption tracks discovered at scan (FEAT-4 / ADR-0005 §4). A muxed
-- subtitle lives inside the container and is addressed by its ffprobe stream
-- index out of media_file_stream (0050). A SIDECAR caption is a separate file
-- next to the media file on disk — "movie.en.srt", "movie.en.forced.srt",
-- "movie.en.sdh.vtt" — with its own path and no muxed stream index, so it does
-- not fit the (content_fingerprint, stream_index) inventory. It gets its own
-- table.
--
-- Like the stream inventory, these rows hang off media_file by
-- content_fingerprint (the file-plane identity, ADR-0009): a sidecar is
-- discovered as a sibling of a specific media file's bytes, so it is a property
-- of those bytes, not of the Media row a file currently backs, and survives a
-- re-key/merge cleanly. ON DELETE CASCADE reaps the sidecars when the media_file
-- row is dropped. Re-scan full-replaces every sidecar row for a content
-- fingerprint (delete-then-insert), exactly like the stream inventory, so a
-- removed sidecar drops and a new one appears on the next pass.
--
-- These are generic player concepts, never domain vocabulary (invariant #1):
-- a caption/subtitle track with a language tag, a forced flag, and an
-- SDH/hearing-impaired flag is shape every media file may carry regardless of
-- what plugin reads it.
CREATE TABLE IF NOT EXISTS media_file_sidecar_caption (
  content_fingerprint TEXT NOT NULL
    REFERENCES media_file(content_fingerprint) ON DELETE CASCADE,
  -- rel_path is the sidecar file's path RELATIVE to the media file's directory
  -- (e.g. "movie.en.forced.srt"). It is the natural per-file key (a media file
  -- can have many sidecars but not two with the same sibling name) and the value
  -- the byte route joins back to an absolute on-disk path under the library
  -- root — the path-escape guard re-confines it at serve time.
  rel_path TEXT NOT NULL,
  -- format is the closed sidecar caption vocabulary: 'srt' | 'vtt'. The byte
  -- route converts srt to WebVTT for the wire; vtt is served as-is.
  format TEXT NOT NULL DEFAULT '',
  -- language is the ISO-639 tag parsed from the filename hint ('en', 'spa');
  -- '' when the filename carries no language segment.
  language TEXT NOT NULL DEFAULT '',
  -- is_forced marks a forced-narrative track (the "movie.en.forced.srt" hint):
  -- a track the player shows even when subtitles are otherwise off.
  is_forced INTEGER NOT NULL DEFAULT 0,
  -- is_sdh marks a hearing-impaired / SDH track (the "movie.en.sdh.srt" hint).
  is_sdh INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (content_fingerprint, rel_path)
);
