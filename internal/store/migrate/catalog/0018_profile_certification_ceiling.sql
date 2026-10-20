-- Per-Profile certification ceilings (ADR-0028 §5, ADR-0004's
-- "plugin-provided rating ceilings", mechanized): one row per
-- (Profile, plugin) pins a ceiling value on that plugin's ordered
-- certification scale. Media in the plugin's Libraries whose
-- `certification` Media field ranks above the ceiling is filtered from
-- the Profile's reads server-side, composed with reveal filtering.
-- Unrated Media is blocked under a ceiling by default (fail-closed,
-- consistent with the Reveal posture); allow_unrated is the per-Profile
-- override.
CREATE TABLE profile_certification_ceiling (
  profile_id TEXT NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  plugin_id TEXT NOT NULL,
  ceiling TEXT NOT NULL,
  allow_unrated INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (profile_id, plugin_id)
);
