CREATE TABLE auth_attempt (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  occurred_at_ms INTEGER NOT NULL,
  action TEXT NOT NULL,
  key TEXT NOT NULL,
  success INTEGER NOT NULL
);

CREATE INDEX auth_attempt_lane_time_idx ON auth_attempt(action, key, occurred_at_ms);
