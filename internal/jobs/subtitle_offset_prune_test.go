package jobs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type subtitleSidecarGuardStub struct {
	checkSize func(format string, size int64) error
	guard     func(format string, raw []byte) error
}

func (s subtitleSidecarGuardStub) CheckSize(format string, size int64) error {
	if s.checkSize == nil {
		return nil
	}
	return s.checkSize(format, size)
}

func (s subtitleSidecarGuardStub) Guard(format string, raw []byte) error {
	if s.guard == nil {
		return nil
	}
	return s.guard(format, raw)
}

type recordingPruner struct {
	sidecars  []store.LiveSubtitleSidecar
	reachable []string
	cutoff    time.Time
	calls     int
	listErr   error
	pruneErr  error
}

func (p *recordingPruner) LiveSubtitleSidecars(context.Context) ([]store.LiveSubtitleSidecar, error) {
	return p.sidecars, p.listErr
}

func (p *recordingPruner) PruneSubtitleOffsets(_ context.Context, before time.Time, reachable []string) (int64, error) {
	p.calls++
	p.cutoff = before
	p.reachable = reachable
	return 0, p.pruneErr
}

// The sweep reaps at exactly now minus the retention window — the edge the
// policy is stated in, so a drifted constant or a sign error fails here.
func TestSubtitleOffsetPruneHandlerReapsAtTheRetentionEdge(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	pruner := &recordingPruner{}

	if _, err := SubtitleOffsetPruneHandler(pruner, subtitleSidecarGuardStub{}, func() time.Time { return now })(context.Background(), store.Job{}); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if pruner.calls != 1 {
		t.Fatalf("pruner called %d times, want 1", pruner.calls)
	}
	want := now.Add(-store.SubtitleOffsetRetention)
	if !pruner.cutoff.Equal(want) {
		t.Fatalf("cutoff = %s, want %s", pruner.cutoff, want)
	}
	if !pruner.cutoff.Before(now) {
		t.Fatalf("cutoff %s is not in the past; the sweep would reap live rows", pruner.cutoff)
	}
}

// A store failure fails the JOB rather than being swallowed, so a sweep that
// silently stopped reaping surfaces in the job queue instead of leaking rows
// forever.
func TestSubtitleOffsetPruneHandlerSurfacesStoreFailure(t *testing.T) {
	boom := errors.New("catalog is unavailable")
	handler := SubtitleOffsetPruneHandler(&recordingPruner{pruneErr: boom}, subtitleSidecarGuardStub{}, time.Now)

	if _, err := handler(context.Background(), store.Job{}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the store failure", err)
	}
}

// End to end against a REAL store and filesystem: a nudge older than the
// retention window survives solely because its fingerprint is still resolvable
// from a live catalogued sidecar. An equally old orphan is reaped.
func TestSubtitleOffsetPruneSweepSparesResolvableFingerprint(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_800_000_000, 0).UTC()
	longAgo := now.Add(-2 * store.SubtitleOffsetRetention)

	dir := t.TempDir()
	mediaPath := filepath.Join(dir, "item.mkv")
	sidecarPath := filepath.Join(dir, "item.en.srt")
	sidecarBytes := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n")
	if err := os.WriteFile(mediaPath, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecarPath, sidecarBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               mediaPath,
		Title:              "Item",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: "sha256:media",
		Sidecars: []store.MediaFileSidecarCaption{{
			RelPath:  "item.en.srt",
			Kind:     store.SidecarKindCaption,
			Format:   "srt",
			Language: "en",
		}},
	}); err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	reachable, err := store.SubtitleTrackFingerprint(bytes.NewReader(sidecarBytes))
	if err != nil {
		t.Fatal(err)
	}
	for _, fingerprint := range []string{reachable, "sha256:orphaned"} {
		if err := st.PutSubtitleOffset(ctx, "profile-default", fingerprint, 400, longAgo); err != nil {
			t.Fatalf("PutSubtitleOffset(%s): %v", fingerprint, err)
		}
	}

	if _, err := SubtitleOffsetPruneHandler(st, subtitleSidecarGuardStub{}, func() time.Time { return now })(ctx, store.Job{}); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if got, _ := st.SubtitleOffset(ctx, "profile-default", reachable); got != 400 {
		t.Fatalf("reachable nudge = %d, want 400 — the sweep reaped a live fingerprint", got)
	}
	if got, _ := st.SubtitleOffset(ctx, "profile-default", "sha256:orphaned"); got != 0 {
		t.Fatalf("orphaned nudge = %d, want it reaped", got)
	}
}

func TestLiveSidecarPathRejectsSymlinkEscape(t *testing.T) {
	mediaDir := t.TempDir()
	outsideDir := t.TempDir()
	mediaPath := filepath.Join(mediaDir, "item.mkv")
	outsidePath := filepath.Join(outsideDir, "outside.srt")
	if err := os.WriteFile(mediaPath, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsidePath, []byte("subtitle"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(mediaDir, "item.en.srt")
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if path, ok := liveSidecarPath(mediaPath, filepath.Base(linkPath)); ok {
		t.Fatalf("liveSidecarPath = %q, true; want symlink escape rejected", path)
	}
}

func TestReadLiveSubtitleSidecarRejectsOversizeBeforeRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversize.srt")
	f, err := os.Create(path) //nolint:gosec // path is t.TempDir()-rooted, not attacker-controlled
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(2); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	guardCalled := false
	guard := subtitleSidecarGuardStub{
		checkSize: func(_ string, size int64) error {
			if size > 1 {
				return ErrSubtitleSidecarTooLarge
			}
			return nil
		},
		guard: func(_ string, _ []byte) error {
			guardCalled = true
			return nil
		},
	}
	if _, err := readLiveSubtitleSidecar(path, "srt", guard); !errors.Is(err, ErrSubtitleSidecarTooLarge) {
		t.Fatalf("readLiveSubtitleSidecar error = %v, want ErrSubtitleSidecarTooLarge", err)
	}
	if guardCalled {
		t.Fatal("content guard called after the pre-read size check rejected the sidecar")
	}
}
