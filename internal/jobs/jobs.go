// Package jobs runs durable Beamfall background work on top of events storage.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/beamfall/core/internal/store"
)

// secretPatterns matches known secret classes before they reach operational
// logs. Patterns cover:
//   - Beamfall API keys (bfk_…)
//   - Authorization header values (Bearer …, Basic …)
//   - URL query tokens (?token=…, &token=…)
//   - Stream and pack content paths (/api/media/…, /api/pack/…)
//   - Private endpoint URLs (http:// and https:// in error strings)
//
// Each pattern replaces the matched value with a [REDACTED] marker so log
// consumers still see a non-empty diagnostic token.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(bfk_[A-Za-z0-9_-]+)`),
	regexp.MustCompile(`(?i)(Bearer\s+\S+)`),
	regexp.MustCompile(`(?i)(Basic\s+\S+)`),
	regexp.MustCompile(`(?i)([?&]token=[^&\s"']+)`),
	regexp.MustCompile(`(?i)(/api/media/[^\s"']+)`),
	regexp.MustCompile(`(?i)(/api/pack/[^\s"']+)`),
	regexp.MustCompile(`(?i)(https?://[^\s"']+)`),
}

// redactLogError returns a copy of err.Error() with every known secret class
// replaced by [REDACTED]. It is applied before logging and before storing job
// failure text so Console reads never receive a handler-embedded secret.
func redactLogError(err error) string {
	s := err.Error()
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}

var (
	ErrYielded    = errors.New("job yielded")
	ErrNoHandler  = errors.New("job handler not registered")
	ErrNoRunnable = store.ErrNoRunnableJob
)

type Store interface {
	ClaimNextJob(context.Context, string, []store.JobClass, time.Time) (store.Job, error)
	CompleteJob(context.Context, store.JobID, string, time.Time) error
	FailJob(context.Context, store.JobID, string, string, time.Time) error
	YieldJob(context.Context, store.JobID, string, []byte, time.Time, time.Time) error
	ResumeInterruptedJobs(context.Context, time.Time) (int, error)
	RecoverExpiredJobLeases(context.Context, time.Time) (int, error)
}

type Handler func(context.Context, store.Job) (Result, error)

type Result struct {
	Checkpoint []byte
	RunAfter   time.Time
}

type Registry struct {
	store    Store
	workerID string
	handlers map[store.JobKind]Handler
	logger   *slog.Logger
	// renewInterval is how often a running job's lease is renewed, and leaseTTL
	// is how long one renewal buys. They mirror the store's own lease policy;
	// tests narrow them to keep a renewal round observable.
	renewInterval time.Duration
	leaseTTL      time.Duration
}

func NewRegistry(st Store, workerID string) *Registry {
	return &Registry{
		store:         st,
		workerID:      workerID,
		handlers:      map[store.JobKind]Handler{},
		logger:        slog.Default(),
		renewInterval: store.JobLeaseRenewInterval,
		leaseTTL:      store.JobLeaseTTL,
	}
}

// SetLogger overrides the default logger; nil restores slog.Default().
func (r *Registry) SetLogger(logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	r.logger = logger
}

func (r *Registry) Register(kind store.JobKind, handler Handler) {
	if kind == "" || handler == nil {
		return
	}
	r.handlers[kind] = handler
}

func (r *Registry) Resume(ctx context.Context, now time.Time) (int, error) {
	count, err := r.store.ResumeInterruptedJobs(ctx, now)
	if err == nil && count > 0 {
		r.logger.InfoContext(ctx, "jobs resumed after interruption", "count", count)
	}
	return count, err
}

// RecoverExpiredLeases requeues running jobs whose lease has lapsed.
func (r *Registry) RecoverExpiredLeases(ctx context.Context, now time.Time) (int, error) {
	count, err := r.store.RecoverExpiredJobLeases(ctx, now)
	if err == nil && count > 0 {
		r.logger.WarnContext(ctx, "expired job leases recovered", "count", count)
	}
	return count, err
}

func (r *Registry) RunOne(ctx context.Context, classes []store.JobClass, now time.Time) (store.Job, error) {
	job, err := r.store.ClaimNextJob(ctx, r.workerID, classes, now)
	if err != nil {
		return store.Job{}, err
	}
	r.logger.DebugContext(ctx, "job claimed", "id", job.ID, "kind", job.Kind)
	// Bookkeeping writes must land even when ctx is canceled mid-handler;
	// otherwise the job sits "running" until its lease expires.
	bookCtx := context.WithoutCancel(ctx)
	handler := r.handlers[job.Kind]
	if handler == nil {
		r.logger.WarnContext(ctx, "job failed", "id", job.ID, "kind", job.Kind, "reason", "no handler")
		failErr := r.store.FailJob(bookCtx, job.ID, job.LeaseOwner, ErrNoHandler.Error(), now)
		return job, errors.Join(ErrNoHandler, failErr)
	}
	// The lease is renewed for as long as the handler runs, and the handler runs
	// under a context the renewal loop cancels the moment the lease is lost.
	leaseCtx, lease := r.startLeaseRenewal(ctx, job)
	defer lease.stop()
	result, err := r.runHandler(leaseCtx, handler, job)
	// Join the renewal goroutine before reading its verdict, so the lease state
	// this decision rests on is final rather than one tick behind.
	lease.stop()
	if lease.leaseLost() {
		r.logger.WarnContext(ctx, "job abandoned after lease loss", "id", job.ID, "kind", job.Kind)
		return job, ErrLeaseLost
	}
	// A durable job interrupted by its own context being cancelled — shutdown or
	// governor preemption arriving mid-handler — yields and resumes from its
	// checkpoint rather than being recorded as failed. The guard is
	// narrow: only a cancellation error raised while the outer ctx is itself
	// cancelled counts, so a handler's unrelated error keeps its failure semantics.
	interrupted := ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
	if errors.Is(err, ErrYielded) || interrupted {
		runAfter := result.RunAfter
		if runAfter.IsZero() {
			runAfter = now
		}
		r.logger.InfoContext(ctx, "job yielded", "id", job.ID, "kind", job.Kind)
		return job, r.store.YieldJob(bookCtx, job.ID, job.LeaseOwner, result.Checkpoint, runAfter, now)
	}
	if err != nil {
		redactedErr := redactLogError(err)
		r.logger.WarnContext(ctx, "job failed", "id", job.ID, "kind", job.Kind)
		r.logger.DebugContext(ctx, "job failure detail", "id", job.ID, "error", redactedErr)
		return job, r.store.FailJob(bookCtx, job.ID, job.LeaseOwner, redactedErr, now)
	}
	r.logger.InfoContext(ctx, "job completed", "id", job.ID, "kind", job.Kind)
	return job, r.store.CompleteJob(bookCtx, job.ID, job.LeaseOwner, now)
}

// runHandler converts a handler panic into a failed-job error instead of
// killing the process.
func (r *Registry) runHandler(ctx context.Context, handler Handler, job store.Job) (result Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("job handler panic: %v", recovered)
		}
	}()
	return handler(ctx, job)
}
