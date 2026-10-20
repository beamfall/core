-- Content-fingerprint columns for scraper variable substitution
-- (ADR-0009 content-fingerprint identity; ADR-0027 D2 §7): oshash is the
-- OpenSubtitles-style 64-bit hash (size + head/tail 64KiB word sums), phash a
-- 64-bit perceptual difference hash over one sampled frame. Both are
-- lowercase 16-hex strings; '' means "not computed yet" (scan fills them in).
ALTER TABLE media_file ADD COLUMN oshash TEXT NOT NULL DEFAULT '';
ALTER TABLE media_file ADD COLUMN phash TEXT NOT NULL DEFAULT '';
