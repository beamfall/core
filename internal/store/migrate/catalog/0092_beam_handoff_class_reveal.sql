-- PLAYREV-6 (ADR-0023 §7): carry the resolved Media class and the offering
-- session's Reveal state on a Beam handoff offer so the claiming device can pick
-- the audio-vs-video surface and pre-prompt for the Reveal PIN before its own
-- playback round-trip resolves. Both are advisory UI hints — the server still
-- re-derives Reveal server-side at claim and never authorizes bytes from these.
-- Named by shape ("video"/"audio"/"image"), never domain (invariant #1).
ALTER TABLE beam_handoff_offer ADD COLUMN media_class TEXT NOT NULL DEFAULT '';
ALTER TABLE beam_handoff_offer ADD COLUMN sender_revealed INTEGER NOT NULL DEFAULT 0;
