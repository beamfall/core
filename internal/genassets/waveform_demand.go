package genassets

import (
	"context"
	"sync"
	"time"

	"github.com/beamfall/core/internal/store"
)

// waveformDemandSlots bounds concurrent on-demand waveform generations, so a
// burst of first plays waits on a few FFmpeg runs instead of starting one per
// request.
const waveformDemandSlots = 2

// waveformDemandTimeout bounds one on-demand generation. It runs detached from
// the request, so a client that navigates away does not cancel work another
// request is waiting on.
const waveformDemandTimeout = 2 * time.Minute

type waveformDemand struct {
	mu       sync.Mutex
	inflight map[string]chan struct{}
	slots    chan struct{}
}

// EnsureWaveform generates the waveform envelope for an audio Media now, when
// the background sweep has not reached it yet (CORE-2401). Calls for the same
// content fingerprint share one generation. It returns once the generation
// finished or ctx ended; the caller re-checks AssetPath, so a failed or
// skipped generation (non-audio, sealed, no source) reads as a missing asset.
func (s *Service) EnsureWaveform(ctx context.Context, id store.MediaID, fingerprint string) {
	if fingerprint == "" {
		return
	}
	if _, ok := s.WaveformAsset(fingerprint); ok {
		return
	}
	d := &s.waveformDemand
	d.mu.Lock()
	if d.inflight == nil {
		d.inflight = map[string]chan struct{}{}
		d.slots = make(chan struct{}, waveformDemandSlots)
	}
	done, running := d.inflight[fingerprint]
	if !running {
		done = make(chan struct{})
		d.inflight[fingerprint] = done
		go s.runWaveformDemand(context.WithoutCancel(ctx), id, fingerprint, done)
	}
	d.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (s *Service) runWaveformDemand(ctx context.Context, id store.MediaID, fingerprint string, done chan struct{}) {
	d := &s.waveformDemand
	defer func() {
		d.mu.Lock()
		delete(d.inflight, fingerprint)
		d.mu.Unlock()
		close(done)
	}()
	ctx, cancel := context.WithTimeout(ctx, waveformDemandTimeout)
	defer cancel()
	select {
	case d.slots <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-d.slots }()
	_ = s.GenerateWaveform(ctx, id)
}
