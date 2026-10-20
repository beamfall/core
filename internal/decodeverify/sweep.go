package decodeverify

import (
	"context"
	"encoding/json"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

// SweepJobKind is the durable decode-verify sampling job.
// Lowest job class; the composition root gates it behind the resource governor
// so a realtime transcode preempts it. The weekly Schedule enqueues
// it; the DedupeKey keeps at most one queued/running/yielded, so a slow week of
// Sundays cannot stack jobs.
const SweepJobKind store.JobKind = "decode-verify"

type sweepPayload struct {
	Version int `json:"version"`
}

type sweepCheckpoint struct {
	Version int    `json:"version"`
	LastID  string `json:"last_id,omitempty"`
	Broken  int    `json:"broken"`
	Sound   int    `json:"sound"`
	Failed  int    `json:"failed"`
}

// sweepJobBatchSize bounds the Media decoded per claim so a preempting
// transcode never waits long (ADR-0013 checkpoints). Decode-verify is heavier
// per item than asset generation (it decodes real bytes), so the batch is
// smaller than genassets'.
const sweepJobBatchSize = 3

// NewSweepJob builds the deduped background decode-verify job. The dedupe key
// keeps at most one sweep queued, running, or yielded — re-arming while one is
// active is a no-op (job dedupe), which is exactly the ADR-0032 §5 anti-stack
// guarantee for the weekly Schedule.
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

// SweepHandler returns the job handler that sample-decodes each playable Media
// item's primary file and records the broken/sound verdict, checkpointing by
// Media ID so a preempted sweep resumes where it stopped. A file that decodes
// cleanly clears any prior broken flag (self-healing). A per-item FFmpeg-run
// failure (not a corrupt-file verdict) is counted and skipped rather than
// failing the whole sweep, so one unreadable mount never blocks the rest.
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
		now := func() time.Time { return time.Now().UTC() }
		processed := 0
		for _, id := range ids {
			if ctx.Err() != nil {
				break
			}
			broken, ok, err := service.VerifyMedia(ctx, id, now)
			switch {
			case err != nil:
				// Inability to run FFmpeg or read the store for this item — count
				// and move on; never abort the sweep over one bad item.
				checkpoint.Failed++
			case !ok:
				// Skipped (image-class, or no available file) — not counted as a
				// decode result either way.
			case broken:
				checkpoint.Broken++
			default:
				checkpoint.Sound++
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
			Current: int64(checkpoint.Broken + checkpoint.Sound),
			Unit:    "items",
			Message: "Verifying decode integrity",
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
