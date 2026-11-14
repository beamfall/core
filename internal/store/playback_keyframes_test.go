package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestPlaybackKeyframesRoundTripAndGeneration(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const path = "/media/films/long-gop.mkv"

	if _, ok, err := st.PlaybackKeyframes(ctx, path, 100, 1); err != nil || ok {
		t.Fatalf("before put: ok %v err %v", ok, err)
	}
	first := PlaybackKeyframes{
		Path: path, SizeBytes: 100, ModTimeNs: 1,
		Duration:  50*time.Second + 23*time.Millisecond,
		Keyframes: []time.Duration{0, 7 * time.Second, 14*time.Second + 12*time.Microsecond},
	}
	if err := st.PutPlaybackKeyframes(ctx, first); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.PlaybackKeyframes(ctx, path, 100, 1)
	if err != nil || !ok {
		t.Fatalf("after put: ok %v err %v", ok, err)
	}
	if got.Duration != first.Duration || !slices.Equal(got.Keyframes, first.Keyframes) {
		t.Fatalf("round trip = %+v, want %+v", got, first)
	}

	// A different size or modification time is a different file generation.
	for _, gen := range [][2]int64{{101, 1}, {100, 2}} {
		if _, ok, err := st.PlaybackKeyframes(ctx, path, gen[0], gen[1]); err != nil || ok {
			t.Fatalf("generation %v: ok %v err %v, want a miss", gen, ok, err)
		}
	}

	// Re-probing a changed file replaces the row.
	second := PlaybackKeyframes{Path: path, SizeBytes: 200, ModTimeNs: 2, Duration: 10 * time.Second, Keyframes: []time.Duration{0, 5 * time.Second}}
	if err := st.PutPlaybackKeyframes(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.PlaybackKeyframes(ctx, path, 100, 1); ok {
		t.Fatal("old generation still served after replacement")
	}
	got, ok, err = st.PlaybackKeyframes(ctx, path, 200, 2)
	if err != nil || !ok || !slices.Equal(got.Keyframes, second.Keyframes) {
		t.Fatalf("replacement = %+v ok %v err %v", got, ok, err)
	}
}

func TestPutPlaybackKeyframesValidates(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	for name, idx := range map[string]PlaybackKeyframes{
		"no path":       {Duration: time.Second, Keyframes: []time.Duration{0}},
		"no duration":   {Path: "/a", Keyframes: []time.Duration{0}},
		"no keyframes":  {Path: "/a", Duration: time.Second},
		"not ascending": {Path: "/a", Duration: time.Second, Keyframes: []time.Duration{0, 0}},
		"negative":      {Path: "/a", Duration: time.Second, Keyframes: []time.Duration{-time.Second}},
	} {
		if err := st.PutPlaybackKeyframes(ctx, idx); !errors.As(err, new(ValidationError)) {
			t.Errorf("%s: err = %v, want validation error", name, err)
		}
	}
}
