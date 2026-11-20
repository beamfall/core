package ffmpeg

import "context"

type playbackSpawnKey struct{}

// WithPlaybackSpawn binds a live launch to a permit and child handoff. Both
// callbacks run synchronously on the starter, without holding an FFmpeg lock.
func WithPlaybackSpawn(ctx context.Context, permit func() error, handoff func(*HLSProcess)) context.Context {
	return context.WithValue(ctx, playbackSpawnKey{}, playbackSpawn{permit, handoff})
}

type playbackSpawn struct {
	permit  func() error
	handoff func(*HLSProcess)
}

func playbackPermit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if claim, ok := ctx.Value(playbackSpawnKey{}).(playbackSpawn); ok {
		return claim.permit()
	}
	return nil
}

type playbackProbeKey struct{}

// WithPlaybackProbe lets the probe supervisor obtain a permit immediately
// before Start, hand the child off immediately after Start, and mark it reaped.
func WithPlaybackProbe(ctx context.Context, permit func() error, handoff func() bool, finish func()) context.Context {
	return context.WithValue(ctx, playbackProbeKey{}, playbackProbe{permit, handoff, finish})
}

type playbackProbe struct {
	permit  func() error
	handoff func() bool
	finish  func()
}
