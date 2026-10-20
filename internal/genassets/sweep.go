package genassets

import (
	"context"
	"encoding/json"
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
			failed := false
			for _, generate := range []func(context.Context, store.MediaID) error{
				service.GenerateTrickplay,
				service.GenerateMarkerThumbs,
				service.GeneratePreviewClip,
				service.GenerateWaveform,
				service.GenerateSilenceMap,
				service.GenerateBeatGrid,
				service.GenerateSyncLanes,
				service.GenerateLoudness,
				service.GenerateImageEnrich,
			} {
				if err := generate(ctx, id); err != nil {
					failed = true
				}
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
		if err := service.store.UpdateJobCheckpoint(ctx, job.ID, job.LeaseOwner, data, progress, time.Now().UTC()); err != nil {
			return jobs.Result{}, err
		}
		if len(ids) > processed || ctx.Err() != nil {
			return jobs.Result{Checkpoint: data}, jobs.ErrYielded
		}
		return jobs.Result{}, nil
	}
}
