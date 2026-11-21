-- CORE-2762: Staged contributions live beside engagement so erasure and
-- replay can each remove them in the same events transaction as their writes.
CREATE TABLE version_merge_playback (
    batch_id TEXT NOT NULL,
    profile_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    media_id TEXT NOT NULL,
    payload TEXT NOT NULL,
    PRIMARY KEY (batch_id, profile_id)
);

CREATE INDEX idx_version_merge_playback_profile ON version_merge_playback(profile_id);
