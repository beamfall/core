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
		return jobs.Result{Checkpoint: data}, nil
	}
}

// CatchUpJobKind fills missing assets only for Media inserted after a scan's
// watermark. It shares the sweep's background admission and generation policy.
const CatchUpJobKind store.JobKind = "asset-catch-up"

// Version 2 uses durable insertion sequences. Keep the legacy JSON keys so
// queued jobs remain readable; version 1 rowid floors/cursors must restart at 0.
type catchUpPayload struct {
	Generation     int64 `json:"generation,omitempty"`
	Version        int   `json:"version"`
	AfterInsertSeq int64 `json:"after_rowid"`
}

type catchUpCheckpoint struct {
	Generation     int64  `json:"generation,omitempty"`
	AfterInsertSeq *int64 `json:"after_rowid,omitempty"`
	Version        int    `json:"version"`
	LastInsertSeq  int64  `json:"last_rowid"`
	Generated      int    `json:"generated"`
	Failed         int    `json:"failed"`
}

// NewCatchUpJob builds a deduped, non-blocking catch-up for newly inserted Media.
// Enqueue coalesces active catch-ups to the minimum floor, even during a claim.
func NewCatchUpJob(createdAt time.Time, afterInsertSeq int64) (store.Job, error) {
	if afterInsertSeq < 0 {
		return store.Job{}, errors.New("catch-up insertion sequence must be non-negative")
	}
	payload, err := json.Marshal(catchUpPayload{Version: 2, AfterInsertSeq: afterInsertSeq})
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
		if (payload.Version != 1 && payload.Version != 2) || payload.AfterInsertSeq < 0 {
			return jobs.Result{}, errors.New("invalid asset catch-up payload")
		}
		if payload.Version == 1 {
			// Backfill can assign a lower sequence than an old rowid. Revisit all
			// Media; already-generated assets are cheap to check.
			payload.AfterInsertSeq = 0
		}
		checkpoint := catchUpCheckpoint{Version: 2, LastInsertSeq: payload.AfterInsertSeq}
		if len(job.Checkpoint) > 0 {
			if err := json.Unmarshal(job.Checkpoint, &checkpoint); err != nil {
				return jobs.Result{}, err
			}
			if checkpoint.Version != 1 && checkpoint.Version != 2 {
				return jobs.Result{}, errors.New("invalid asset catch-up checkpoint")
			}
			if checkpoint.Version == 1 {
				checkpoint = catchUpCheckpoint{Version: 2, LastInsertSeq: payload.AfterInsertSeq}
			}
			if checkpoint.AfterInsertSeq == nil || *checkpoint.AfterInsertSeq > payload.AfterInsertSeq {
				// This claim must revisit the lower floor, regardless of progress
				// saved by a worker that started before the coalescing enqueue.
				checkpoint.LastInsertSeq = payload.AfterInsertSeq
			}
			if checkpoint.LastInsertSeq < payload.AfterInsertSeq {
				return jobs.Result{}, errors.New("invalid asset catch-up checkpoint")
			}
		}
		checkpoint.AfterInsertSeq = &payload.AfterInsertSeq
		checkpoint.Generation = payload.Generation
		refs, err := service.store.MediaIDsAfterInsertSeq(ctx, checkpoint.LastInsertSeq, sweepJobBatchSize+1)
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
			checkpoint.LastInsertSeq = ref.InsertSeq
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
		return jobs.Result{Checkpoint: data}, nil
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
		// GenerateImageEnrich is deliberately absent: it is an experimental
		// stand-in, not a production enrichment capability (CORE-0700).
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
