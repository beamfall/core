package playback

import "testing"

func TestRungsForNeverAdvertiseAboveTheSource(t *testing.T) {
	cases := []struct {
		name          string
		width, height int
		wantIDs       []string
	}{
		{"4K source gets the full ladder", 3840, 2160, []string{"1080p", "720p", "480p"}},
		{"1080p source gets the full ladder", 1920, 1080, []string{"1080p", "720p", "480p"}},
		{"720p source never advertises 1080p", 1280, 720, []string{"720p", "480p"}},
		{"480p source gets a single rung", 854, 480, []string{"480p"}},
		{"sub-ladder source gets its own height", 640, 360, []string{"360p"}},
		{"unknown dimensions get the source rung", 0, 0, []string{"source"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rungs := rungsFor(tc.width, tc.height)
			if len(rungs) != len(tc.wantIDs) {
				t.Fatalf("rungsFor(%d, %d) = %+v, want IDs %v", tc.width, tc.height, rungs, tc.wantIDs)
			}
			for i, want := range tc.wantIDs {
				if rungs[i].ID != want {
					t.Fatalf("rung[%d] = %q, want %q", i, rungs[i].ID, want)
				}
				if tc.height > 0 && rungs[i].Height > tc.height {
					t.Fatalf("rung %q height %d exceeds source %d", rungs[i].ID, rungs[i].Height, tc.height)
				}
				if rungs[i].Bandwidth <= 0 || rungs[i].VideoKbps <= 0 {
					t.Fatalf("rung %q missing rate ceilings: %+v", rungs[i].ID, rungs[i])
				}
			}
		})
	}
}

func TestRungsForClientNeverAdvertiseAboveSourceOrClient(t *testing.T) {
	cases := []struct {
		name            string
		width, height   int
		clientMaxHeight int
		wantIDs         []string
	}{
		// No ceiling (0) is exactly the source-only behaviour.
		{"no ceiling on 4K gets the full ladder", 3840, 2160, 0, []string{"1080p", "720p", "480p"}},
		// Client ceiling clips below the source: a 4K source to a 720p client.
		{"720p client on a 4K source drops 1080p", 3840, 2160, 720, []string{"720p", "480p"}},
		// Client ceiling above the source never adds a rung the source can't back.
		{"4K-capable client on a 720p source stays source-bound", 1280, 720, 2160, []string{"720p", "480p"}},
		// Ceiling equal to a ladder rung keeps that rung.
		{"1080p client on a 4K source keeps 1080p", 3840, 2160, 1080, []string{"1080p", "720p", "480p"}},
		// Ceiling below the lowest ladder rung yields a single floor rung at the
		// ceiling, never zero — the client always gets one playable rendition.
		{"360p client on a 1080p source gets one 360p rung", 1920, 1080, 360, []string{"360p"}},
		// Unknown source dimensions still give a single rung, now BOUNDED by the
		// client ceiling rather than the unbounded source rung: the ceiling is
		// the one claim we can honour without measuring the source, and leaving
		// it unapplied is the single place a decode ceiling could fail to reach
		// the encoder. The rung's Height is an upper bound for the encoder only —
		// playbackInfo omits it from the advertised rendition, since the server
		// never measured the source (advertisedRungHeight).
		{"unknown source is bounded by the client ceiling", 0, 0, 720, []string{"720p"}},
		// With no ceiling the unbounded source rung is emitted exactly as before.
		{"unknown source with no ceiling stays the source rung", 0, 0, 0, []string{"source"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rungs := rungsForClient(tc.width, tc.height, tc.clientMaxHeight)
			if len(rungs) != len(tc.wantIDs) {
				t.Fatalf("rungsForClient(%d,%d,%d) = %+v, want IDs %v", tc.width, tc.height, tc.clientMaxHeight, rungs, tc.wantIDs)
			}
			for i, want := range tc.wantIDs {
				if rungs[i].ID != want {
					t.Fatalf("rung[%d] = %q, want %q", i, rungs[i].ID, want)
				}
				if tc.height > 0 && rungs[i].Height > tc.height {
					t.Fatalf("rung %q height %d exceeds source %d", rungs[i].ID, rungs[i].Height, tc.height)
				}
				if tc.clientMaxHeight > 0 && rungs[i].Height > tc.clientMaxHeight {
					t.Fatalf("rung %q height %d exceeds client ceiling %d", rungs[i].ID, rungs[i].Height, tc.clientMaxHeight)
				}
				if rungs[i].Bandwidth <= 0 || rungs[i].VideoKbps <= 0 {
					t.Fatalf("rung %q missing rate ceilings: %+v", rungs[i].ID, rungs[i])
				}
			}
		})
	}
}

func TestRungsForLadderAddsHEVCRungsOnlyWhenAllowed(t *testing.T) {
	rungs := rungsForLadder(3840, 2160, ladderCaps{
		ClientMaxHeight: 1080,
		HEVC:            true,
		HEVCMaxHeight:   2160,
	})
	wantIDs := []string{"2160p-hevc", "1440p-hevc", "1080p", "720p", "480p"}
	if len(rungs) != len(wantIDs) {
		t.Fatalf("rungs = %+v, want IDs %v", rungs, wantIDs)
	}
	for i, want := range wantIDs {
		if rungs[i].ID != want {
			t.Fatalf("rung[%d] = %q, want %q", i, rungs[i].ID, want)
		}
		if rungs[i].Height > 2160 {
			t.Fatalf("rung %q height %d exceeds source", rungs[i].ID, rungs[i].Height)
		}
		if rungs[i].Codec != "hevc" && rungs[i].Height > 1080 {
			t.Fatalf("h264 rung %q exceeded client ceiling: %+v", rungs[i].ID, rungs[i])
		}
		if rungs[i].Bandwidth != bandwidthFor(rungs[i].VideoKbps) {
			t.Fatalf("rung %q bandwidth = %d, want %d", rungs[i].ID, rungs[i].Bandwidth, bandwidthFor(rungs[i].VideoKbps))
		}
	}
}

// TestRungsForIsRungsForClientWithoutCeiling locks the delegation: the byte
// layer's source-only rungsFor stays byte-identical to the no-ceiling client
// call, so the existing callers (and the golden master playlists) don't move.
func TestRungsForIsRungsForClientWithoutCeiling(t *testing.T) {
	for _, dim := range [][2]int{{3840, 2160}, {1920, 1080}, {1280, 720}, {854, 480}, {640, 360}, {0, 0}} {
		a := rungsFor(dim[0], dim[1])
		b := rungsForClient(dim[0], dim[1], 0)
		if len(a) != len(b) {
			t.Fatalf("rungsFor(%v) len %d != rungsForClient len %d", dim, len(a), len(b))
		}
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("rungsFor(%v)[%d] = %+v, rungsForClient = %+v", dim, i, a[i], b[i])
			}
		}
	}
}

func TestRungWidthKeepsAspectAtEvenPixels(t *testing.T) {
	if got := rungWidth(1920, 1080, 480); got != 852 {
		t.Fatalf("rungWidth(1920,1080,480) = %d, want 852", got)
	}
	if got := rungWidth(0, 1080, 480); got != 0 {
		t.Fatalf("rungWidth with unknown source width = %d, want 0", got)
	}
}
