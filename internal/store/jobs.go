package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	ErrJobNotFound    = errors.New("job not found")
	ErrNoRunnableJob  = errors.New("no runnable job")
	ErrJobNotRunning  = errors.New("job is not running")
	ErrJobTerminal    = errors.New("job is terminal")
	ErrJobNotTerminal = errors.New("job is not terminal")

	// ErrJobLeaseLost means the caller's lease is no longer the live one: it
	// expired, it was recovered, or another generation now holds the job. It
	// wraps ErrJobNotRunning so callers written before fencing existed keep
	// their errors.Is(err, ErrJobNotRunning) behaviour unchanged.
	ErrJobLeaseLost = fmt.Errorf("job lease lost: %w", ErrJobNotRunning)
)

const (
	// JobLeaseTTL is how long one claim or one renewal keeps a job leased.
	// The value is applied by the store, never supplied by a worker, so the
	// lease deadline is always a server-clock quantity.
	JobLeaseTTL = 15 * time.Minute

	// JobLeaseRenewInterval is the cadence at which a live worker renews its
	// lease. It is a third of the TTL so two consecutive renewal failures
	// still leave a full interval of slack before the lease actually lapses.
	JobLeaseRenewInterval = JobLeaseTTL / 3
)

// leaseNow is the server clock, and it is the ONLY clock the lease timeline is
// built on: claim deadlines, renewal deadlines, and every liveness test read it
// rather than the caller's `now`. Callers still name an operation time — that is
// what the row's updated_at_ms/finished_at_ms record — but a worker cannot
// lengthen its own lease, nor sneak a write past expiry, by reporting a time
// that suits it. Lease authority stays entirely server-side.
func leaseNow() time.Time {
	// Truncated to the millisecond the row stores, so a deadline handed back to
	// a caller is byte-identical to the persisted one.
	return millisToTime(timeToMillis(time.Now().UTC()))
}

// jobLeaseToken builds the fencing token for one lease generation. The
// generation is the job's attempt_count at claim time, which the claim
// transaction increments exactly once, so (job, generation) names exactly one
// lease for all time. Every conditional job mutation matches on this token,
// which is why a worker that reclaims the SAME job after its own lease lapsed
// (the ABA case) cannot finish it under the token it held before.
func jobLeaseToken(workerID string, generation int) string {
	return workerID + jobLeaseTokenSeparator + strconv.Itoa(generation)
}

const jobLeaseTokenSeparator = "#"

// jobLeaseGeneration recovers the generation from a fencing token. Tokens
// persisted before fencing existed carry no separator and report generation 0.
func jobLeaseGeneration(token string) int {
	index := strings.LastIndex(token, jobLeaseTokenSeparator)
	if index < 0 {
		return 0
	}
	generation, err := strconv.Atoi(token[index+len(jobLeaseTokenSeparator):])
	if err != nil {
		return 0
	}
	return generation
}

type JobID string
type JobKind string
type JobClass string
type JobStatus string

const (
	JobClassBackground  JobClass = "background"
	JobClassInteractive JobClass = "interactive"

	JobStatusQueued    JobStatus = "queued"
	JobStatusRunning   JobStatus = "running"
	JobStatusYielded   JobStatus = "yielded"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
	JobStatusCanceled  JobStatus = "canceled"
)

type JobProgress struct {
	Current int64
	Total   int64
	Unit    string
	Message string
}

type Job struct {
	ID                JobID
	Kind              JobKind
	Class             JobClass
	Status            JobStatus
	Priority          int
	DedupeKey         string
	Payload           []byte
	Checkpoint        []byte
	CheckpointVersion int
	Attempts          int
	RunAfter          time.Time
	// LeaseOwner is the opaque fencing token for the lease a claim handed out,
	// empty when the job holds no lease. Handlers pass it back verbatim to every
	// conditional mutation (progress, yield, complete, fail, renew); they must
	// not parse it or substitute their own worker id, because the token — not
	// the worker name — is what distinguishes this lease generation from an
	// earlier one the same worker held.
	LeaseOwner string
	// LeaseWorker is the worker id embedded in LeaseOwner, for operator display.
	LeaseWorker string
	// LeaseGeneration is the attempt number the lease was handed out on; it
	// increases on every claim of the same job and is 0 when no lease is held.
	LeaseGeneration int
	LeaseExpiresAt  time.Time
	Error           string
	Progress        JobProgress
	CreatedAt       time.Time
	UpdatedAt       time.Time
	StartedAt       time.Time
	FinishedAt      time.Time
	CanceledAt      time.Time
}

