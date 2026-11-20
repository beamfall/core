package ffmpeg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func blockedResolution[T any](t *testing.T, value T) *sharedResolution[T] {
	t.Helper()
	var releaseOnce sync.Once
	started, release, reaped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		<-reaped
	})
	entry := &sharedResolution[T]{}
	go func() {
		defer close(reaped)
		_, _ = entry.wait(context.Background(), func() (T, error) {
			close(started)
			<-release
			return value, nil
		})
	}()
	<-started
	return entry
}

type resolutionWaitContext struct {
	context.Context
	remaining atomic.Int32
	atWait    chan struct{}
}

func (c *resolutionWaitContext) Done() <-chan struct{} {
	if c.remaining.Add(-1) == 0 {
		close(c.atWait)
	}
	return c.Context.Done()
}

func TestT3PlaybackSharedCacheWaitsAreCancelable(t *testing.T) {
	identity := FFmpegIdentity{SHA256: "t3 fixture", Version: t.Name()}
	readyIdentity := &sharedResolution[FFmpegIdentity]{}
	_, _ = readyIdentity.wait(t.Context(), func() (FFmpegIdentity, error) { return identity, nil })
	resolvedIdentity.Lock()
	prior := resolvedIdentity.checking
	resolvedIdentity.checking = readyIdentity
	resolvedIdentity.Unlock()
	t.Cleanup(func() {
		resolvedIdentity.Lock()
		resolvedIdentity.checking = prior
		resolvedIdentity.Unlock()
	})
	key := toneMapVerdictKey{class: TransferClassPQ, identity: identity.String()}
	for _, name := range []string{"ffprobe", "identity", "tone map", "device decode", "component lists", "redirect option"} {
		t.Run(name, func(t *testing.T) {
			var call func(context.Context)
			switch name {
			case "ffprobe":
				runner := New()
				entered, release := make(chan struct{}), make(chan struct{})
				t.Cleanup(func() { close(release) })
				go func() {
					_, _ = runner.ffprobe.wait(context.Background(), func() (string, error) {
						close(entered)
						<-release
						return "", errors.New("test lookup")
					})
				}()
				<-entered
				src := filepath.Join(t.TempDir(), "source")
				call = func(ctx context.Context) { _, _ = runner.Probe(ctx, src) }
			case "identity":
				entry := blockedResolution(t, identity)
				resolvedIdentity.Lock()
				resolvedIdentity.checking = entry
				resolvedIdentity.Unlock()
				t.Cleanup(func() {
					resolvedIdentity.Lock()
					resolvedIdentity.checking = readyIdentity
					resolvedIdentity.Unlock()
				})
				call = func(ctx context.Context) { _, _ = ResolveFFmpegIdentity(ctx) }
			case "tone map":
				entry := blockedResolution(t, ToneMapProbe{})
				toneMapVerdictMu.Lock()
				toneMapVerdicts[key] = entry
				toneMapVerdictMu.Unlock()
				t.Cleanup(func() {
					toneMapVerdictMu.Lock()
					delete(toneMapVerdicts, key)
					toneMapVerdictMu.Unlock()
				})
				call = func(ctx context.Context) { _ = ToneMapSupportedContext(ctx) }
			case "device decode":
				entry := blockedResolution(t, ToneMapProbe{})
				deviceDecodeVerdictMu.Lock()
				deviceDecodeVerdicts[key] = entry
				deviceDecodeVerdictMu.Unlock()
				t.Cleanup(func() {
					deviceDecodeVerdictMu.Lock()
					delete(deviceDecodeVerdicts, key)
					deviceDecodeVerdictMu.Unlock()
				})
				call = func(ctx context.Context) { _ = deviceDecodeCapabilityFor(ctx, "", TransferClassPQ) }
			case "component lists":
				entry := blockedResolution(t, capabilities{})
				capabilitiesMu.Lock()
				capabilitiesByBuild[identity.String()] = entry
				capabilitiesMu.Unlock()
				t.Cleanup(func() {
					capabilitiesMu.Lock()
					delete(capabilitiesByBuild, identity.String())
					capabilitiesMu.Unlock()
				})
				call = func(ctx context.Context) { _ = AudioAdjustSupportedContext(ctx, Rendition{}) }
			case "redirect option":
				path := filepath.Join(t.TempDir(), "ffmpeg")
				entry := blockedResolution(t, false)
				maxRedirectsMu.Lock()
				maxRedirectsByTool[path] = entry
				maxRedirectsMu.Unlock()
				t.Cleanup(func() {
					maxRedirectsMu.Lock()
					delete(maxRedirectsByTool, path)
					maxRedirectsMu.Unlock()
				})
				call = func(ctx context.Context) { _ = toolHasMaxRedirectsContext(ctx, path) }
			}
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx := &resolutionWaitContext{Context: parent, atWait: make(chan struct{})}
			switch name {
			case "ffprobe", "identity":
				ctx.remaining.Store(1)
			default:
				ctx.remaining.Store(2) // tool identity, then the capability entry
			}
			result := make(chan struct{})
			go func() {
				call(ctx)
				close(result)
			}()
			select {
			case <-ctx.atWait:
			case <-time.After(time.Second):
				t.Fatal("playback did not reach the blocked cache entry")
			}
			cancel()
			select {
			case <-result:
			case <-time.After(time.Second):
				t.Fatal("playback waiter remained joined to tool-owned work")
			}
		})
	}
}

func TestT3LiveEncryptedPreparationCancelsBeforeSpawn(t *testing.T) {
	bin := t.TempDir()
	tool := filepath.Join(bin, "ffmpeg")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- executable test fixture in a private directory.
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	maxRedirectsMu.Lock()
	old := httpProtocolHelp
	httpProtocolHelp = func(context.Context, string) ([]byte, error) {
		close(started)
		<-release
		return []byte("-max_redirects "), nil
	}
	maxRedirectsMu.Unlock()
	t.Cleanup(func() {
		maxRedirectsMu.Lock()
		httpProtocolHelp = old
		maxRedirectsMu.Unlock()
	})

	request, cancel := context.WithCancel(t.Context())
	defer cancel()
	var permits atomic.Int32
	ctx := WithPlaybackSpawn(request, func() error {
		permits.Add(1)
		return context.Canceled
	}, func(*HLSProcess) { t.Error("a child reached handoff") })
	result := make(chan error, 1)
	ep := EncryptedLiveEndpoint{
		Source: "http://127.0.0.1:40000/token/source",
		Output: "http://127.0.0.1:40000/token/out/720p",
	}
	go func() {
		_, err := New().StartEncryptedHLSSegments(ctx, ep, 0, 4, Rendition{})
		result <- err
	}()
	select {
	case <-started:
	case err := <-result:
		t.Fatalf("preparation skipped the redirect probe: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("redirect probe did not start")
	}
	cancel()
	select {
	case err := <-result:
		if err == nil || permits.Load() != 0 {
			t.Fatalf("canceled preparation = %v, permits = %d", err, permits.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("encrypted preparation waited for the shared probe")
	}
	unblock()
	if !toolHasMaxRedirectsContext(t.Context(), tool) {
		t.Fatal("a surviving caller did not receive the shared value")
	}
}
