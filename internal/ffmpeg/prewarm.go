package ffmpeg

import (
	"context"
)

// PrewarmHardwareProbes populates the in-memory tone-map and device-decode
// capability caches asynchronously after startup so the first live playback
// request does not pay cold-start probe latency. All probes run under ctx so a
// server shutdown cancels any in-flight subprocesses cleanly.
func PrewarmHardwareProbes(ctx context.Context, hw HardwareEncode, policy EncodePolicy) {
	if !ToneMapEnabled() || policy.SoftwareOnly || hw.Accel == "" {
		return
	}
	go func() {
		if ctx.Err() != nil {
			return
		}
		identity, err := currentFFmpegIdentityKey(ctx)
		if err != nil || ctx.Err() != nil {
			return
		}
		if hw.Accel == HWAccelVAAPI || hw.Accel == HWAccelQSV {
			prewarmDeviceDecode(ctx, hw.Accel, TransferClassPQ, identity)
		}
		prewarmToneMap(ctx, hw.Accel, TransferClassPQ, identity)
		prewarmSDRDecode(ctx, hw.Accel, "h264", identity)
		prewarmSDRDecode(ctx, hw.Accel, "hevc", identity)
	}()
}

func prewarmDeviceDecode(ctx context.Context, backend HWAccel, class TransferClass, identity string) {
	if ctx.Err() != nil {
		return
	}
	verdict := ProbeDeviceDecode(ctx, DeviceDecodeOptions{
		ToneMapOptions: ToneMapOptions{Backend: backend, Class: class},
	})
	if ctx.Err() != nil {
		return
	}
	key := toneMapVerdictKey{backend: backend, class: class, identity: identity}
	deviceDecodeVerdictMu.Lock()
	entry, cached := deviceDecodeVerdicts[key]
	if !cached {
		entry = &toneMapVerdictEntry{}
		deviceDecodeVerdicts[key] = entry
	}
	deviceDecodeVerdictMu.Unlock()
	_, _ = entry.wait(ctx, func() (ToneMapProbe, error) { return verdict, nil })
}

func prewarmToneMap(ctx context.Context, backend HWAccel, class TransferClass, identity string) {
	if ctx.Err() != nil {
		return
	}
	verdict := ProbeToneMap(ctx, ToneMapOptions{Backend: backend, Class: class})
	if ctx.Err() != nil {
		return
	}
	entry := toneMapVerdictEntryFor(toneMapVerdictKey{backend: backend, class: class, identity: identity})
	_, _ = entry.wait(ctx, func() (ToneMapProbe, error) { return verdict, nil })
}

func prewarmSDRDecode(ctx context.Context, backend HWAccel, codec, identity string) {
	if ctx.Err() != nil {
		return
	}
	ok := ProbeSDRDeviceDecode(ctx, backend, codec)
	if ctx.Err() != nil {
		return
	}
	key := sdrDecodeKey{backend: backend, codec: codec, identity: identity}
	sdrDeviceDecodeMu.Lock()
	entry, cached := sdrDeviceDecodeVerdicts[key]
	if !cached {
		entry = &sharedResolution[bool]{}
		sdrDeviceDecodeVerdicts[key] = entry
	}
	sdrDeviceDecodeMu.Unlock()
	_, _ = entry.wait(ctx, func() (bool, error) { return ok, nil })
}
