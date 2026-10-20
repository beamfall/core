//go:build io_e2e

package ioe2e

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
)

// hlsArtifacts globs the HLS output directory for the playlist and the fMP4
// media segments the starters emit (index/live.m3u8 + seg%05d.m4s, CMAF — not
// legacy .ts).
func hlsArtifacts(t testing.TB, dir, playlist string) (m3u8 []string, segs []string) {
	t.Helper()
	m3u8, err := filepath.Glob(filepath.Join(dir, playlist))
	if err != nil {
		t.Fatalf("glob %s: %v", playlist, err)
	}
	segs, err = filepath.Glob(filepath.Join(dir, "seg*.m4s"))
	if err != nil {
		t.Fatalf("glob segments: %v", err)
	}
	return m3u8, segs
}

// waitForSegments polls dir until at least one media segment exists or the
// deadline elapses, for the background/streaming starters whose output appears
// asynchronously.
func waitForSegments(t testing.TB, dir string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, segs := hlsArtifacts(t, dir, "*.m3u8"); len(segs) > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestStartHLSJourney transcodes real media to a CMAF HLS rendition and waits
// for it to finish, then asserts the playlist and at least one non-empty media
// segment exist. CopyVideo=false forces a real libx264 transcode so segments
// are produced deterministically regardless of source GOP layout.
//
// io:ffmpeg-start-hls
func TestStartHLSJourney(t *testing.T) {
	r := newRunner(t)
	src := fixtures(t).Path("av_basic")
	dir := t.TempDir()

	proc, err := r.StartHLS(ctx(t), src, dir, ffmpeg.Rendition{Height: 240, VideoKbps: 300})
	if err != nil {
		t.Fatalf("StartHLS(%s): %v", src, err)
	}
	if err := proc.Wait(); err != nil {
		t.Fatalf("HLS transcode Wait: %v", err)
	}

	m3u8, segs := hlsArtifacts(t, dir, "index.m3u8")
	if len(m3u8) == 0 {
		t.Fatalf("no index.m3u8 playlist in %s", dir)
	}
	if len(segs) == 0 {
		t.Fatalf("no .m4s segments in %s", dir)
	}
	requireNonEmptyFile(t, m3u8[0])
	requireNonEmptyFile(t, segs[0])
}

// TestStartBackgroundHLSJourney drives the niced background starter; segments
// appear asynchronously so the journey polls the dir, then stops the process.
//
// io:ffmpeg-start-background-hls
func TestStartBackgroundHLSJourney(t *testing.T) {
	r := newRunner(t)
	src := fixtures(t).Path("av_basic")
	dir := t.TempDir()

	proc, err := r.StartBackgroundHLS(ctx(t), src, dir, ffmpeg.Rendition{Height: 240, VideoKbps: 300})
	if err != nil {
		t.Fatalf("StartBackgroundHLS(%s): %v", src, err)
	}
	defer proc.Stop()

	waitForSegments(t, dir)
	m3u8, segs := hlsArtifacts(t, dir, "index.m3u8")
	if len(m3u8) == 0 {
		t.Fatalf("no index.m3u8 playlist in %s", dir)
	}
	if len(segs) == 0 {
		t.Fatalf("no .m4s segments in %s after polling", dir)
	}
	requireNonEmptyFile(t, segs[0])
}

// TestStartHLSSegmentsJourney drives the absolute-offset segmenter (startSegment
// 0, 2s segments). It writes live.m3u8 plus seg*.m4s; the journey waits for a
// segment, then stops.
//
// io:ffmpeg-start-hls-segments
func TestStartHLSSegmentsJourney(t *testing.T) {
	r := newRunner(t)
	src := fixtures(t).Path("av_basic")
	dir := t.TempDir()

	proc, err := r.StartHLSSegments(ctx(t), src, dir, 0, 2, ffmpeg.Rendition{Height: 240, VideoKbps: 300})
	if err != nil {
		t.Fatalf("StartHLSSegments(%s): %v", src, err)
	}
	defer proc.Stop()

	waitForSegments(t, dir)
	_, segs := hlsArtifacts(t, dir, "live.m3u8")
	if len(segs) == 0 {
		t.Fatalf("no .m4s segments in %s after polling", dir)
	}
	requireNonEmptyFile(t, segs[0])
}
