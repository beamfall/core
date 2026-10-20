-- SUBSYS-2 (ADR-0194 §3, amending ADR-0177 §5): the per-item subtitle-choice
-- record widens to SERIES scope for episodic content. A viewer's manual
-- subtitle pick — and the audio track changed in the same interaction, if any —
-- persists per (profile, group), where the group is a series/ordered-container
-- key for episodic content and the item itself otherwise. The stored choice
-- beats later profile-pref changes and never writes back to profile prefs
-- (ADR-0194 §3 pinned semantics 2/4).
--
-- The track identity is the format-independent language+kind+role tuple
-- (ADR-0194 §3 / ADR-0177 §5), never a stream index — so the pin survives
-- renditions and remuxes. `selected` distinguishes a pinned track (1) from a
-- deliberate subtitles-off pin (0). `audio_language` records the paired audio
-- choice when audio+subtitle were changed together as one interaction; '' means
-- the subtitle pick stood alone.
--
-- group_key and audio_language carry only identifiers the profile already keys
-- its own playback state on; no title, path, or content reference is stored, so
-- reveal rules do not apply to this per-profile preference row.
CREATE TABLE subtitle_choice_record (
  profile_id          TEXT NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  group_key           TEXT NOT NULL,
  selected            INTEGER NOT NULL DEFAULT 1,
  sub_language        TEXT NOT NULL DEFAULT '',
  sub_kind            TEXT NOT NULL DEFAULT '' CHECK (sub_kind IN ('', 'muxed', 'sidecar')),
  sub_forced          INTEGER NOT NULL DEFAULT 0,
  sub_sdh             INTEGER NOT NULL DEFAULT 0,
  sub_visual_impaired INTEGER NOT NULL DEFAULT 0,
  sub_commentary      INTEGER NOT NULL DEFAULT 0,
  audio_language      TEXT NOT NULL DEFAULT '',
  updated_at_ms       INTEGER NOT NULL,
  PRIMARY KEY (profile_id, group_key)
);
