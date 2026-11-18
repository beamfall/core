package identify

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

// RefreshJobKind is the durable Library-wide metadata refresh: Refresh for
// every live Media in one Library. It is a network-bound background job, so
// the composition root does not put it behind the resource governor.
const RefreshJobKind store.JobKind = "library-metadata-refresh"

// RefreshJobPace is the production delay between two items' provider lookups.
// It bounds the request rate a Library-wide refresh sends to one provider on
// top of the scraper's own 429 back-off.
const RefreshJobPace = 250 * time.Millisecond

// refreshJobBatchSize bounds the items refreshed per claim, so the job yields
// to the scheduler (and checkpoints) every few seconds.
const refreshJobBatchSize = 10

// RefreshJobProgressMessage is the progress message the job reports; the
// Console maps it to a localized label.
const RefreshJobProgressMessage = "Refreshing metadata"

type refreshPayload struct {
	Version   int    `json:"version"`
	LibraryID string `json:"library_id"`
}

type refreshCheckpoint struct {
	Version   int    `json:"version"`
	LastID    string `json:"last_id,omitempty"`
	Total     int    `json:"total"`
	Refreshed int    `json:"refreshed"`
	Skipped   int    `json:"skipped"`
	Failed    int    `json:"failed"`
}

func (c refreshCheckpoint) processed() int { return c.Refreshed + c.Skipped + c.Failed }

// RefreshJobStore is the store surface the refresh job reads and checkpoints.
type RefreshJobStore interface {
	MediaIDsForLibrary(ctx context.Context, libraryID store.LibraryID) ([]store.MediaID, error)
	LibraryMediaIDsAfter(ctx context.Context, libraryID store.LibraryID, after store.MediaID, limit int) ([]store.MediaID, error)
	UpdateJobCheckpoint(ctx context.Context, id store.JobID, leaseOwner string, checkpoint []byte, progress store.JobProgress, now time.Time) error
	RenewJobLease(ctx context.Context, id store.JobID, leaseOwner string, now time.Time) (time.Time, error)
}

// NewRefreshJob builds the background refresh job for one Library. The dedupe
// key is the Library id, so a second request while one is queued, running or
// yielded returns the active job instead of stacking another.
func NewRefreshJob(libraryID store.LibraryID, createdAt time.Time) (store.Job, error) {
	if libraryID == "" {
		return store.Job{}, errors.New("identify: library ID is required")
	}
	payload, err := json.Marshal(refreshPayload{Version: 1, LibraryID: string(libraryID)})
	if err != nil {
		return store.Job{}, err
	}
	return store.Job{
		Kind:      RefreshJobKind,
		Class:     store.JobClassBackground,
		DedupeKey: string(libraryID),
		Payload:   payload,
		CreatedAt: createdAt,
	}, nil
}

// RefreshJobHandler returns the handler that refreshes a batch of the
// Library's live Media per claim, in Media id order, waiting pace between
// items. An item with nothing to refresh or whose scraper is gone is skipped;
// any other per-item error is counted as failed and the walk goes on. Progress
// and the checkpoint land after each batch. The lease is checked before every
// item, so a cancel stops the walk before the next provider call instead of
// waiting for the registry's renewal tick.
func RefreshJobHandler(st RefreshJobStore, service *Service, pace time.Duration) jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		var payload refreshPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return jobs.Result{}, err
		}
		libraryID := store.LibraryID(payload.LibraryID)
		var checkpoint refreshCheckpoint
		if len(job.Checkpoint) > 0 {
			if err := json.Unmarshal(job.Checkpoint, &checkpoint); err != nil {
				return jobs.Result{}, err
			}
		}
		// A fresh job's stored checkpoint is "{}" (version 0): count the
		// Library's live items once, for progress.
		if checkpoint.Version == 0 {
			all, err := st.MediaIDsForLibrary(ctx, libraryID)
			if err != nil {
				return jobs.Result{}, err
			}
			checkpoint.Total = len(all)
		}
		ids, err := st.LibraryMediaIDsAfter(ctx, libraryID, store.MediaID(checkpoint.LastID), refreshJobBatchSize+1)
		if err != nil {
			return jobs.Result{}, err
		}
		done := 0
		for _, id := range ids {
			if done >= refreshJobBatchSize || ctx.Err() != nil {
				break
			}
			if done > 0 && !sleepCtx(ctx, pace) {
				break
			}
			if _, err := st.RenewJobLease(ctx, job.ID, job.LeaseOwner, time.Now().UTC()); err != nil {
				if ctx.Err() != nil {
					break
				}
				return jobs.Result{}, err
			}
			err := service.Refresh(ctx, id)
			if err != nil && ctx.Err() != nil {
				// Interrupted mid-item: leave it for the resumed run.
				break
			}
			switch {
			case err == nil:
				checkpoint.Refreshed++
			case errors.Is(err, ErrNothingToRefresh), errors.Is(err, ErrScraperUnavailable):
				checkpoint.Skipped++
			default:
				checkpoint.Failed++
			}
			checkpoint.LastID = string(id)
			done++
		}
		checkpoint.Version = 1
		if checkpoint.Total < checkpoint.processed() {
			checkpoint.Total = checkpoint.processed()
		}
		data, err := json.Marshal(checkpoint)
		if err != nil {
			return jobs.Result{}, err
		}
		progress := store.JobProgress{
			Current: int64(checkpoint.processed()),
			Total:   int64(checkpoint.Total),
			Unit:    "items",
			Message: RefreshJobProgressMessage,
		}
		// The checkpoint write must land even when shutdown cancelled ctx; a
		// cancel still refuses it through the lost lease.
		if err := st.UpdateJobCheckpoint(context.WithoutCancel(ctx), job.ID, job.LeaseOwner, data, progress, time.Now().UTC()); err != nil {
			return jobs.Result{}, err
		}
		if len(ids) > done || ctx.Err() != nil {
			return jobs.Result{Checkpoint: data}, jobs.ErrYielded
		}
		return jobs.Result{}, nil
	}
}

// sleepCtx waits d, returning false if ctx ends first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
