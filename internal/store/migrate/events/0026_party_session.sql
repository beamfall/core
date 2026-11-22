CREATE TABLE party_session (
  session_id TEXT NOT NULL PRIMARY KEY,
  host_profile_id TEXT NOT NULL,
  join_code TEXT NOT NULL,
  fairness TEXT NOT NULL DEFAULT 'round-robin' CHECK (fairness IN ('round-robin', 'fifo')),
  auto_add INTEGER NOT NULL DEFAULT 0 CHECK (auto_add IN (0, 1)),
  join_open INTEGER NOT NULL DEFAULT 1 CHECK (join_open IN (0, 1)),
  library_ids TEXT NOT NULL DEFAULT '[]',
  host_filter TEXT NOT NULL DEFAULT '{}',
  started_at_ms INTEGER NOT NULL,
  expires_at_ms INTEGER NOT NULL,
  ended_at_ms INTEGER,
  current_entry_id TEXT,
  playback_state TEXT NOT NULL DEFAULT 'paused' CHECK (playback_state IN ('playing', 'paused')),
  position_ms INTEGER NOT NULL DEFAULT 0 CHECK (position_ms >= 0),
  anchor_at_ms INTEGER NOT NULL DEFAULT 0,
  tempo REAL NOT NULL DEFAULT 1 CHECK (tempo > 0),
  pitch_semitones INTEGER NOT NULL DEFAULT 0
);

-- A Profile hosts at most one live session, and a join code names at most
-- one live session. Expired sessions are ended before either is claimed.
CREATE UNIQUE INDEX party_session_live_host ON party_session (host_profile_id) WHERE ended_at_ms IS NULL;
CREATE UNIQUE INDEX party_session_live_code ON party_session (join_code) WHERE ended_at_ms IS NULL;

CREATE TABLE party_participant (
  participant_id TEXT NOT NULL PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES party_session (session_id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (name <> ''),
  guest INTEGER NOT NULL DEFAULT 0 CHECK (guest IN (0, 1)),
  host INTEGER NOT NULL DEFAULT 0 CHECK (host IN (0, 1)),
  -- Guest capabilities are stored only as a SHA-256 hex digest.
  token_hash TEXT UNIQUE,
  token_expires_at_ms INTEGER,
  joined_at_ms INTEGER NOT NULL,
  removed_at_ms INTEGER
);

CREATE INDEX party_participant_session ON party_participant (session_id);

CREATE TABLE party_entry (
  entry_id TEXT NOT NULL PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES party_session (session_id) ON DELETE CASCADE,
  participant_id TEXT NOT NULL REFERENCES party_participant (participant_id) ON DELETE CASCADE,
  media_id TEXT NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('pending', 'waiting', 'current', 'done', 'removed', 'declined', 'skipped')),
  prior_state TEXT CHECK (prior_state IS NULL OR prior_state IN ('pending', 'waiting')),
  pinned_position INTEGER CHECK (pinned_position IS NULL OR pinned_position >= 1),
  pitch_semitones INTEGER NOT NULL DEFAULT 0 CHECK (pitch_semitones BETWEEN -12 AND 12),
  added_at_ms INTEGER NOT NULL,
  current_at_ms INTEGER,
  played_at_ms INTEGER,
  finished_at_ms INTEGER,
  removed_at_ms INTEGER
);

CREATE INDEX party_entry_session_state ON party_entry (session_id, state);
CREATE INDEX party_entry_participant ON party_entry (participant_id);

CREATE TABLE party_screen (
  screen_id TEXT NOT NULL PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES party_session (session_id) ON DELETE CASCADE,
  profile_id TEXT NOT NULL,
  name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('popout', 'joined')),
  carrier TEXT NOT NULL CHECK (carrier IN ('local', 'relay')),
  ticker INTEGER NOT NULL DEFAULT 1 CHECK (ticker IN (0, 1)),
  offset_ms INTEGER NOT NULL DEFAULT 0,
  rtt_ms INTEGER NOT NULL DEFAULT 0 CHECK (rtt_ms >= 0),
  registered_at_ms INTEGER NOT NULL,
  reported_at_ms INTEGER,
  dropped_at_ms INTEGER
);

CREATE INDEX party_screen_session ON party_screen (session_id);
CREATE INDEX party_screen_profile ON party_screen (profile_id);
