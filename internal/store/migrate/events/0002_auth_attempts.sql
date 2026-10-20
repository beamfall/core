CREATE TABLE IF NOT EXISTS auth_attempt (
  id INTEGER PRIMARY KEY,
  occurred_at INTEGER NOT NULL,
  action TEXT NOT NULL,
  key TEXT NOT NULL,
  success INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS auth_attempt_action_time_idx ON auth_attempt(action, occurred_at);