type JobStatsRow struct {
	Class  JobClass
	Status JobStatus
	Count  int
}

type JobEvent struct {
	ID         int64
	JobID      JobID
	OccurredAt time.Time
	Type       string
	Payload    []byte
}

func (s *Store) EnqueueJob(ctx context.Context, job Job) (Job, error) {
	normalized, err := normalizeNewJob(job)
	if err != nil {
		return Job{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Job{}, err
	}
	return s.enqueueJobSQL(ctx, normalized)
}

// verifyLiveJobLease reports whether leaseOwner is still THE live lease for id,
// under the same predicate every fenced job mutation carries
// (jobLiveLeaseClause). It is the ownership-succession check that
// side-effectful store operations run at each fence point before acting on
// behalf of a job: a lapsed, recovered, or superseded lease answers
// ErrJobLeaseLost, which is the instruction to stop — whichever owner holds the
// live lease now owns every later write. Read-only: it never extends or mutates
// the lease.
func (s *Store) verifyLiveJobLease(ctx context.Context, id JobID, leaseOwner string) error {
	if id == "" {
		return errors.New("job ID is required")
	}
	leaseOwner = strings.TrimSpace(leaseOwner)
	if leaseOwner == "" {
		return errors.New("job lease owner is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	var one int
	err := s.eventsR.QueryRowContext(ctx,
		`SELECT 1 FROM job WHERE id = ?`+jobLiveLeaseClause,
		id, leaseOwner, timeToMillis(leaseNow()),
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrJobLeaseLost
	}
	return err
}

func (s *Store) Job(ctx context.Context, id JobID) (Job, error) {
	if id == "" {
		return Job{}, errors.New("job ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Job{}, err
	}
	return s.jobSQL(ctx, id)
}

func (s *Store) ListJobs(ctx context.Context, statuses ...JobStatus) ([]Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	return s.listJobsSQL(ctx, statuses...)
}

func (s *Store) ListJobEvents(ctx context.Context, id JobID) ([]JobEvent, error) {
	if id == "" {
		return nil, errors.New("job ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	return s.listJobEventsSQL(ctx, id)
}

// AppendJobEvent records a durable operator-visible event for a running job.
func (s *Store) AppendJobEvent(ctx context.Context, id JobID, typ string, payload []byte, now time.Time) error {
	if id == "" {
		return errors.New("job ID is required")
	}
	typ = strings.TrimSpace(typ)
	if typ == "" {
		return errors.New("job event type is required")
	}
	now = normalizeTime(now)
	payload = normalizeJSON(payload)

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := appendJobEventSQL(ctx, tx, id, now, typ, string(payload)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ClaimNextJob(ctx context.Context, workerID string, classes []JobClass, now time.Time) (Job, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return Job{}, errors.New("worker ID is required")
	}
	now = normalizeTime(now)
	leaseExpiresAt := leaseNow().Add(JobLeaseTTL)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Job{}, err
	}
	return s.claimNextJobSQL(ctx, workerID, classes, now, leaseExpiresAt)
}

// RenewJobLease extends a live lease by another JobLeaseTTL and returns the new
// deadline. It is the only way a long-running handler keeps working past the
// initial TTL, and it is deliberately conditional: the renewal lands only while
// the job is still running under this exact fencing token AND the recorded
// deadline has not already passed. A worker therefore never decides for itself
// whether its lease is alive — it asks, and ErrJobLeaseLost is the instruction
// to stop. The new deadline is computed by the store, so the lease clock stays
// server-side even though the caller names the operation time.
//
// A renewal appends no job_event and no outbox row: a heartbeat is not an
// operator-visible transition, and emitting one per interval per running job
// would grow the event log without telling a reader anything new.
func (s *Store) RenewJobLease(ctx context.Context, id JobID, leaseOwner string, now time.Time) (time.Time, error) {
	if id == "" {
		return time.Time{}, errors.New("job ID is required")
	}
	leaseOwner = strings.TrimSpace(leaseOwner)
	if leaseOwner == "" {
		return time.Time{}, errors.New("job lease owner is required")
	}
	now = normalizeTime(now)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return time.Time{}, err
	}
	return s.renewJobLeaseSQL(ctx, id, leaseOwner, now)
}

func (s *Store) UpdateJobCheckpoint(ctx context.Context, id JobID, leaseOwner string, checkpoint []byte, progress JobProgress, now time.Time) error {
	if id == "" {
		return errors.New("job ID is required")
	}
	leaseOwner = strings.TrimSpace(leaseOwner)
	if leaseOwner == "" {
		return errors.New("job lease owner is required")
	}
	checkpoint = normalizeJSON(checkpoint)
	now = normalizeTime(now)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	return s.updateJobCheckpointSQL(ctx, id, leaseOwner, checkpoint, progress, now)
}

// YieldJob parks a running job for a later re-claim. A caller that supplies no
// checkpoint (nil/empty) PRESERVES the job's stored checkpoint rather than
// clearing it: the interruption path yields with no checkpoint of its own, and
// erasing the last durable record there would destroy the journal a handler
// wrote mid-run — exactly the record ADR-0174 succession recovery depends on
// after shutdown/restart. The has-checkpoint decision is made HERE, before
// normalizeJSON turns an empty slice into "{}". A handler that means to record
// progress passes its checkpoint explicitly and it upserts as always.
func (s *Store) YieldJob(ctx context.Context, id JobID, leaseOwner string, checkpoint []byte, runAfter time.Time, now time.Time) error {
	if id == "" {
		return errors.New("job ID is required")
	}
	leaseOwner = strings.TrimSpace(leaseOwner)
	if leaseOwner == "" {
		return errors.New("job lease owner is required")
	}
	hasCheckpoint := len(checkpoint) > 0
	checkpoint = normalizeJSON(checkpoint)
	runAfter = normalizeTime(runAfter)
	now = normalizeTime(now)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	return s.yieldJobSQL(ctx, id, leaseOwner, checkpoint, hasCheckpoint, runAfter, now)
}

func (s *Store) CompleteJob(ctx context.Context, id JobID, leaseOwner string, now time.Time) error {
	return s.finishJob(ctx, id, leaseOwner, JobStatusCompleted, "", now)
}

func (s *Store) FailJob(ctx context.Context, id JobID, leaseOwner string, message string, now time.Time) error {
	if strings.TrimSpace(message) == "" {
		return errors.New("job error message is required")
	}
	return s.finishJob(ctx, id, leaseOwner, JobStatusFailed, message, now)
}

func (s *Store) CancelJob(ctx context.Context, id JobID, now time.Time) error {
	if id == "" {
		return errors.New("job ID is required")
	}
	now = normalizeTime(now)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	return s.cancelJobSQL(ctx, id, now)
}

func (s *Store) RetryJob(ctx context.Context, id JobID, now time.Time) (Job, error) {
	if id == "" {
		return Job{}, errors.New("job ID is required")
	}
	now = normalizeTime(now)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Job{}, err
	}
	previous, err := s.jobSQL(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if previous.Status == JobStatusQueued || previous.Status == JobStatusRunning || previous.Status == JobStatusYielded {
		return Job{}, ErrJobNotTerminal
	}
	retry, err := normalizeNewJob(Job{
		Kind:              previous.Kind,
		Class:             previous.Class,
		Priority:          previous.Priority,
		DedupeKey:         previous.DedupeKey,
		Payload:           cloneBytes(previous.Payload),
		Checkpoint:        cloneBytes(previous.Checkpoint),
		CheckpointVersion: previous.CheckpointVersion,
		CreatedAt:         now,
	})
	if err != nil {
		return Job{}, err
	}
	return s.enqueueJobSQL(ctx, retry)
}

func (s *Store) ResumeInterruptedJobs(ctx context.Context, now time.Time) (int, error) {
	now = normalizeTime(now)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	return s.resumeInterruptedJobsSQL(ctx, now)
}

func (s *Store) RecoverExpiredJobLeases(ctx context.Context, now time.Time) (int, error) {
	now = normalizeTime(now)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	return s.recoverExpiredJobLeasesSQL(ctx, now)
}

func (s *Store) JobStats(ctx context.Context) ([]JobStatsRow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	return s.jobStatsSQL(ctx)
}

// OldestQueuedJobAgeMs returns how long, in milliseconds, the oldest job still
// waiting to run (queued or yielded) has been enqueued, and whether any such job
// exists. It backs ADR-0146 §8's server-observable job-queue-age SLI: the
// signal is derived from the durable job.created_at_ms — a pure server clock,
// carrying no library content — so it is safe under Invariant #5/#7.
func (s *Store) OldestQueuedJobAgeMs(ctx context.Context, now time.Time) (int64, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, false, err
	}
	var oldest sql.NullInt64
	row := s.eventsR.QueryRowContext(ctx, "SELECT MIN(created_at_ms) FROM job WHERE status IN (?, ?)", JobStatusQueued, JobStatusYielded)
	if err := row.Scan(&oldest); err != nil {
		return 0, false, err
	}
	if !oldest.Valid {
		return 0, false, nil
	}
	age := timeToMillis(now) - oldest.Int64
	if age < 0 {
		age = 0
	}
	return age, true, nil
}

func (s *Store) finishJob(ctx context.Context, id JobID, leaseOwner string, status JobStatus, message string, now time.Time) error {
	if id == "" {
		return errors.New("job ID is required")
	}
	leaseOwner = strings.TrimSpace(leaseOwner)
	if leaseOwner == "" {
		return errors.New("job lease owner is required")
	}
	now = normalizeTime(now)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	return s.finishJobSQL(ctx, id, leaseOwner, status, message, now)
}

func normalizeNewJob(job Job) (Job, error) {
	if job.ID == "" {
		id, err := randomJobID()
		if err != nil {
			return Job{}, err
		}
		job.ID = id
	}
	if job.Kind == "" {
		return Job{}, errors.New("job kind is required")
	}
	if job.Class == "" {
		job.Class = JobClassBackground
	}
	if job.Status == "" {
		job.Status = JobStatusQueued
	}
	if job.Status != JobStatusQueued {
		return Job{}, errors.New("new job must be queued")
	}
	now := normalizeTime(job.CreatedAt)
	job.CreatedAt = now
	if job.UpdatedAt.IsZero() || job.UpdatedAt.Before(job.CreatedAt) {
		job.UpdatedAt = job.CreatedAt
	} else {
		job.UpdatedAt = job.UpdatedAt.UTC()
	}
	if job.RunAfter.IsZero() {
		job.RunAfter = job.CreatedAt
	} else {
		job.RunAfter = job.RunAfter.UTC()
	}
	job.Payload = normalizeJSON(job.Payload)
	job.Checkpoint = normalizeJSON(job.Checkpoint)
	if job.CheckpointVersion == 0 {
		job.CheckpointVersion = 1
	}
	return job, nil
}

func normalizeJSON(data []byte) []byte {
	if len(data) == 0 {
		return []byte("{}")
	}
	return cloneBytes(data)
}

func cloneBytes(data []byte) []byte {
	if data == nil {
		return nil
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out
}

func randomJobID() (JobID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return JobID("job-" + hex.EncodeToString(raw[:])), nil
}

func normalizeTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func scanJob(scanner interface {
	Scan(dest ...any) error
}) (Job, error) {
	var job Job
	var dedupe sql.NullString
	var leaseOwner sql.NullString
	var errText sql.NullString
	var progressUnit sql.NullString
	var progressMessage sql.NullString
	var started sql.NullInt64
	var finished sql.NullInt64
	var leaseExpires sql.NullInt64
	var canceled sql.NullInt64
	var runAfter int64
	var created int64
	var updated int64
	if err := scanner.Scan(
		&job.ID,
		&job.Kind,
		&job.Class,
		&job.Status,
		&job.Priority,
		&dedupe,
		&job.Payload,
		&job.Checkpoint,
		&job.CheckpointVersion,
		&job.Attempts,
		&runAfter,
		&leaseOwner,
		&leaseExpires,
		&errText,
		&job.Progress.Current,
		&job.Progress.Total,
		&progressUnit,
		&progressMessage,
		&created,
		&updated,
		&started,
		&finished,
		&canceled,
	); err != nil {
		return Job{}, err
	}
	if dedupe.Valid {
		job.DedupeKey = dedupe.String
	}
	if leaseOwner.Valid {
		job.LeaseOwner = leaseOwner.String
		job.LeaseGeneration = jobLeaseGeneration(job.LeaseOwner)
		job.LeaseWorker = job.LeaseOwner
		if index := strings.LastIndex(job.LeaseOwner, jobLeaseTokenSeparator); index >= 0 {
			job.LeaseWorker = job.LeaseOwner[:index]
		}
	}
	if errText.Valid {
		job.Error = errText.String
	}
	if progressUnit.Valid {
		job.Progress.Unit = progressUnit.String
	}
	if progressMessage.Valid {
		job.Progress.Message = progressMessage.String
	}
	job.RunAfter = millisToTime(runAfter)
	job.CreatedAt = millisToTime(created)
	job.UpdatedAt = millisToTime(updated)
	if started.Valid {
		job.StartedAt = millisToTime(started.Int64)
	}
	if finished.Valid {
		job.FinishedAt = millisToTime(finished.Int64)
	}
	if leaseExpires.Valid {
		job.LeaseExpiresAt = millisToTime(leaseExpires.Int64)
	}
	if canceled.Valid {
		job.CanceledAt = millisToTime(canceled.Int64)
	}
	return job, nil
}

const jobColumns = `
  j.id, j.kind, j.class, j.status, j.priority, j.dedupe_key, j.payload_json,
  COALESCE(c.checkpoint_json, '{}'), COALESCE(c.version, 0),
  j.attempt_count, j.available_at_ms, j.lease_owner, j.lease_expires_at_ms,
  j.last_error, j.progress_current, j.progress_total, j.progress_unit, j.progress_message,
  j.created_at_ms, j.updated_at_ms, j.started_at_ms, j.finished_at_ms, j.cancel_requested_at_ms
`

func (s *Store) enqueueJobSQL(ctx context.Context, job Job) (Job, error) {
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	job, err = enqueueJobTx(ctx, tx, job)
	if err != nil {
		return Job{}, err
	}
	return job, tx.Commit()
}

// enqueueJobTx inserts a normalized new job inside the caller's transaction,
// honoring active-job dedupe. The schedule occurrence journal rides this so a
// member's job insert commits atomically with its publication mark.
func enqueueJobTx(ctx context.Context, tx *sql.Tx, job Job) (Job, error) {
	if job.DedupeKey != "" {
		existing, err := activeJobByDedupeSQL(ctx, tx, job.Kind, job.DedupeKey)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, ErrJobNotFound) {
			return Job{}, err
		}
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO job(id, kind, class, payload_json, dedupe_key, priority, status, created_at_ms, updated_at_ms, available_at_ms, attempt_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		job.ID, job.Kind, job.Class, string(job.Payload), nullableString(job.DedupeKey), job.Priority, job.Status,
		timeToMillis(job.CreatedAt), timeToMillis(job.UpdatedAt), timeToMillis(job.RunAfter), job.Attempts,
	)
	if err != nil {
		return Job{}, err
	}
	if err := upsertJobCheckpointSQL(ctx, tx, job.ID, job.Checkpoint, job.CheckpointVersion, job.UpdatedAt); err != nil {
		return Job{}, err
	}
	if err := appendJobEventSQL(ctx, tx, job.ID, job.UpdatedAt, "queued", `{"status":"queued"}`); err != nil {
		return Job{}, err
	}
	return job, nil
}

func activeJobByDedupeSQL(ctx context.Context, tx *sql.Tx, kind JobKind, dedupeKey string) (Job, error) {
	query := "SELECT " + jobColumns + " FROM job j LEFT JOIN job_checkpoint c ON c.job_id = j.id WHERE j.kind = ? AND j.dedupe_key = ? AND j.status IN ('queued', 'running', 'yielded') ORDER BY j.created_at_ms LIMIT 1"
	job, err := scanJob(tx.QueryRowContext(ctx, query, kind, dedupeKey))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrJobNotFound
	}
	return job, err
}

func (s *Store) jobSQL(ctx context.Context, id JobID) (Job, error) {
	query := "SELECT " + jobColumns + " FROM job j LEFT JOIN job_checkpoint c ON c.job_id = j.id WHERE j.id = ?"
	job, err := scanJob(s.eventsR.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrJobNotFound
	}
	return job, err
}

func (s *Store) listJobsSQL(ctx context.Context, statuses ...JobStatus) ([]Job, error) {
	args := []any{}
	query := "SELECT " + jobColumns + " FROM job j LEFT JOIN job_checkpoint c ON c.job_id = j.id"
	if len(statuses) > 0 {
		parts := make([]string, len(statuses))
		for i, status := range statuses {
			parts[i] = "?"
			args = append(args, status)
		}
		query += " WHERE j.status IN (" + strings.Join(parts, ", ") + ")"
	}
	query += " ORDER BY j.created_at_ms, j.id"
	rows, err := s.eventsR.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) listJobEventsSQL(ctx context.Context, id JobID) ([]JobEvent, error) {
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT id, job_id, occurred_at_ms, type, payload_json
		FROM job_event
		WHERE job_id = ?
		ORDER BY occurred_at_ms, id
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []JobEvent
	for rows.Next() {
		var event JobEvent
		var occurred int64
		if err := rows.Scan(&event.ID, &event.JobID, &occurred, &event.Type, &event.Payload); err != nil {
			return nil, err
		}
		event.OccurredAt = millisToTime(occurred)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) claimNextJobSQL(ctx context.Context, workerID string, classes []JobClass, now time.Time, leaseExpiresAt time.Time) (Job, error) {
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()

	args := []any{timeToMillis(now)}
	query := "SELECT " + jobColumns + " FROM job j LEFT JOIN job_checkpoint c ON c.job_id = j.id WHERE j.status IN ('queued', 'yielded') AND j.available_at_ms <= ?"
	if len(classes) > 0 {
		parts := make([]string, len(classes))
		for i, class := range classes {
			parts[i] = "?"
			args = append(args, class)
		}
		query += " AND j.class IN (" + strings.Join(parts, ", ") + ")"
	}
	query += " ORDER BY j.priority DESC, j.created_at_ms ASC LIMIT 1"
	job, err := scanJob(tx.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNoRunnableJob
	}
	if err != nil {
		return Job{}, err
	}
	startedAt := job.StartedAt
	if startedAt.IsZero() {
		startedAt = now
	}
	// attempt_count is incremented by this very statement, so the generation
	// this claim is fenced by is the count the row will hold once it lands.
	generation := job.Attempts + 1
	leaseToken := jobLeaseToken(workerID, generation)
	result, err := tx.ExecContext(ctx, `
		UPDATE job
		SET status = ?, lease_owner = ?, lease_expires_at_ms = ?, attempt_count = attempt_count + 1, updated_at_ms = ?, started_at_ms = COALESCE(started_at_ms, ?)
		WHERE id = ? AND status IN ('queued', 'yielded')
	`, JobStatusRunning, leaseToken, timeToMillis(leaseExpiresAt), timeToMillis(now), timeToMillis(startedAt), job.ID)
	if err != nil {
		return Job{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Job{}, err
	}
	if affected == 0 {
		return Job{}, ErrNoRunnableJob
	}
	if err := appendJobEventSQL(ctx, tx, job.ID, now, "running", `{"status":"running"}`); err != nil {
		return Job{}, err
	}
	job.Status = JobStatusRunning
	job.LeaseOwner = leaseToken
	job.LeaseWorker = workerID
	job.LeaseGeneration = generation
	job.LeaseExpiresAt = leaseExpiresAt
	job.Attempts++
	job.UpdatedAt = now
	job.StartedAt = startedAt
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return job, nil
}

// jobLiveLeaseClause is the single fence every lease-bearing job mutation
// carries. Two conditions, both required, both evaluated by the database rather
// than by the worker:
//
//   - lease_owner = ? matches the exact fencing token the claim issued, so a
//     stale generation — including the same worker's own earlier lease — is
//     refused (the ABA case).
//   - lease_expires_at_ms > ? refuses a write whose lease has already lapsed,
//     even in the window before RecoverExpiredJobLeases has swept it. Without
//     this half, an expired worker could still record progress or finalize a
//     job another worker is about to pick up.
//
// The trailing placeholders are bound as (leaseOwner, nowMillis) after the
// statement's own arguments.
const jobLiveLeaseClause = ` AND status = 'running' AND lease_owner = ? AND lease_expires_at_ms IS NOT NULL AND lease_expires_at_ms > ?`

func (s *Store) renewJobLeaseSQL(ctx context.Context, id JobID, leaseOwner string, now time.Time) (time.Time, error) {
	renewedTo := leaseNow().Add(JobLeaseTTL)
	result, err := s.eventsW.ExecContext(ctx, `
		UPDATE job
		SET lease_expires_at_ms = ?, updated_at_ms = ?
		WHERE id = ?`+jobLiveLeaseClause,
		timeToMillis(renewedTo), timeToMillis(now), id, leaseOwner, timeToMillis(leaseNow()))
	if err := jobMutationResult(result, err, ErrJobLeaseLost); err != nil {
		return time.Time{}, err
	}
	return renewedTo, nil
}

func (s *Store) updateJobCheckpointSQL(ctx context.Context, id JobID, leaseOwner string, checkpoint []byte, progress JobProgress, now time.Time) error {
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE job
		SET progress_current = ?, progress_total = ?, progress_unit = ?, progress_message = ?, updated_at_ms = ?
		WHERE id = ?`+jobLiveLeaseClause,
		progress.Current, progress.Total, nullableString(progress.Unit), nullableString(progress.Message), timeToMillis(now), id, leaseOwner, timeToMillis(leaseNow()))
	if err := jobMutationResult(result, err, ErrJobLeaseLost); err != nil {
		return err
	}
	version, err := nextCheckpointVersionSQL(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := upsertJobCheckpointSQL(ctx, tx, id, checkpoint, version, now); err != nil {
		return err
	}
	if err := appendJobEventSQL(ctx, tx, id, now, "progress", `{"type":"progress"}`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) yieldJobSQL(ctx context.Context, id JobID, leaseOwner string, checkpoint []byte, hasCheckpoint bool, runAfter time.Time, now time.Time) error {
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE job
		SET status = ?, available_at_ms = ?, lease_owner = NULL, lease_expires_at_ms = NULL, updated_at_ms = ?
		WHERE id = ?`+jobLiveLeaseClause,
		JobStatusYielded, timeToMillis(runAfter), timeToMillis(now), id, leaseOwner, timeToMillis(leaseNow()))
	if err := jobMutationResult(result, err, ErrJobLeaseLost); err != nil {
		return err
	}
	// A yield carrying no checkpoint preserves the stored one (see YieldJob):
	// only an explicit checkpoint advances the journal.
	if hasCheckpoint {
		version, err := nextCheckpointVersionSQL(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := upsertJobCheckpointSQL(ctx, tx, id, checkpoint, version, now); err != nil {
			return err
		}
	}
	if err := appendJobEventSQL(ctx, tx, id, now, "yielded", `{"status":"yielded"}`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) finishJobSQL(ctx context.Context, id JobID, leaseOwner string, status JobStatus, message string, now time.Time) error {
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE job
		SET status = ?, lease_owner = NULL, lease_expires_at_ms = NULL, last_error = ?, updated_at_ms = ?, finished_at_ms = ?
		WHERE id = ?`+jobLiveLeaseClause,
		status, nullableString(message), timeToMillis(now), timeToMillis(now), id, leaseOwner, timeToMillis(leaseNow()))
	if err := jobMutationResult(result, err, ErrJobLeaseLost); err != nil {
		return err
	}
	if err := appendJobEventSQL(ctx, tx, id, now, string(status), `{"status":"`+string(status)+`"}`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) cancelJobSQL(ctx context.Context, id JobID, now time.Time) error {
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE job
		SET status = ?, lease_owner = NULL, lease_expires_at_ms = NULL, updated_at_ms = ?, cancel_requested_at_ms = ?, finished_at_ms = ?
		WHERE id = ? AND status IN ('queued', 'running', 'yielded')
	`, JobStatusCanceled, timeToMillis(now), timeToMillis(now), timeToMillis(now), id)
	if err := jobMutationResult(result, err, ErrJobTerminal); err != nil {
		return err
	}
	if err := appendJobEventSQL(ctx, tx, id, now, "canceled", `{"status":"canceled"}`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) resumeInterruptedJobsSQL(ctx context.Context, now time.Time) (int, error) {
	return s.yieldRunningJobsSQL(ctx, "1 = 1", now)
}

func (s *Store) recoverExpiredJobLeasesSQL(ctx context.Context, now time.Time) (int, error) {
	return s.yieldRunningJobsSQL(ctx, "lease_expires_at_ms IS NOT NULL AND lease_expires_at_ms <= "+timeMillisString(now), now)
}

func (s *Store) yieldRunningJobsSQL(ctx context.Context, predicate string, now time.Time) (int, error) {
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	query := "SELECT id FROM job WHERE status = 'running' AND " + predicate
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return 0, err
	}
	var ids []JobID
	for rows.Next() {
		var id JobID
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return 0, nil
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE job SET status = ?, lease_owner = NULL, lease_expires_at_ms = NULL, available_at_ms = ?, updated_at_ms = ?
		WHERE status = 'running' AND `+predicate, JobStatusYielded, timeToMillis(now), timeToMillis(now))
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := appendJobEventSQL(ctx, tx, id, now, "yielded", `{"status":"yielded","reason":"resume"}`); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(affected), nil
}

func (s *Store) jobStatsSQL(ctx context.Context) ([]JobStatsRow, error) {
	rows, err := s.eventsR.QueryContext(ctx, "SELECT class, status, COUNT(*) FROM job GROUP BY class, status ORDER BY class, status")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stats []JobStatsRow
	for rows.Next() {
		var row JobStatsRow
		if err := rows.Scan(&row.Class, &row.Status, &row.Count); err != nil {
			return nil, err
		}
		stats = append(stats, row)
	}
	return stats, rows.Err()
}

func upsertJobCheckpointSQL(ctx context.Context, tx *sql.Tx, id JobID, checkpoint []byte, version int, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO job_checkpoint(job_id, version, checkpoint_json, updated_at_ms)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(job_id) DO UPDATE SET
		  version = excluded.version,
		  checkpoint_json = excluded.checkpoint_json,
		  updated_at_ms = excluded.updated_at_ms
	`, id, version, string(checkpoint), timeToMillis(now))
	return err
}

func nextCheckpointVersionSQL(ctx context.Context, tx *sql.Tx, id JobID) (int, error) {
	var version sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT version FROM job_checkpoint WHERE job_id = ?", id).Scan(&version); errors.Is(err, sql.ErrNoRows) {
		return 1, nil
	} else if err != nil {
		return 0, err
	}
	if !version.Valid {
		return 1, nil
	}
	return int(version.Int64) + 1, nil
}

func appendJobEventSQL(ctx context.Context, tx *sql.Tx, id JobID, now time.Time, typ string, payload string) error {
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO job_event(job_id, occurred_at_ms, type, payload_json) VALUES (?, ?, ?, ?)",
		id, timeToMillis(now), typ, payload,
	); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		"INSERT INTO event_outbox(occurred_at_ms, kind, aggregate_type, aggregate_id, payload_json) VALUES (?, ?, ?, ?, ?)",
		timeToMillis(now), "job."+typ, "job", id, payload,
	)
	return err
}

func jobMutationResult(result sql.Result, err error, empty error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return empty
	}
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func timeToMillis(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixNano() / int64(time.Millisecond)
}

func timeMillisString(value time.Time) string {
	return strconv.FormatInt(timeToMillis(value), 10)
}

func millisToTime(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value*int64(time.Millisecond)).UTC()
}
