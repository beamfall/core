-- Probed video keyframe index for audio-copy HLS (CORE-0021, ADR-0010). The
-- audio-copy rung copies the source video, so its segments can only start at
-- source keyframes; the session builds its playlist and restart offsets from
-- this index. A row is a disposable cache of one ffprobe packet scan, keyed by
-- the source path and valid only for the file generation (size and
-- modification time) it was probed from; a changed file is re-probed and its
-- row replaced. keyframes_us is a JSON array of keyframe times in whole
-- microseconds from the container start, ascending.
CREATE TABLE IF NOT EXISTS playback_keyframes (
  path TEXT PRIMARY KEY,
  size_bytes INTEGER NOT NULL,
  mod_time_ns INTEGER NOT NULL,
  duration_us INTEGER NOT NULL,
  keyframes_us TEXT NOT NULL,
  probed_at_ms INTEGER NOT NULL
);
