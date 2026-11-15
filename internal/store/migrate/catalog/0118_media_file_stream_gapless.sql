-- Gapless facts per audio stream (CORE-1626): the stream's sample rate, the
-- encoder delay (priming samples at the start of the decoded stream) and end
-- padding (samples at its end) the stream declares, and the decoded content
-- length between them. Playback reports them so a client can trim the
-- priming and padding and play consecutive items without a gap. Zero means
-- "not declared"; existing rows stay zero until a rescan re-probes the file.
ALTER TABLE media_file_stream ADD COLUMN sample_rate INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_file_stream ADD COLUMN encoder_delay_samples INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_file_stream ADD COLUMN encoder_padding_samples INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_file_stream ADD COLUMN decoded_samples INTEGER NOT NULL DEFAULT 0;
