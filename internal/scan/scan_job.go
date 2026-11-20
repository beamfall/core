package scan

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"slices"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

// JobPriority places library scans above background sweeps (0) and below
// foreground/playback demand (10), so scans get a worker first (CORE-2571).
const JobPriority = 5

const batchJobPayloadVersion = 3

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
		Priority:  JobPriority,
		DedupeKey: dedupeKey,
		Payload:   payload,
		CreatedAt: req.CreatedAt,
	}, nil
}

// NewBatchJob builds one durable scan job covering all supplied Library roots.
func NewBatchJob(roots []JobRoot, createdAt time.Time) (store.Job, error) {
	if len(roots) == 0 {
		return store.Job{}, errors.New("scan roots are required")
	}
	roots = slices.Clone(roots)
	for i, target := range roots {
		if target.LibraryID == "" || target.Root == "" {
			return store.Job{}, errors.New("library ID and scan root are required")
		}
		root, err := normalizeRoot(target.Root)
		if err != nil {
			return store.Job{}, err
		}
		roots[i].Root = root
	}
	payload, err := json.Marshal(jobPayload{Version: batchJobPayloadVersion, Roots: roots})
	if err != nil {
		return store.Job{}, err
	}
	return store.Job{
		Kind: JobKind, Class: store.JobClassBackground, Priority: JobPriority,
		Payload: payload, CreatedAt: createdAt,
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
		checkpoint, err := checkpointFromJob(job.Checkpoint)
		if err != nil {
			return jobs.Result{}, err
		}
		if len(req.Roots) == 0 {
			return runScanJob(ctx, st, prober, grabber, parsers, interactiveSidecarExts, identifier, localExtractors, metadataHydrators, gov, firstImageDeriver(imageDerivers), job, req.Request, checkpoint)
		}

		// Each claim advances one root using the normal scan checkpoint. A
		// completed root is durable before the job yields or completes.
		index := 0
		if checkpoint.RootIndex != nil {
			index = *checkpoint.RootIndex
			if index < 0 || index >= len(req.Roots) {
				return jobs.Result{}, errors.New("scan checkpoint root is out of range")
			}
			if checkpoint.Stage == "complete" {
				index++
				checkpoint = jobCheckpoint{Stage: "scanning"}
			}
		}
		if index == len(req.Roots) {
			return jobs.Result{}, nil
		}
		checkpoint.RootIndex = &index
		target := req.Roots[index]
		available, err := batchRootAvailable(ctx, st, target)
		if err != nil {
			return jobs.Result{}, err
		}
		var result jobs.Result
		if available {
			root := Request{LibraryID: target.LibraryID, Root: target.Root}
			result, err = runScanJob(ctx, st, prober, grabber, parsers, interactiveSidecarExts, identifier, localExtractors, metadataHydrators, gov, firstImageDeriver(imageDerivers), job, root, checkpoint)
		} else {
			result, err = finishBatchRoot(ctx, st, job, checkpoint)
		}
		if err == nil && index+1 < len(req.Roots) {
			err = jobs.ErrYielded
		}
		return result, err
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

	tally := newIdentifyTally(nil)
	result, err := scanFromCheckpoint(withIdentifyTally(ctx, tally), st, prober, grabber, parsers, interactiveSidecarExts, identifier, localExtractors, metadataHydrators, gov, firstImageDeriver(imageDerivers), req, jobCheckpoint{}, time.Now().UTC(), nil)
	if err != nil {
		return result, err
	}
	result.Unidentified = tally.snapshot()
	logIdentifyOutcomes(ctx, req.LibraryID, result.Unidentified)
	return result, nil
}

func firstImageDeriver(derivers []ImageDeriver) ImageDeriver {
	if len(derivers) == 0 {
		return nil
	}
	return derivers[0]
}

// A queued root may have been removed, trashed, restricted or sealed before
// this claim. Recheck before touching its filesystem.
func batchRootAvailable(ctx context.Context, st *store.Store, target JobRoot) (bool, error) {
	libraries, err := st.Libraries(ctx, store.LibraryFilter{AllowedIDs: []store.LibraryID{target.LibraryID}})
	if err != nil || len(libraries) == 0 {
		return false, err
	}
	if libraries[0].Encrypted && !st.EncryptedLibraryDBRegistered(target.LibraryID) {
		return false, nil
	}
	roots, err := st.LibraryRoots(ctx, target.LibraryID)
	return slices.Contains(roots, target.Root), err
}

func runScanJob(ctx context.Context, st *store.Store, prober Prober, grabber FrameGrabber, parsers map[string]FilenameParser, interactiveSidecarExts []string, identifier Identifier, localExtractors map[string][]LocalExtractor, metadataHydrators map[string]MetadataHydrator, gov WorkerGovernor, imageDeriver ImageDeriver, job store.Job, req Request, checkpoint jobCheckpoint) (jobs.Result, error) {
	// Record a gone or unreadable folder before attempting a walk, so the
	// Console can name the folder that needs attention (CORE-2309).
	if rootErr := checkScanRootReadable(req.Root); rootErr != nil {
		if eventErr := appendScanRootUnavailable(ctx, st, job.ID, req, rootErr); eventErr != nil {
			return jobs.Result{}, errors.Join(rootErr, eventErr)
		}
		if checkpoint.RootIndex != nil {
			// Keep an unavailable root from preventing scans of later roots.
			return finishBatchRoot(ctx, st, job, checkpoint)
		}
		return jobs.Result{}, rootErr
	}
	scanStarted := job.StartedAt
	if scanStarted.IsZero() {
		scanStarted = time.Now().UTC()
	}
	var latest []byte
	filesThisRun := 0
	// The budget counts from the run's first checkpoint, not from the claim:
	// a resumed run walks the whole tree up to its checkpoint again before it
	// reaches new work, and on a large slow share that walk alone outlasted
	// the budget, so every run yielded at its first checkpoint and a scan
	// advanced by a few files per attempt (CORE-2353).
	var workStarted time.Time
	tally := newIdentifyTally(checkpoint.Unidentified)
	result, err := scanFromCheckpoint(withIdentifyTally(ctx, tally), st, prober, grabber, parsers, interactiveSidecarExts, identifier, localExtractors, metadataHydrators, gov, imageDeriver, req, checkpoint, scanStarted, func(current jobCheckpoint, progress store.JobProgress) error {
		current.RootIndex = checkpoint.RootIndex
		current.Unidentified = tally.snapshot()
		data, err := marshalScanCheckpoint(current)
		if err != nil {
			return err
		}
		if err := st.UpdateJobCheckpoint(ctx, job.ID, job.LeaseOwner, data, progress, time.Now().UTC()); err != nil {
			return err
		}
		latest = data
		filesThisRun++
		now := scanJobClock()
		if workStarted.IsZero() {
			workStarted = now
		}
		if filesThisRun >= scanJobFileBatchSize || now.Sub(workStarted) >= scanJobRunBudget {
			return jobs.ErrYielded
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, jobs.ErrYielded) {
			return jobs.Result{Checkpoint: latest}, jobs.ErrYielded
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			if result.interrupted != nil {
				interrupted := *result.interrupted
				interrupted.RootIndex = checkpoint.RootIndex
				interrupted.Unidentified = tally.snapshot()
				latest, _ = marshalScanCheckpoint(interrupted)
			} else if len(latest) == 0 {
				latest, _ = marshalScanCheckpoint(checkpoint)
			}
			return jobs.Result{Checkpoint: latest, RunAfter: time.Now().UTC()}, jobs.ErrYielded
		}
		return jobs.Result{Checkpoint: latest}, err
	}
	result.Unidentified = tally.snapshot()
	complete := result.checkpoint("complete")
	complete.RootIndex = checkpoint.RootIndex
	complete.Unidentified = result.Unidentified
	data, err := marshalScanCheckpoint(complete)
	if err != nil {
		return jobs.Result{Checkpoint: latest}, err
	}
	if err := appendScanSummary(ctx, st, job.ID, result); err != nil {
		return jobs.Result{Checkpoint: latest}, err
	}
	logIdentifyOutcomes(ctx, req.LibraryID, result.Unidentified)
	err = st.UpdateJobCheckpoint(ctx, job.ID, job.LeaseOwner, data, store.JobProgress{
		Current: int64(result.FilesSeen),
		Total:   int64(result.totalFiles),
		Unit:    "files",
		Message: missingCredentialsMessage(result.MissingCredentials, identifier) + unidentifiableMessage(result.Unidentified),
	}, time.Now().UTC())
	if err != nil {
		return jobs.Result{Checkpoint: latest}, err
	}
	return jobs.Result{Checkpoint: data}, nil
}

func finishBatchRoot(ctx context.Context, st *store.Store, job store.Job, checkpoint jobCheckpoint) (jobs.Result, error) {
	checkpoint.Stage = "complete"
	data, err := marshalScanCheckpoint(checkpoint)
	if err != nil {
		return jobs.Result{}, err
	}
	if err := st.UpdateJobCheckpoint(ctx, job.ID, job.LeaseOwner, data, job.Progress, time.Now().UTC()); err != nil {
		return jobs.Result{}, err
	}
	return jobs.Result{Checkpoint: data}, nil
}

// ScanRootUnavailableEvent is the job event type a scan job records when its
// Library folder cannot be scanned (CORE-2309). Its payload is a
// ScanRootUnavailable.
const ScanRootUnavailableEvent = "scan-root-unavailable"

// ScanRootUnavailable names the Library folder a scan could not use and why:
// Reason is "missing" when the folder does not exist or is not a folder, and
// "unreadable" when it exists but cannot be listed.
type ScanRootUnavailable struct {
	LibraryID string `json:"libraryID"`
	Root      string `json:"root"`
	Reason    string `json:"reason"`
}

func appendScanRootUnavailable(ctx context.Context, st *store.Store, id store.JobID, req Request, rootErr error) error {
	reason := "unreadable"
	if errors.Is(rootErr, fs.ErrNotExist) {
		reason = "missing"
	} else if info, err := os.Stat(req.Root); err == nil && !info.IsDir() {
		reason = "missing"
	}
	payload, err := json.Marshal(ScanRootUnavailable{LibraryID: string(req.LibraryID), Root: req.Root, Reason: reason})
	if err != nil {
		return err
	}
	return st.AppendJobEvent(ctx, id, ScanRootUnavailableEvent, payload, time.Now().UTC())
}

// ScanSummaryEvent is the job event type a completed scan job records with
// its ScanSummary payload (CORE-2040).
const ScanSummaryEvent = "scan-summary"

// ScanSummary is what a completed scan did: files checked, catalogued for the
// first time, re-read because they changed on disk and marked missing, and
// items left in the Unmatched queue.
type ScanSummary struct {
	FilesChecked int `json:"filesChecked"`
	Added        int `json:"added"`
	Changed      int `json:"changed"`
	Missing      int `json:"missing"`
	Unmatched    int `json:"unmatched"`
}

func appendScanSummary(ctx context.Context, st *store.Store, id store.JobID, result Result) error {
	payload, err := json.Marshal(ScanSummary{
		FilesChecked: result.FilesSeen,
		Added:        result.FilesAdded,
		Changed:      result.FilesChanged,
		Missing:      result.FilesMissing,
		Unmatched:    result.MediaUnmatched,
	})
	if err != nil {
		return err
	}
	return st.AppendJobEvent(ctx, id, ScanSummaryEvent, payload, time.Now().UTC())
}
