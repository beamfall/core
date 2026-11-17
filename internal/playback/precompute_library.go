package playback

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/beamfall/core/internal/store"
)

// PrecomputeLibrarySpec is one run of a Library's rendition precompute
// Schedule (CORE-2326). The Schedule ships disabled; the operator enabling it
// is the explicit trigger ADR-0057 §2 requires, so this never runs on its own.
type PrecomputeLibrarySpec struct {
	LibraryID       store.LibraryID
	RenditionTarget string
	CacheRoot       string
	// BudgetBytes is the rendition cache budget the eviction job also
	// governs. A run adds work only while the ready cache plus every job it
	// has already chosen fits inside it.
	BudgetBytes int64
	// Sealed reports whether an Encrypted Library is Sealed. A nil predicate
	// or a Sealed Library yields no work: the server holds no key for it.
	Sealed func(store.LibraryID) bool
}

// PrecomputeLibraryJobs builds the precompute jobs one Schedule run of a
// Library queues. It skips renditions already ready or generating, Media the
// precompute cannot back (images, refused or dimensionless sources, unknown
// duration), and stops once the next rendition would push the ready cache
// over the budget, so a run never plans more than the cache can hold. The
// jobs are Background class, so they yield to playback like every other
// background job, and each still passes the precompute admission governor
// when it runs.
func PrecomputeLibraryJobs(ctx context.Context, st *store.Store, spec PrecomputeLibrarySpec, now time.Time) ([]store.Job, error) {
	requests, err := precomputeLibraryRequests(ctx, st, spec)
	if err != nil || len(requests) == 0 {
		return nil, err
	}
	return PrecomputeScheduleJobs(now, requests)
}

func precomputeLibraryRequests(ctx context.Context, st *store.Store, spec PrecomputeLibrarySpec) ([]PrecomputeRequest, error) {
	if st == nil {
		return nil, errors.New("store is required")
	}
	if spec.LibraryID == "" {
		return nil, errors.New("library ID is required")
	}
	if spec.BudgetBytes <= 0 {
		return nil, nil
	}
	encrypted, err := st.LibraryEncrypted(ctx, spec.LibraryID)
	if err != nil {
		return nil, err
	}
	if encrypted && (spec.Sealed == nil || spec.Sealed(spec.LibraryID)) {
		return nil, nil
	}
	health, err := st.PlaybackRenditionCacheHealth(ctx)
	if err != nil {
		return nil, err
	}
	planned := health.ReadyBytes
	if planned >= spec.BudgetBytes {
		return nil, nil
	}
	mediaIDs, err := libraryMediaIDs(ctx, st, spec.LibraryID)
	if err != nil {
		return nil, err
	}
	var out []PrecomputeRequest
	for _, mediaID := range mediaIDs {
		media, requests, err := precomputeMediaRequests(ctx, st, mediaID, spec.RenditionTarget, spec.CacheRoot, spec.Sealed)
		var ineligible precomputeIneligible
		if errors.As(err, &ineligible) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, req := range requests {
			estimate, err := precomputeEstimateBytes(media.DurationSeconds, req.Bandwidth)
			if err != nil {
				// Unknown duration: the job could never pass bounded admission.
				break
			}
			pending, err := precomputeRenditionPending(ctx, st, req.RenditionID)
			if err != nil {
				return nil, err
			}
			if pending {
				continue
			}
			if estimate > spec.BudgetBytes-planned {
				return out, nil
			}
			planned += estimate
			out = append(out, req)
		}
	}
	return out, nil
}

// precomputeRenditionPending reports whether a rendition is already ready or
// being generated, so a run does not regenerate it.
func precomputeRenditionPending(ctx context.Context, st *store.Store, id store.PlaybackRenditionID) (bool, error) {
	r, err := st.PlaybackRendition(ctx, id, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if errors.Is(err, store.ErrPlaybackRenditionNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return r.Status == store.PlaybackRenditionReady || r.Status == store.PlaybackRenditionGenerating, nil
}

func libraryMediaIDs(ctx context.Context, st *store.Store, libraryID store.LibraryID) ([]store.MediaID, error) {
	files, err := st.MediaFilesForLibrary(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	seen := map[store.MediaID]bool{}
	out := make([]store.MediaID, 0, len(files))
	for _, file := range files {
		if file.MediaID == "" || seen[file.MediaID] {
			continue
		}
		seen[file.MediaID] = true
		out = append(out, file.MediaID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}
