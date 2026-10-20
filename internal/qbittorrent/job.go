package qbittorrent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

// PollJobKind is the durable background job that polls qBittorrent for completed
// downloads and ingests the routable ones. Poll-based v1 is the
// ADR's sanctioned mechanism (§1: the Web API is the completion source of
// truth; watch-folder heuristics are explicitly rejected).
const PollJobKind store.JobKind = "qbittorrent-ingest-poll"

// DefaultPollInterval is the cadence the composition root re-arms the poll job
// at. ADR-0032's Scheduler owns the user-editable cadence later; this is the
// seed value.
const DefaultPollInterval = 5 * time.Minute

type pollPayload struct {
	Version int `json:"version"`
}

type pollCheckpoint struct {
	Version  int `json:"version"`
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
	Unrouted int `json:"unrouted"`
	Errors   int `json:"errors"`
}

// NewPollJob builds the deduped background poll job.
func NewPollJob(createdAt time.Time) (store.Job, error) {
	payload, err := json.Marshal(pollPayload{Version: 1})
	if err != nil {
		return store.Job{}, err
	}
	return store.Job{
		Kind:      PollJobKind,
		Class:     store.JobClassBackground,
		DedupeKey: string(PollJobKind),
		Payload:   payload,
		CreatedAt: createdAt,
	}, nil
}

// Handler returns the poll job handler. One full ingest pass per claim: the
// pass is internally idempotent (processed-hash ledger), so a re-run never
// double-imports and the job needs no fine-grained checkpoint. A qBittorrent
// outage surfaces as a job error and rides the substrate's retry-with-backoff
// (§5). When ingestor is nil (qBittorrent not configured) the job is a no-op.
func Handler(ingestor *Ingestor) jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		if ingestor == nil {
			return jobs.Result{}, nil
		}
		result, err := ingestor.Run(ctx)
		if err != nil {
			return jobs.Result{}, err
		}
		checkpoint, marshalErr := json.Marshal(pollCheckpoint{
			Version:  1,
			Imported: result.Imported,
			Skipped:  result.Skipped,
			Unrouted: result.Unrouted,
			Errors:   result.Errors,
		})
		if marshalErr != nil {
			return jobs.Result{}, marshalErr
		}
		_ = job
		return jobs.Result{Checkpoint: checkpoint}, nil
	}
}
