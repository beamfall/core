package genassets

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store"
)

func TestGenerateTrickplaySkipsUnsuitableSources(t *testing.T) {
	for _, tc := range []struct {
		name, path, container string
		duration              int
		class                 store.MediaClass
		wantCalls             int
	}{
		{name: "short", path: "short.mp4", duration: defaultIntervalSeconds - 1},
		{name: "iso extension", path: "disc.IsO", duration: 120},
		{name: "img extension", path: "disc.ImG", duration: 120},
		{name: "BDMV segment", path: "bDmV/STREAM/clip.m2ts", duration: 120},
		{name: "VIDEO_TS segment", path: "ViDeO_tS/clip.vob", duration: 120},
		{name: "iso container", path: "disc.bin", container: "ISO", duration: 120},
		{name: "non video", path: "audio.mp3", duration: 120, class: store.MediaClassAudio},
		{name: "interval boundary", path: "clip.mp4", duration: defaultIntervalSeconds, wantCalls: 1},
		{name: "segment substring", path: "not_BDMV/VIDEO_TS_extra.mp4", duration: 120, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gen := &fakeGen{sheetCount: 1, sheetHeight: 1800}
			svc, st, _ := newTestService(t, gen)
			path := filepath.Join(t.TempDir(), tc.path)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("trickplay skip fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			class := tc.class
			if class == "" {
				class = store.MediaClassVideo
			}
			container := tc.container
			if container == "" {
				container = "mp4"
			}
			media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
				LibraryID: "library-local", Path: path, Title: tc.name, Container: container,
				Codec: "h264", Class: class, ContentFingerprint: "sha256:trickplay-skip-fixture",
				DurationSecondsHint: tc.duration,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.GenerateTrickplay(context.Background(), media.ID); err != nil {
				t.Fatal(err)
			}
			if gen.spriteCalls != tc.wantCalls {
				t.Fatalf("sprite calls = %d, want %d", gen.spriteCalls, tc.wantCalls)
			}
			if _, ok := svc.TrickplayAsset("sha256:trickplay-skip-fixture"); ok != (tc.wantCalls > 0) {
				t.Fatalf("manifest exists = %v, want %v", ok, tc.wantCalls > 0)
			}
		})
	}
}
