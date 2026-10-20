-- Pre-freeze Profile schema (ADR-0011 freeze): session-admission policy
-- (max_concurrent_streams NULL = unlimited; disabled hides the Profile and
-- refuses new sessions while preserving engagement history), transcode
-- policy, and deterministic track pre-selection preferences (ADR-0010).
-- Language lists are stored as JSON arrays of ordered language tags;
-- subtitle_mode holds the off|smart|forced-only|always vocabulary,
-- validated in code.
ALTER TABLE profile ADD COLUMN max_concurrent_streams INTEGER;
ALTER TABLE profile ADD COLUMN transcode_allowed INTEGER NOT NULL DEFAULT 1;
ALTER TABLE profile ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE profile ADD COLUMN audio_languages TEXT NOT NULL DEFAULT '[]';
ALTER TABLE profile ADD COLUMN subtitle_languages TEXT NOT NULL DEFAULT '[]';
ALTER TABLE profile ADD COLUMN subtitle_mode TEXT NOT NULL DEFAULT 'off';
ALTER TABLE profile ADD COLUMN prefer_sdh INTEGER NOT NULL DEFAULT 0;
