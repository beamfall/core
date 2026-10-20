package scan

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

// NewJob builds the durable store.Job for req, deriving a dedupe key from its
// Library and root (backfills get a distinct key so they never suppress, or
// are suppressed by, a queued normal scan of the same root).
func NewJob(req JobRequest) (store.Job, error) {
	if req.LibraryID == "" {
		return store.Job{}, errors.New("library ID is required")
	}
	if req.Root == "" {
		return store.Job{}, errors.New("scan root is required")
	}
	root, err := normalizeRoot(req.Root)
	if err != nil {
		return store.Job{}, err
	}
	payload, err := json.Marshal(jobPayload{
		Version:   2,
		LibraryID: req.LibraryID,
		Root:      root,
		Backfill:  req.Backfill,
	})
	if err != nil {
		return store.Job{}, err
	}
	// A backfill is distinct work from a normal scan of the same root, so it
	// gets its own dedupe key — a queued backfill never suppresses (or is
	// suppressed by) a queued normal scan, and two backfills still dedupe.
	dedupeKey := string(req.LibraryID) + ":" + root
	if req.Backfill {
		dedupeKey += ":backfill"
	}
	return store.Job{
		ID:        req.ID,
		Kind:      JobKind,
		Class:     store.JobClassBackground,
		DedupeKey: dedupeKey,
		Payload:   payload,
		CreatedAt: req.CreatedAt,
	}, nil
}

// Handler adapts Scan to the job substrate. grabber powers perceptual
// hashing; nil (or a grab failure) just leaves phash empty — the same
// degrade-gracefully posture as a nil Prober. parsers maps a Library's plugin
// ID to its FilenameParser; a nil/empty map, or a Library whose
// plugin ships no parser, scans with the unchanged titleFromPath behaviour.
// interactiveSidecarExts are the lowercase extensions (no dot) scan recognises
// as INTERACTIVE-control sidecar tracks: a sibling file with one
// of these extensions is recorded as a store sidecar of the interactive kind,
// served behind a Stream Token as raw bytes. The set is plugin-declared data
// (plugin.Registry.InteractiveSidecarExts) threaded in like parsers; Core never
// names an extension. Nil/empty recognises only the Core caption sidecars.
func Handler(st *store.Store, prober Prober, grabber FrameGrabber, parsers map[string]FilenameParser, interactiveSidecarExts []string, identifier Identifier) jobs.Handler {
	return HandlerWithLocalExtractors(st, prober, grabber, parsers, interactiveSidecarExts, identifier, nil)
}

// HandlerWithLocalExtractors extends Handler with the LocalExtractor seam:
// localExtractors maps a Library's plugin ID to its local-file extractors;
// a nil/empty map imports unchanged.
func HandlerWithLocalExtractors(st *store.Store, prober Prober, grabber FrameGrabber, parsers map[string]FilenameParser, interactiveSidecarExts []string, identifier Identifier, localExtractors map[string][]LocalExtractor) jobs.Handler {
	return HandlerWithMetadataHydration(st, prober, grabber, parsers, interactiveSidecarExts, identifier, localExtractors, nil)
}

// HandlerWithMetadataHydration extends HandlerWithLocalExtractors with the
// plugin-neutral local metadata hook. Hydrators are selected by Library plugin
// ID and declare the Media classes they support; absent contributions import
// unchanged.
func HandlerWithMetadataHydration(st *store.Store, prober Prober, grabber FrameGrabber, parsers map[string]FilenameParser, interactiveSidecarExts []string, identifier Identifier, localExtractors map[string][]LocalExtractor, metadataHydrators map[string]MetadataHydrator) jobs.Handler {
	return HandlerWithWorkerGovernor(st, prober, grabber, parsers, interactiveSidecarExts, identifier, localExtractors, metadataHydrators, nil)
}

// HandlerWithWorkerGovernor extends HandlerWithMetadataHydration with the
// worker-pool governor seam: gov sizes the bounded pool that
// fans the per-file fingerprint/OSHash/probe/perceptual-hash work across
// workers (ADR-0013 §2 worker-pool sizing), while every store write and all
// order-sensitive state stay strictly in walk order, so checkpoints, part
// grouping, and crash-resume semantics are unchanged. A nil gov (the default
// for every earlier constructor) scans with the historical fully-sequential
// single-worker walk.
func HandlerWithWorkerGovernor(st *store.Store, prober Prober, grabber FrameGrabber, parsers map[string]FilenameParser, interactiveSidecarExts []string, identifier Identifier, localExtractors map[string][]LocalExtractor, metadataHydrators map[string]MetadataHydrator, gov WorkerGovernor, imageDerivers ...ImageDeriver) jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		req, err := requestFromJobPayload(job.Payload)
		if err != nil {
			return jobs.Result{}, err
		}
		return runScanJob(ctx, st, prober, grabber, parsers, interactiveSidecarExts, identifier, localExtractors, metadataHydrators, gov, firstImageDeriver(imageDerivers), job, req)
	}
}

