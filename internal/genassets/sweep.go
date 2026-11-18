package genassets

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

// SweepJobKind is the durable job that fills in missing Generated Assets.
// Lowest job class; the composition root gates it behind the resource
// governor so realtime transcode preempts generation.
const SweepJobKind store.JobKind = "asset-sweep"

type sweepPayload struct {
	Version int `json:"version"`
}

type sweepCheckpoint struct {
	Version   int    `json:"version"`
	LastID    string `json:"last_id,omitempty"`
	Generated int    `json:"generated"`
	Failed    int    `json:"failed"`
}

// sweepJobBatchSize bounds work per claim so a preempting transcode never
// waits long (ADR-0013 checkpoints — what makes a 100k-file backfill
// survivable).
const sweepJobBatchSize = 5

// NewSweepJob builds the deduped background job that generates missing
// Generated Assets. The dedupe key keeps at most one sweep queued, running,
// or yielded — re-arming while one is active is a no-op (job dedupe).
func NewSweepJob(createdAt time.Time) (store.Job, error) {
	payload, err := json.Marshal(sweepPayload{Version: 1})
	if err != nil {
		return store.Job{}, err
	}
	return store.Job{
		Kind:      SweepJobKind,
		Class:     store.JobClassBackground,
		DedupeKey: string(SweepJobKind),
		Payload:   payload,
		CreatedAt: createdAt,
	}, nil
}

// SweepHandler returns the job handler that fills in missing trickplay
// sprites, Marker thumbnails, preview clips, audio waveform envelopes, audio
// silence maps, audio beat grids, audio sync lanes, and audio loudness measurements,
// checkpointing by Media ID so
// a preempted sweep resumes where it stopped. Each generator no-ops when its
// asset already exists for the file's content fingerprint, so reruns over
// unchanged files are stat-only.
func SweepHandler(service *Service) jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		var checkpoint sweepCheckpoint
		if len(job.Checkpoint) > 0 {
			if err := json.Unmarshal(job.Checkpoint, &checkpoint); err != nil {
				return jobs.Result{}, err
			}
		}
		ids, err := service.store.MediaIDsAfter(ctx, store.MediaID(checkpoint.LastID), sweepJobBatchSize+1)
		if err != nil {
			return jobs.Result{}, err
		}
		processed := 0
		for _, id := range ids {
			if ctx.Err() != nil {
				break
			}
			failed := generateMediaAssets(ctx, service, id)
			if ctx.Err() != nil {
				break // Retry the interrupted item, including its missing assets.
			}
			if failed {
				checkpoint.Failed++
			} else {
				checkpoint.Generated++
			}
			checkpoint.LastID = string(id)
			processed++
			if processed >= sweepJobBatchSize {
				break
			}
		}
		checkpoint.Version = 1
		data, err := json.Marshal(checkpoint)
		if err != nil {
			return jobs.Result{}, err
		}
		progress := store.JobProgress{
			Current: int64(checkpoint.Generated),
			Unit:    "items",
			Message: "Generating assets",
		}
		if err := service.store.UpdateJobCheckpoint(context.WithoutCancel(ctx), job.ID, job.LeaseOwner, data, progress, time.Now().UTC()); err != nil {
			return jobs.Result{Checkpoint: data}, err
		}
		if len(ids) > processed || ctx.Err() != nil {
			return jobs.Result{Checkpoint: data}, jobs.ErrYielded
		}
		return jobs.Result{}, nil
	}
}

// CatchUpJobKind fills missing assets only for Media inserted after a scan's
// watermark. It shares the sweep's background admission and generation policy.
const CatchUpJobKind store.JobKind = "asset-catch-up"

type catchUpPayload struct {
	Version    int   `json:"version"`
	AfterRowID int64 `json:"after_rowid"`
}

type catchUpCheckpoint struct {
	Version   int   `json:"version"`
	LastRowID int64 `json:"last_rowid"`
	Generated int   `json:"generated"`
	Failed    int   `json:"failed"`
}

// NewCatchUpJob builds a deduped, non-blocking catch-up for newly inserted Media.
// An active catch-up keeps its earlier floor and can visit subsequent inserts.
func NewCatchUpJob(createdAt time.Time, afterRowID int64) (store.Job, error) {
	if afterRowID < 0 {
		return store.Job{}, errors.New("catch-up rowid must be non-negative")
	}
	payload, err := json.Marshal(catchUpPayload{Version: 1, AfterRowID: afterRowID})
	if err != nil {
		return store.Job{}, err
	}
	return store.Job{
		Kind:      CatchUpJobKind,
		Class:     store.JobClassBackground,
		DedupeKey: string(CatchUpJobKind),
		Payload:   payload,
		CreatedAt: createdAt,
	}, nil
}

// CatchUpHandler walks insertion order above the scan's floor, checkpointing
// only completed items so playback or scan preemption can safely resume work.
func CatchUpHandler(service *Service) jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		var payload catchUpPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return jobs.Result{}, err
		}
		if payload.Version != 1 || payload.AfterRowID < 0 {
			return jobs.Result{}, errors.New("invalid asset catch-up payload")
		}
		checkpoint := catchUpCheckpoint{Version: 1, LastRowID: payload.AfterRowID}
		if len(job.Checkpoint) > 0 {
			if err := json.Unmarshal(job.Checkpoint, &checkpoint); err != nil {
				return jobs.Result{}, err
			}
			if checkpoint.Version != 1 || checkpoint.LastRowID < payload.AfterRowID {
				return jobs.Result{}, errors.New("invalid asset catch-up checkpoint")
			}
		}
		refs, err := service.store.MediaIDsAfterRowID(ctx, checkpoint.LastRowID, sweepJobBatchSize+1)
		if err != nil {
			return jobs.Result{}, err
		}
		processed := 0
		for _, ref := range refs {
			if ctx.Err() != nil {
				break
			}
			failed := generateMediaAssets(ctx, service, ref.ID)
			if ctx.Err() != nil {
				break
			}
			if failed {
				checkpoint.Failed++
			} else {
				checkpoint.Generated++
			}
			checkpoint.LastRowID = ref.RowID
			processed++
			if processed >= sweepJobBatchSize {
				break
			}
		}
		data, err := json.Marshal(checkpoint)
		if err != nil {
			return jobs.Result{}, err
		}
		progress := store.JobProgress{
			Current: int64(checkpoint.Generated),
			Unit:    "items",
			Message: "Generating assets for new media",
		}
		if err := service.store.UpdateJobCheckpoint(context.WithoutCancel(ctx), job.ID, job.LeaseOwner, data, progress, time.Now().UTC()); err != nil {
			return jobs.Result{Checkpoint: data}, err
		}
		if len(refs) > processed || ctx.Err() != nil {
			return jobs.Result{Checkpoint: data}, jobs.ErrYielded
		}
		return jobs.Result{}, nil
	}
}

// generateMediaAssets is the shared per-item recipe for full sweeps and catch-up.
// Asset failures do not prevent other asset families or later items from running.
func generateMediaAssets(ctx context.Context, service *Service, id store.MediaID) (failed bool) {
	for _, generate := range []func(context.Context, store.MediaID) error{
		service.GenerateTrickplay,
		service.GenerateMarkerThumbs,
		service.GeneratePreviewClip,
		service.GenerateWaveform,
		service.GenerateSilenceMap,
		service.GenerateLoudness,
		service.GenerateImageEnrich,
	} {
		if ctx.Err() != nil {
			break
		}
		if err := generate(ctx, id); err != nil {
			failed = true
		}
	}
	return failed
}
