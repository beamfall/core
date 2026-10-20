-- Scoped-key-authenticated mutations attribute honestly in the append-only
-- activity log (ADR-0012 §11 / ADR-0040 §2.2). api_key_id carries the scoped
-- developer key identifier; token_id remains reserved for ADR-0013 admin API
-- tokens, so the two credential classes stay distinct.
ALTER TABLE activity_entry ADD COLUMN api_key_id TEXT NOT NULL DEFAULT '';
