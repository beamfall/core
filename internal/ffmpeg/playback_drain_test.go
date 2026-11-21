// The tone-map conformance binary is built with this tag so its closure stays
// internal/ffmpeg alone (tools/gate/gate_scope_test.sh); this playback-owner
// test still runs in every ordinary go test.

//go:build !tonemap_conformance

package ffmpeg_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/playback/sessiontest"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

type lookupWait struct {
	context.Context
	remaining atomic.Int32
	waiting   chan struct{}
}

func (c *lookupWait) Done() <-chan struct{} {
	if c.remaining.Add(-1) == 0 {
		close(c.waiting)
	}
	return c.Context.Done()
}

func observeLookup(ctx context.Context, waiting chan struct{}, calls int32) context.Context {
	w := &lookupWait{Context: ctx, waiting: waiting}
	w.remaining.Store(calls)
	return w
}

func drainTestWait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for playback ownership")
		var zero T
		return zero
	}
}

func preparedArtifact(ctx context.Context, s *playback.Sessions, library store.LibraryID, src string, copyVideo bool, name string) error {
	key := playback.SessionKey{SessionID: store.SessionID(library)}
	artifact, err := s.LiveArtifactShaped(ctx, key, src, 120, 1280, 720, copyVideo, false, playback.LadderCapsFromGrant(streamtoken.Grant{}),
		playback.VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, playback.StreamShape{LibraryID: library}, playback.PriorityNormal, name)
	if artifact != nil {
		artifact.Release()
	}
	return err
}

type sharedPreparationTranscoder struct {
	*ffmpeg.Runner
	resolve func(context.Context, string) bool
	starts  atomic.Int32
}

func (f *sharedPreparationTranscoder) StartHLSSegments(ctx context.Context, src, dir string, _ int, _ int, _ ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	if !f.resolve(ctx, src) {
		return nil, ctx.Err()
	}
	f.starts.Add(1)
	return &ffmpeg.HLSProcess{}, os.WriteFile(filepath.Join(dir, "init.mp4"), []byte("init"), 0o600)
}

