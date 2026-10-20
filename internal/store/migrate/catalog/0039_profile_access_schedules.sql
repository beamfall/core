-- Access schedules (ADR-0004 §4 / ADR-0007): server-enforced allowed time
-- windows for managed Profiles. Each row is one weekly window in server-local
-- minutes-from-midnight; weekday follows Go's time.Weekday (0 = Sunday). A
-- Profile with no rows has no schedule and is always allowed (additive
-- default). When rows exist, a session is denied outside every window.
CREATE TABLE IF NOT EXISTS profile_access_schedules (
  profile_id     TEXT NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
  weekday        INTEGER NOT NULL CHECK (weekday BETWEEN 0 AND 6),
  start_minute   INTEGER NOT NULL CHECK (start_minute BETWEEN 0 AND 1439),
  end_minute     INTEGER NOT NULL CHECK (end_minute BETWEEN 1 AND 1440),
  PRIMARY KEY (profile_id, weekday, start_minute, end_minute)
);

CREATE INDEX IF NOT EXISTS profile_access_schedules_profile_idx
  ON profile_access_schedules(profile_id);
