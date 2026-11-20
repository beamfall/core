package ffmpeg

import (
	"context"
	"os/exec"
	"sync"
)

// sharedResolution starts tool-owned work on the first call. Every caller
// waits independently, including the caller that starts the worker.
type sharedResolution[T any] struct {
	resolve func() (T, error) // optional resolver supplied with a preconfigured entry
	once    sync.Once
	ready   chan struct{}
	value   T
	err     error
}

func (s *sharedResolution[T]) wait(ctx context.Context, resolve func() (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	s.once.Do(func() {
		s.ready = make(chan struct{})
		go func() {
			if s.resolve != nil {
				resolve = s.resolve
			}
			s.value, s.err = resolve()
			close(s.ready)
		}()
	})
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-s.ready:
		return s.value, s.err
	}
}

func (r *Runner) resolveProbe(ctx context.Context) (string, error) {
	return r.ffprobe.wait(ctx, func() (string, error) { return exec.LookPath("ffprobe") })
}
