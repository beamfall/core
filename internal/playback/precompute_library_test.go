package playback

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const unlimitedPrecomputeBudget = int64(1) << 50

func precomputeLibraryFixture(t *testing.T) (*store.Store, []store.MediaID) {
	t.Helper()
	st := storetest.Open(t)
	first := importProbedMedia(t, st, "fp-library-precompute-a", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	second := importProbedMedia(t, st, "fp-library-precompute-b", "mkv", "hevc", 1280, 720, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	return st, []store.MediaID{first, second}
}

// TestPrecomputeLibraryJobsQueuesTheLibrarysUnbuiltRenditions pins CORE-2326:
// an enabled Library precompute Schedule queues a Background job per honest
// ladder rung of every Media in the Library, and skips renditions already
// ready.
func TestPrecomputeLibraryJobsQueuesTheLibrarysUnbuiltRenditions(t *testing.T) {
	ctx := context.Background()
	st, media := precomputeLibraryFixture(t)
	cacheRoot := t.TempDir()
	now := time.Unix(900, 0).UTC()
	spec := PrecomputeLibrarySpec{LibraryID: "library-local", RenditionTarget: "h264", CacheRoot: cacheRoot, BudgetBytes: unlimitedPrecomputeBudget}

	jobs, err := PrecomputeLibraryJobs(ctx, st, spec, now)
	if err != nil {
		t.Fatalf("PrecomputeLibraryJobs: %v", err)
	}
	// 1080p source backs 1080p/720p/480p; 720p source backs 720p/480p.
	if len(jobs) != 5 {
		t.Fatalf("queued %d jobs, want 5 ladder rungs across the Library", len(jobs))
	}
	for _, job := range jobs {
		if job.Kind != PrecomputeJobKind || job.Class != store.JobClassBackground {
			t.Fatalf("job kind/class = %q/%q, want precompute/background", job.Kind, job.Class)
		}
	}

	readyID := PrecomputeRenditionID(media[0], "fp-library-precompute-a", "720p")
	if err := st.UpsertPlaybackRendition(ctx, store.PlaybackRendition{
		ID: readyID, MediaID: media[0], ContentFingerprint: "fp-library-precompute-a",
		Kind: store.PlaybackRenditionKindHLS, Status: store.PlaybackRenditionReady,
		RungID: "720p", Codec: "h264", Height: 720, Bandwidth: 4_256_000,
		Path: filepath.Join(cacheRoot, string(readyID)), SizeBytes: 4096, CreatedAt: now, LastUsedAt: now,
	}); err != nil {
		t.Fatalf("seed ready rendition: %v", err)
	}
	requests, err := precomputeLibraryRequests(ctx, st, spec)
	if err != nil {
		t.Fatalf("precomputeLibraryRequests: %v", err)
	}
	if len(requests) != 4 {
		t.Fatalf("queued %d requests after one rendition is ready, want 4", len(requests))
	}
	for _, req := range requests {
		if req.RenditionID == readyID {
			t.Fatal("schedule re-queued a ready rendition")
		}
	}
}

// TestPrecomputeLibraryJobsStaysInsideTheCacheBudget pins that a run plans
// only what fits in the budget after the ready cache, and nothing once the
// ready cache already fills it.
func TestPrecomputeLibraryJobsStaysInsideTheCacheBudget(t *testing.T) {
	ctx := context.Background()
	st, media := precomputeLibraryFixture(t)
	cacheRoot := t.TempDir()
	now := time.Unix(900, 0).UTC()
	const readyBytes = 1 << 20
	readyID := PrecomputeRenditionID(media[1], "fp-library-precompute-b", "720p")
	if err := st.UpsertPlaybackRendition(ctx, store.PlaybackRendition{
		ID: readyID, MediaID: media[1], ContentFingerprint: "fp-library-precompute-b",
		Kind: store.PlaybackRenditionKindHLS, Status: store.PlaybackRenditionReady,
		RungID: "720p", Codec: "h264", Height: 720, Bandwidth: 4_256_000,
		Path: filepath.Join(cacheRoot, string(readyID)), SizeBytes: readyBytes, CreatedAt: now, LastUsedAt: now,
	}); err != nil {
		t.Fatalf("seed ready rendition: %v", err)
	}
	all, err := precomputeLibraryRequests(ctx, st, PrecomputeLibrarySpec{LibraryID: "library-local", CacheRoot: cacheRoot, BudgetBytes: unlimitedPrecomputeBudget})
	if err != nil || len(all) < 3 {
		t.Fatalf("unbounded requests = %d, %v; want at least 3", len(all), err)
	}
	var twoFit int64 = readyBytes
	for _, req := range all[:2] {
		estimate, err := precomputeEstimateBytes(120, req.Bandwidth)
		if err != nil {
			t.Fatal(err)
		}
		twoFit += estimate
	}

	bounded, err := precomputeLibraryRequests(ctx, st, PrecomputeLibrarySpec{LibraryID: "library-local", CacheRoot: cacheRoot, BudgetBytes: twoFit})
	if err != nil {
		t.Fatalf("bounded requests: %v", err)
	}
	if len(bounded) != 2 || bounded[0].RenditionID != all[0].RenditionID || bounded[1].RenditionID != all[1].RenditionID {
		t.Fatalf("bounded requests = %d, want the first 2 that fit the budget", len(bounded))
	}

	full, err := PrecomputeLibraryJobs(ctx, st, PrecomputeLibrarySpec{LibraryID: "library-local", CacheRoot: cacheRoot, BudgetBytes: readyBytes}, now)
	if err != nil || len(full) != 0 {
		t.Fatalf("jobs with a full cache = %d, %v; want none", len(full), err)
	}
	none, err := PrecomputeLibraryJobs(ctx, st, PrecomputeLibrarySpec{LibraryID: "library-local", CacheRoot: cacheRoot}, now)
	if err != nil || len(none) != 0 {
		t.Fatalf("jobs with no budget = %d, %v; want none", len(none), err)
	}
}

// TestPrecomputeLibraryJobsSkipsEncryptedLibraryWithoutTheKey pins that an
// Encrypted Library yields no work unless the server can generate on its
// encrypted plane and the Library is Unsealed.
func TestPrecomputeLibraryJobsSkipsEncryptedLibraryWithoutTheKey(t *testing.T) {
	ctx := context.Background()
	st, _ := precomputeLibraryFixture(t)
	if err := st.SetLibraryBacking(ctx, "library-local", false, true); err != nil {
		t.Fatalf("SetLibraryBacking encrypted: %v", err)
	}
	cacheRoot := t.TempDir()
	now := time.Unix(900, 0).UTC()
	for name, sealed := range map[string]func(store.LibraryID) bool{
		"no encrypted plane": nil,
		"sealed":             func(store.LibraryID) bool { return true },
	} {
		jobs, err := PrecomputeLibraryJobs(ctx, st, PrecomputeLibrarySpec{LibraryID: "library-local", CacheRoot: cacheRoot, BudgetBytes: unlimitedPrecomputeBudget, Sealed: sealed}, now)
		if err != nil {
			t.Fatalf("%s: PrecomputeLibraryJobs: %v", name, err)
		}
		if len(jobs) != 0 {
			t.Fatalf("%s: queued %d jobs for an Encrypted Library without its key", name, len(jobs))
		}
	}
}
