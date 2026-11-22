CREATE TABLE media_operator_ordinal (
    media_id TEXT NOT NULL PRIMARY KEY REFERENCES media(id) ON DELETE CASCADE,
    sequence TEXT NOT NULL CHECK (length(sequence) <= 32),
    ordinal_index INTEGER NOT NULL CHECK (ordinal_index BETWEEN 0 AND 2147483647),
    set_at_ms INTEGER NOT NULL,
    actor_profile_id TEXT REFERENCES profile(id) ON DELETE SET NULL
);
