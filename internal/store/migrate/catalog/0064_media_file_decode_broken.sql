-- Decode-verify health signal (FEAT-8 / ADR-0009 §9): the decode-verify
-- sampling job sample-decodes each media file's bytes via FFmpeg and flags the
-- ones that fail to decode (corrupt) or end short (truncated). A file whose
-- path still resolves on disk but whose bytes won't decode is "broken" in the
-- generic, domain-agnostic sense — it cannot play — so this signal folds into
-- the existing Library Health `broken` dimension (ADR-0042) alongside the
-- "bytes are gone" (missing_at_ms) case, rather than adding a new dimension word
-- to the fixed five-dimension vocabulary (invariant #1/#3).
--
-- decode_broken is a 0/1 flag on the file row: 1 means the last decode-verify
-- pass over this file's bytes detected a decode error or truncation. It backfills
-- to 0 — never-checked files are presumed sound until the job samples them, the
-- same optimistic default the rest of the health derivation uses. A later pass
-- that decodes cleanly clears it back to 0 (self-healing on re-encode/repair).
ALTER TABLE media_file ADD COLUMN decode_broken INTEGER NOT NULL DEFAULT 0
  CHECK (decode_broken IN (0, 1));

-- decode_checked_at_ms records when the decode-verify job last sampled this
-- file's bytes (epoch ms, server clock). NULL until the first check, so the
-- Console can tell "sound" (checked, decode_broken=0) from "not yet checked"
-- (NULL) without a second table. It is a checked-at timestamp, not a content
-- property, so it lives on the file row next to missing_at_ms, not in the
-- fingerprint-keyed stream inventory.
ALTER TABLE media_file ADD COLUMN decode_checked_at_ms INTEGER;
