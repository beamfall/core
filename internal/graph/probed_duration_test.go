package graph

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/playback/sessiontest"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

type runtimeProber struct{ playback.Transcoder }

func (runtimeProber) Probe(context.Context, string) (ffmpeg.Probe, error) {
	return ffmpeg.Probe{DurationSeconds: 7152}, nil
}

func TestMediaDurationAfterPlaybackProbe(t *testing.T) {
	for _, name := range []string{"original", "replacement"} {
		t.Run(name, func(t *testing.T) { testMediaDurationAfterPlaybackProbe(t, name == "replacement") })
	}
}

func testMediaDurationAfterPlaybackProbe(t *testing.T, replace bool) {
	t.Helper()
	ctx := t.Context()
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	path := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(path, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := store.SampleMediaFileContentChecked(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: path, Title: "Probe fixture", Container: "mp4",
		Codec: "h264", Class: store.MediaClassVideo, ContentFingerprint: fingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	readDuration := func() int {
		t.Helper()
		var item MediaInfo
		execGraph(t, handler, authorizeViewer, `query($id: String!) { media(id: $id) { id durationSeconds } }`,
			map[string]any{"id": string(media.ID)}, "media", &item)
		if item.ID != string(media.ID) {
			t.Fatalf("GraphQL Media ID = %s, want %s", item.ID, media.ID)
		}
		return item.DurationSeconds
	}
	if got := readDuration(); got != 0 {
		t.Fatalf("GraphQL duration before playback = %d, want 0", got)
	}
	want := 7152
	if replace {
		// Replace the file in place before playback, with no intervening scan.
		replacement := filepath.Join(filepath.Dir(path), "replacement.mp4")
		if err := os.WriteFile(replacement, []byte("change"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, path); err != nil {
			t.Fatal(err)
		}
		want = 0
	}
	scope, err := handler.auth.Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	svc := playback.NewService(st, streamtoken.NewService([]byte("runtime-probe-test"), nil), nil)
	svc.SetSessions(sessiontest.New(t, runtimeProber{}, t.TempDir(), nil))
	info, err := svc.Info(ctx, scope, playback.InfoRequest{MediaID: string(media.ID)})
	if err != nil || info.Timeline.DurationSeconds != 7152 {
		t.Fatalf("playback runtime = %d, %v; want 7152", info.Timeline.DurationSeconds, err)
	}
	if got := readDuration(); got != want {
		t.Fatalf("GraphQL duration after playback = %d, want %d", got, want)
	}
}
