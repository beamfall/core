package ffmpeg

import (
	"context"
)

// PrewarmHardwareProbes populates the in-memory tone-map and device-decode
// capability caches asynchronously after startup so the first live playback
// request does not pay cold-start probe latency.
func PrewarmHardwareProbes(ctx context.Context, hw HardwareEncode, policy EncodePolicy) {
	if !ToneMapEnabled() || policy.SoftwareOnly || hw.Accel == "" {
		return
	}
	go func() {
		if ctx.Err() != nil {
			return
		}
		if _, err := currentFFmpegIdentityKey(ctx); err != nil {
			return
		}
		if hw.Accel == HWAccelVAAPI || hw.Accel == HWAccelQSV {
			_ = deviceDecodeCapabilityFor(ctx, hw.Accel, TransferClassPQ)
		}
		_ = toneMapCapabilityFor(ctx, hw.Accel, TransferClassPQ)
		_ = sdrDeviceDecodeCapabilityFor(ctx, hw.Accel, "h264")
		_ = sdrDeviceDecodeCapabilityFor(ctx, hw.Accel, "hevc")
	}()
}
