-- CORE-2305 — last playback activity per Stream Token ledger row.
--
-- The live-sessions read listed every unexpired media grant, and grants live
-- for hours, so a closed or crashed player stayed "playing" until its token
-- expired. last_active_ms records when the grant was issued or its session last
-- reported progress for that Media; the live-sessions read drops rows with no
-- recent activity. Existing rows default to 0, so grants issued before this
-- migration read as inactive and stop showing as phantom sessions.
ALTER TABLE stream_token_ledger ADD COLUMN last_active_ms INTEGER NOT NULL DEFAULT 0;
