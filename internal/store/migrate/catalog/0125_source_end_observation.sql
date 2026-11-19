-- CORE-2380: premature EOF is source-generation state, not a session cache.
-- Encrypted Libraries keep the same state in their sealed catalog plane.
CREATE TABLE source_end_observation (
 content_fingerprint TEXT NOT NULL REFERENCES media_file(content_fingerprint) ON DELETE CASCADE,
 generation TEXT NOT NULL,
 end_seconds REAL NOT NULL CHECK (end_seconds >= 0),
 PRIMARY KEY (content_fingerprint, generation)
);
