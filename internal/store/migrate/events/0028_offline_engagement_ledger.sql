-- CORE-3182 (ADR-0157 Amendment B7) — idempotent replay of offline engagement.
--
-- A client playing downloaded Media offline records one event per play session
-- under a random device event ID and replays them when it reconnects. Each
-- applied (Profile, Device, event ID) is recorded here in the same transaction
-- as its playback projection event, so replaying a batch twice changes nothing.
-- The effect itself lives in engagement_event / playback_progress as usual; this
-- table is only the de-duplication ledger, cleared with the Profile's other
-- engagement on erasure and per-Media deletion. Off the entity-query hot path.
CREATE TABLE offline_engagement_event (
  profile_id TEXT NOT NULL,
  device_id TEXT NOT NULL DEFAULT '',
  event_id TEXT NOT NULL,
  media_id TEXT NOT NULL,
  played_at_ms INTEGER NOT NULL,
  applied_at_ms INTEGER NOT NULL,
  PRIMARY KEY (profile_id, device_id, event_id)
);

CREATE INDEX offline_engagement_event_media ON offline_engagement_event (media_id, profile_id);
