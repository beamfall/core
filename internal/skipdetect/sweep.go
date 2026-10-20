package skipdetect

import (
	"context"
	"encoding/json"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

// SweepJobKind is the durable skip-segment detection job (ENR-7 / ADR-0048
// Decision 6). Like the asset sweep it is the lowest job class — the
// composition root gates it behind the resource governor so realtime transcode
// PREEMPTS detection — and is enqueued post-scan (debounced) by the
// same scan-completion hook that arms the asset sweep. The dedupe key keeps at
// most one detection sweep queued/running/yielded; re-arming while one is active
// is a no-op.
const SweepJobKind store.JobKind = "skip-detect-sweep"

type sweepPayload struct {
	Version int `json:"version"`
}

type sweepCheckpoint struct {
	Version  int    `json:"version"`
	LastID   string `json:"last_id,omitempty"`
	Detected int    `json:"detected"`
	Failed   int    `json:"failed"`
}

// sweepJobBatchSize bounds Media processed per claim so a preempting transcode
// never waits long (ADR-0013 checkpoints). Detection is heavier per item than
// asset generation (it fingerprints siblings), so the batch is smaller.
const sweepJobBatchSize = 3

// NewSweepJob builds the deduped background detection job. The dedupe key keeps
// a single sweep in flight; the post-scan hook re-arms it and the dedupe
// collapses repeated arms into one run.
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

// SweepHandler returns the job handler that runs skip-segment detection across
// every Media, checkpointing by Media ID so a preempted sweep resumes where it
// stopped. DetectForMedia no-ops on non-video Media and on any Library that has
// not opted into detection (default off), so a sweep over a server with no
// opted-in Library is cheap — it walks IDs and skips them all. Each write is
// idempotent and manual/suppression-respecting, so reruns over unchanged Media
// neither duplicate nor clobber.
func SweepHandler(service *Service) jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		var checkpoint sweepCheckpoint
		if len(job.Checkpoint) > 0 {
			if err := json.Unmarshal(job.Checkpoint, &checkpoint); err != nil {
				return jobs.Result{}, err
			}
		}
		ids, err := service.store.MediaIDsAfter(ctx, store.MediaID(checkpoint.LastID), sweepJobBatchSize)
		if err != nil {
			return jobs.Result{}, err
		}
		processed := 0
		for _, id := range ids {
			if ctx.Err() != nil {
				break
			}
			if err := service.DetectForMedia(ctx, id); err != nil {
				checkpoint.Failed++
			} else {
				checkpoint.Detected++
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
			Current: int64(checkpoint.Detected),
			Unit:    "items",
			Message: "Detecting skip-segments",
		}
		if err := service.store.UpdateJobCheckpoint(ctx, job.ID, job.LeaseOwner, data, progress, time.Now().UTC()); err != nil {
			return jobs.Result{}, err
		}
		if len(ids) == sweepJobBatchSize || ctx.Err() != nil {
			return jobs.Result{Checkpoint: data}, jobs.ErrYielded
		}
		return jobs.Result{}, nil
	}
}
