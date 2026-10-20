-- Mutation provenance (ADR-0041 §mutated_by): tracks which agent last wrote
-- descriptive metadata (title/summary/year) to a Media row. Values are the
-- MutationSource enum: 'scan' (filename-derived), 'operator' (human
-- identification via identifyApply), 'ai' (future AI auto-tag). NULL means
-- pre-migration rows whose source is unknown; callers treat NULL as 'scan'.
ALTER TABLE media ADD COLUMN mutated_by TEXT;
