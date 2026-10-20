-- Token-authenticated mutations attribute honestly in the append-only
-- activity log (ADR-0012 §11): token_id carries the API token identifier
-- when a mutation arrived with a Bearer token instead of a session.
ALTER TABLE activity_entry ADD COLUMN token_id TEXT NOT NULL DEFAULT '';