func TestT4DrainLetsSharedEncoderPreparationContinue(t *testing.T) {
	for _, name := range []string{"identity", "tone map", "device decode", "component lists", "redirect option"} {
		t.Run(name, func(t *testing.T) {
			call, entered, release, waitCount := ffmpeg.PlaybackResolutionFixtureForTest(t, name)
			aWaiting := make(chan struct{})
			dir := t.TempDir()
			aSrc, bSrc := filepath.Join(dir, "A"), filepath.Join(dir, "B")
			for _, src := range []string{aSrc, bSrc} {
				if err := os.WriteFile(src, []byte("source"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			f := &sharedPreparationTranscoder{Runner: ffmpeg.New(), resolve: func(ctx context.Context, src string) bool {
				if src == aSrc {
					ctx = observeLookup(ctx, aWaiting, waitCount)
				}
				return call(ctx)
			}}
			s := sessiontest.New(t, f, t.TempDir(), nil)
			defer release()
			a, b := make(chan error, 1), make(chan error, 1)
			go func() { b <- preparedArtifact(t.Context(), s, "B", bSrc, false, "720p/init.mp4") }()
			drainTestWait(t, entered)
			go func() { a <- preparedArtifact(t.Context(), s, "A", aSrc, false, "720p/init.mp4") }()
			drainTestWait(t, aWaiting)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if _, err := s.Drain(ctx, "A", 0); err != nil {
				t.Fatal(err)
			}
			if err := drainTestWait(t, a); !errors.Is(err, playback.ErrSessionEvicted) || f.starts.Load() != 0 {
				t.Fatalf("A returned %v, starts = %d", err, f.starts.Load())
			}
			select {
			case err := <-b:
				t.Fatalf("B returned while tool preparation was blocked: %v", err)
			default:
			}
			release()
			if err := drainTestWait(t, b); err != nil || f.starts.Load() != 1 {
				t.Fatalf("B could not use the prepared tool: %v, starts = %d", err, f.starts.Load())
			}
		})
	}
}

type sharedProbeTranscoder struct {
	*ffmpeg.Runner
	aSource  string
	aWaiting chan struct{}
}

func (f *sharedProbeTranscoder) Probe(ctx context.Context, src string) (ffmpeg.Probe, error) {
	if src == f.aSource {
		ctx = observeLookup(ctx, f.aWaiting, 1)
	}
	return f.Runner.Probe(ctx, src)
}

func (f *sharedProbeTranscoder) ProbeKeyframes(ctx context.Context, src string) (ffmpeg.KeyframeIndex, error) {
	if src == f.aSource {
		ctx = observeLookup(ctx, f.aWaiting, 1)
	}
	return f.Runner.ProbeKeyframes(ctx, src)
}

func TestT4DrainLetsSharedFFprobeLookupContinue(t *testing.T) {
	for _, keyframesLead := range []bool{false, true} {
		t.Run(fmt.Sprintf("keyframes lead %t", keyframesLead), func(t *testing.T) {
			dir := t.TempDir()
			tool := filepath.Join(dir, "ffprobe")
			paths := map[store.LibraryID]string{"A": filepath.Join(dir, "A"), "B": filepath.Join(dir, "B")}
			for _, path := range paths {
				if err := os.WriteFile(path, []byte("source"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			const script = `#!/bin/sh
kind=duration
for arg; do
	if [ "$arg" = "csv=p=1" ]; then kind=keyframes; fi
	src=$arg
done
: > "$src.started"
if [ "$kind" = "keyframes" ]; then
	printf '%s\n' packet,0,0,K__ packet,4000,4000,K__ stream,1/1000 format,0.000000,120.000000
else
	printf '%s\n' '{"format":{"duration":"120"}}'
fi
`
			if err := os.WriteFile(tool, []byte(script), 0o700); err != nil { // #nosec G306 -- executable test fixture in a private directory.
				t.Fatal(err)
			}
			runner, entered, release := ffmpeg.BlockedPlaybackProbeForTest(t, tool)
			f := &sharedProbeTranscoder{Runner: runner, aSource: paths["A"], aWaiting: make(chan struct{})}
			s := sessiontest.New(t, f, t.TempDir(), nil)
			defer release()
			request := func(library store.LibraryID, keyframes bool, expectDuration int) <-chan error {
				ch := make(chan error, 1)
				src := paths[library]
				go func() {
					if keyframes {
						ch <- preparedArtifact(t.Context(), s, library, src, true, "master.m3u8")
						return
					}
					binding, err := s.BindLive(playback.SessionKey{SessionID: store.SessionID(library)})
					if err != nil {
						ch <- err
						return
					}
					defer binding.Release()
					if err := binding.Attach(library, "", nil); err != nil {
						ch <- err
						return
					}
					if got := binding.ProbedDurationSeconds(t.Context(), "source", src); got != expectDuration {
						ch <- fmt.Errorf("duration = %d, want %d", got, expectDuration)
						return
					}
					ch <- binding.Check()
				}()
				return ch
			}
			b := request("B", keyframesLead, 120)
			drainTestWait(t, entered)
			a := request("A", !keyframesLead, 0)
			drainTestWait(t, f.aWaiting)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if _, err := s.Drain(ctx, "A", 0); err != nil {
				t.Fatal(err)
			}
			if err := drainTestWait(t, a); !errors.Is(err, playback.ErrSessionEvicted) {
				t.Fatalf("A lookup returned %v", err)
			}
			select {
			case err := <-b:
				t.Fatalf("B returned while lookup was blocked: %v", err)
			default:
			}
			release()
			if err := drainTestWait(t, b); err != nil {
				t.Fatalf("B could not use the resolved probe: %v", err)
			}
			if _, err := os.Stat(paths["B"] + ".started"); err != nil {
				t.Fatalf("B's process did not start: %v", err)
			}
			if _, err := os.Stat(paths["A"] + ".started"); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("A started a process despite cutoff: %v", err)
			}
		})
	}
}
