-- Home Studio per-Profile and server default template configuration.
CREATE TABLE IF NOT EXISTS home_config (
    scope TEXT NOT NULL,
    scope_id TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    data_json TEXT NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    PRIMARY KEY (scope, scope_id)
);
