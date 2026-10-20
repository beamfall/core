-- Per-Library AI-indexing opt-out (ADR-0041 §enrichment-scope): semantic
-- enrichment is separately installed and opt-in, and the operator can exclude
-- a Library from the Enrichment Plugin's index entirely. Default ON mirrors the
-- preview_clips_enabled toggle (0022): a Library participates in indexing unless
-- the operator opts it out. Plugins read this through Core's scoped index status;
-- they never enumerate Libraries directly.
ALTER TABLE library ADD COLUMN ai_indexing_enabled INTEGER NOT NULL DEFAULT 1;
