-- Per-Library preview-clip toggle (ADR-0033 §4): preview-clip generation is
-- a per-Library setting, default ON. Audio-class Media never gets clips
-- regardless — the generator gates on Media class, this column on operator
-- choice.
ALTER TABLE library ADD COLUMN preview_clips_enabled INTEGER NOT NULL DEFAULT 1;
