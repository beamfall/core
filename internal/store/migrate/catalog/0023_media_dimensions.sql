-- Probed video frame size (ADR-0010 §8: honest ABR). The transcode ladder
-- only advertises renditions at or below the source height — never
-- upscaling — so the dimensions captured at scan are schema, like the rest
-- of the stream inventory (ADR-0010 §4). 0 = no video stream or unprobed.
ALTER TABLE media ADD COLUMN video_width INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media ADD COLUMN video_height INTEGER NOT NULL DEFAULT 0;
