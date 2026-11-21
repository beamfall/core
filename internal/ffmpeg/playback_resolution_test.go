package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func blockedResolution[T any](t *testing.T, value T) (func() (T, error), <-chan struct{}, func()) {
	t.Helper()
	var once sync.Once
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	resolve := func() (T, error) {
		defer close(finished)
		close(started)
		<-release
		return value, nil
	}
	return resolve, started, func() {
		unblock()
		select {
		case <-started:
			<-finished
		default:
		}
	}
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

// PlaybackResolutionFixtureForTest exposes a blocked tool worker to the
// external playback-owner tests linked into this package's test binary.
func PlaybackResolutionFixtureForTest(t *testing.T, name string) (func(context.Context) bool, <-chan struct{}, func(), int32) {
	t.Helper()
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
	var call func(context.Context) bool
	var entered <-chan struct{}
	var release func()
	waitCount := int32(2) // tool identity, then the capability entry
	switch name {
	case "ffprobe":
		waitCount = 1
		resolve, started, unblock := blockedResolution(t, "verified ffprobe")
		runner := New()
		runner.ffprobe.resolve = resolve
		entered, release = started, unblock
		call = func(ctx context.Context) bool {
			path, err := runner.resolveProbe(ctx)
			return err == nil && path == "verified ffprobe"
		}
	case "identity":
		waitCount = 1
		resolve, started, unblock := blockedResolution(t, identity)
		entered, release = started, unblock
		resolvedIdentity.Lock()
		resolvedIdentity.checking = &sharedResolution[FFmpegIdentity]{resolve: resolve}
		resolvedIdentity.Unlock()
		t.Cleanup(func() {
			resolvedIdentity.Lock()
			resolvedIdentity.checking = readyIdentity
			resolvedIdentity.Unlock()
		})
		call = func(ctx context.Context) bool {
			got, err := ResolveFFmpegIdentity(ctx)
			return err == nil && got == identity
		}
	case "tone map", "device decode":
		resolve, started, unblock := blockedResolution(t, ToneMapProbe{Status: ToneMapWorking})
		entered, release = started, unblock
		mu, entries := &toneMapVerdictMu, toneMapVerdicts
		probe := toneMapCapabilityFor
		if name == "device decode" {
			mu, entries, probe = &deviceDecodeVerdictMu, deviceDecodeVerdicts, deviceDecodeCapabilityFor
		}
		mu.Lock()
		entries[key] = &toneMapVerdictEntry{resolve: resolve}
		mu.Unlock()
		t.Cleanup(func() {
			mu.Lock()
			delete(entries, key)
			mu.Unlock()
		})
		call = func(ctx context.Context) bool { return probe(ctx, "", TransferClassPQ).working() }
	case "component lists":
		resolve, started, unblock := blockedResolution(t, capabilities{})
		entered, release = started, unblock
		capabilitiesMu.Lock()
		capabilitiesByBuild[identity.String()] = &capabilitiesEntry{resolve: resolve}
		capabilitiesMu.Unlock()
		t.Cleanup(func() {
			capabilitiesMu.Lock()
			delete(capabilitiesByBuild, identity.String())
			capabilitiesMu.Unlock()
		})
		call = func(ctx context.Context) bool {
			_, err := capabilitiesForCurrentBuild(ctx)
			return err == nil
		}
	case "redirect option":
		path := filepath.Join(t.TempDir(), "ffmpeg")
		resolve, started, unblock := blockedResolution(t, true)
		entered, release = started, unblock
		maxRedirectsMu.Lock()
		maxRedirectsByTool[path] = &maxRedirectsEntry{resolve: resolve}
		maxRedirectsMu.Unlock()
		t.Cleanup(func() {
			maxRedirectsMu.Lock()
			delete(maxRedirectsByTool, path)
			maxRedirectsMu.Unlock()
		})
		call = func(ctx context.Context) bool { ok, _ := toolHasMaxRedirectsContext(ctx, path); return ok }
	default:
		t.Fatalf("unknown shared resolution %q", name)
	}
	return call, entered, release, waitCount
}

func BlockedPlaybackProbeForTest(t *testing.T, executable string) (*Runner, <-chan struct{}, func()) {
	t.Helper()
	resolve, entered, release := blockedResolution(t, executable)
	runner := New()
	runner.ffprobe.resolve = resolve
	return runner, entered, release
}

func TestT3PlaybackSharedCacheWaitsAreCancelable(t *testing.T) {
	for _, name := range []string{"ffprobe", "identity", "tone map", "device decode", "component lists", "redirect option"} {
		t.Run(name, func(t *testing.T) {
			call, entered, release, waitCount := PlaybackResolutionFixtureForTest(t, name)
			for _, waiter := range []string{"initiator", "follower"} {
				parent, cancel := context.WithCancel(t.Context())
				defer cancel()
				ctx := &resolutionWaitContext{Context: parent, atWait: make(chan struct{})}
				ctx.remaining.Store(waitCount)
				result := make(chan bool, 1)
				go func() { result <- call(ctx) }()
				if waiter == "initiator" {
					select {
					case <-entered:
					case <-time.After(5 * time.Second):
						t.Fatal("initiator did not start the resolution")
					}
				}
				select {
				case <-ctx.atWait:
				case <-time.After(5 * time.Second):
					t.Fatalf("%s did not reach the blocked resolution", waiter)
				}
				cancel()
				select {
				case got := <-result:
					if got {
						t.Fatalf("%s received a result before resolution completed", waiter)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("%s remained joined to tool-owned work", waiter)
				}
			}
			release()
			if !call(t.Context()) {
				t.Fatal("a surviving caller did not receive the published value")
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
	if ok, _ := toolHasMaxRedirectsContext(t.Context(), tool); !ok {
		t.Fatal("a surviving caller did not receive the shared value")
	}
}
