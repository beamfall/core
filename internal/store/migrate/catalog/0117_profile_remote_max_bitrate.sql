-- Per-Profile remote bitrate cap (CORE-1717), in bits per second. 0 means
-- no per-Profile cap. Playback for a non-LAN request is capped at the
-- lowest non-zero value of this column, the server-wide
-- playback.remote_max_bitrate server_config key and the client's own
-- maxStreamingBitrate; LAN requests ignore it.
ALTER TABLE profile ADD COLUMN remote_max_bitrate INTEGER NOT NULL DEFAULT 0;