// Scan walks a Library root and imports regular files by content fingerprint.
func Scan(ctx context.Context, st *store.Store, prober Prober, grabber FrameGrabber, parsers map[string]FilenameParser, interactiveSidecarExts []string, identifier Identifier, req Request) (Result, error) {
	return ScanWithLocalExtractors(ctx, st, prober, grabber, parsers, interactiveSidecarExts, identifier, nil, req)
}

// ScanWithLocalExtractors is Scan plus the LocalExtractor seam; see
// HandlerWithLocalExtractors.
func ScanWithLocalExtractors(ctx context.Context, st *store.Store, prober Prober, grabber FrameGrabber, parsers map[string]FilenameParser, interactiveSidecarExts []string, identifier Identifier, localExtractors map[string][]LocalExtractor, req Request) (Result, error) {
	return ScanWithMetadataHydration(ctx, st, prober, grabber, parsers, interactiveSidecarExts, identifier, localExtractors, nil, req)
}

// ScanWithMetadataHydration is ScanWithLocalExtractors plus the plugin-neutral
// local metadata hook; see HandlerWithMetadataHydration.
func ScanWithMetadataHydration(ctx context.Context, st *store.Store, prober Prober, grabber FrameGrabber, parsers map[string]FilenameParser, interactiveSidecarExts []string, identifier Identifier, localExtractors map[string][]LocalExtractor, metadataHydrators map[string]MetadataHydrator, req Request) (Result, error) {
	return ScanWithWorkerGovernor(ctx, st, prober, grabber, parsers, interactiveSidecarExts, identifier, localExtractors, metadataHydrators, nil, req)
}

// ScanWithWorkerGovernor is ScanWithMetadataHydration plus the worker-pool
// governor seam; see HandlerWithWorkerGovernor.
func ScanWithWorkerGovernor(ctx context.Context, st *store.Store, prober Prober, grabber FrameGrabber, parsers map[string]FilenameParser, interactiveSidecarExts []string, identifier Identifier, localExtractors map[string][]LocalExtractor, metadataHydrators map[string]MetadataHydrator, gov WorkerGovernor, req Request, imageDerivers ...ImageDeriver) (Result, error) {
	if st == nil {
		return Result{}, errors.New("store is required")
	}
	if req.LibraryID == "" {
		return Result{}, errors.New("library ID is required")
	}
	if req.Root == "" {
		return Result{}, errors.New("scan root is required")
	}

	return scanFromCheckpoint(ctx, st, prober, grabber, parsers, interactiveSidecarExts, identifier, localExtractors, metadataHydrators, gov, firstImageDeriver(imageDerivers), req, jobCheckpoint{}, time.Now().UTC(), nil)
}

func firstImageDeriver(derivers []ImageDeriver) ImageDeriver {
	if len(derivers) == 0 {
		return nil
	}
	return derivers[0]
}

func runScanJob(ctx context.Context, st *store.Store, prober Prober, grabber FrameGrabber, parsers map[string]FilenameParser, interactiveSidecarExts []string, identifier Identifier, localExtractors map[string][]LocalExtractor, metadataHydrators map[string]MetadataHydrator, gov WorkerGovernor, imageDeriver ImageDeriver, job store.Job, req Request) (jobs.Result, error) {
	checkpoint, err := checkpointFromJob(job.Checkpoint)
	if err != nil {
		return jobs.Result{}, err
	}
	scanStarted := job.StartedAt
	if scanStarted.IsZero() {
		scanStarted = time.Now().UTC()
	}
	var latest []byte
	filesThisRun := 0
	result, err := scanFromCheckpoint(ctx, st, prober, grabber, parsers, interactiveSidecarExts, identifier, localExtractors, metadataHydrators, gov, imageDeriver, req, checkpoint, scanStarted, func(checkpoint jobCheckpoint, progress store.JobProgress) error {
		data, err := marshalScanCheckpoint(checkpoint)
		if err != nil {
			return err
		}
		latest = data
		if err := st.UpdateJobCheckpoint(ctx, job.ID, job.LeaseOwner, data, progress, time.Now().UTC()); err != nil {
			return err
		}
		filesThisRun++
		if filesThisRun >= scanJobFileBatchSize {
			return jobs.ErrYielded
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, jobs.ErrYielded) {
			return jobs.Result{Checkpoint: latest}, jobs.ErrYielded
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			if len(latest) == 0 {
				latest, _ = marshalScanCheckpoint(checkpoint)
			}
			return jobs.Result{Checkpoint: latest, RunAfter: time.Now().UTC()}, jobs.ErrYielded
		}
		return jobs.Result{}, err
	}
	complete := result.checkpoint("complete")
	data, err := marshalScanCheckpoint(complete)
	if err != nil {
		return jobs.Result{}, err
	}
	return jobs.Result{}, st.UpdateJobCheckpoint(ctx, job.ID, job.LeaseOwner, data, store.JobProgress{
		Current: int64(result.FilesSeen),
		Total:   int64(result.totalFiles),
		Unit:    "files",
		Message: "Scan complete",
	}, time.Now().UTC())
}
