package store

// Schedule occurrence journal (AUDITFIX-SCHEDULER-DURABILITY).
//
// One due firing of a schedule is an OCCURRENCE, identified by its
// deterministic due instant (NextDue over the persisted completion cursor),
// never by the wall-clock instant the tick happened to run. The journal
// freezes the occurrence's member set at creation and marks each member's
// publication in the SAME events-DB transaction that inserts the job row, so
// a crash at any enqueue boundary resumes to exactly-once publication:
// journaled-but-unpublished members are re-published from their persisted
// spec, published members are never re-enqueued, and completion only ever
// moves the schedule cursor forward.

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ScheduleOccurrenceMember is one job a schedule occurrence must publish.
// The job spec fields are frozen at occurrence creation so a restart can
// resume publication without re-running the schedule's build function.
type ScheduleOccurrenceMember struct {
	Ordinal   int
	Kind      JobKind
	Class     JobClass
	Priority  int
	DedupeKey string
	Payload   []byte
	CreatedAt time.Time
	RunAfter  time.Time
	// JobID is set once the member's job row is committed; empty means the
	// member is still pending publication.
	JobID      JobID
	EnqueuedAt time.Time
}

// ScheduleOccurrence is one journaled due firing of a schedule.
type ScheduleOccurrence struct {
	ScheduleID ScheduleID
	Due        time.Time
	StartedAt  time.Time
	// CompletedAt is zero while any member is still pending publication.
	CompletedAt time.Time
	Members     []ScheduleOccurrenceMember
}

// Completed reports whether every member of the occurrence has been published
// and the occurrence's completion instant has been journaled.
func (o ScheduleOccurrence) Completed() bool {
	return !o.CompletedAt.IsZero()
}

// LatestScheduleOccurrence returns the newest journaled occurrence for the
// schedule, complete or not, with its members in ordinal order.
func (s *Store) LatestScheduleOccurrence(ctx context.Context, id ScheduleID) (ScheduleOccurrence, bool, error) {
	if id == "" {
		return ScheduleOccurrence{}, false, errors.New("schedule ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ScheduleOccurrence{}, false, err
	}
	occurrence, err := scanScheduleOccurrence(s.eventsR.QueryRowContext(ctx, `
		SELECT schedule_id, due_ms, started_at_ms, completed_at_ms
		FROM schedule_occurrence WHERE schedule_id = ?
		ORDER BY due_ms DESC LIMIT 1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ScheduleOccurrence{}, false, nil
	}
	if err != nil {
		return ScheduleOccurrence{}, false, err
	}
	occurrence.Members, err = scheduleOccurrenceMembersSQL(ctx, s.eventsR, occurrence.ScheduleID, occurrence.Due)
	if err != nil {
		return ScheduleOccurrence{}, false, err
	}
	return occurrence, true, nil
}

// BeginScheduleOccurrence journals a new occurrence with a frozen member set,
// in one transaction. If the occurrence already exists (a crash landed after
// the journal write), the existing journal — including any members already
// published — is returned unchanged, so a retry can never fork a second
// member set for the same due instant.
func (s *Store) BeginScheduleOccurrence(ctx context.Context, id ScheduleID, due, started time.Time, jobs []Job) (ScheduleOccurrence, error) {
	if id == "" {
		return ScheduleOccurrence{}, errors.New("schedule ID is required")
	}
	if due.IsZero() {
		return ScheduleOccurrence{}, errors.New("schedule occurrence due instant is required")
	}
	for _, job := range jobs {
		if err := validateScheduleOccurrenceJob(job); err != nil {
			return ScheduleOccurrence{}, err
		}
	}
	started = normalizeTime(started)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ScheduleOccurrence{}, err
	}
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return ScheduleOccurrence{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO schedule_occurrence(schedule_id, due_ms, started_at_ms)
		VALUES (?, ?, ?)
		ON CONFLICT(schedule_id, due_ms) DO NOTHING
	`, id, timeToMillis(due), timeToMillis(started))
	if err != nil {
		return ScheduleOccurrence{}, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return ScheduleOccurrence{}, err
	}
	if inserted == 0 {
		existing, err := scanScheduleOccurrence(tx.QueryRowContext(ctx, `
			SELECT schedule_id, due_ms, started_at_ms, completed_at_ms
			FROM schedule_occurrence WHERE schedule_id = ? AND due_ms = ?`, id, timeToMillis(due)))
		if err != nil {
			return ScheduleOccurrence{}, err
		}
		existing.Members, err = scheduleOccurrenceMembersTx(ctx, tx, id, due)
		if err != nil {
			return ScheduleOccurrence{}, err
		}
		return existing, tx.Commit()
	}
	occurrence := ScheduleOccurrence{ScheduleID: id, Due: due.UTC(), StartedAt: started}
	for ordinal, job := range jobs {
		createdAt := normalizeTime(job.CreatedAt)
		runAfter := job.RunAfter
		if runAfter.IsZero() {
			runAfter = createdAt
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO schedule_occurrence_member(
				schedule_id, due_ms, ordinal, job_kind, job_class, job_priority,
				job_dedupe_key, job_payload_json, job_created_at_ms, job_run_after_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, id, timeToMillis(due), ordinal, job.Kind, jobClassOrDefault(job.Class), job.Priority,
			nullableString(job.DedupeKey), string(normalizeJSON(job.Payload)),
			timeToMillis(createdAt), timeToMillis(runAfter.UTC())); err != nil {
			return ScheduleOccurrence{}, err
		}
		occurrence.Members = append(occurrence.Members, ScheduleOccurrenceMember{
			Ordinal:   ordinal,
			Kind:      job.Kind,
			Class:     jobClassOrDefault(job.Class),
			Priority:  job.Priority,
			DedupeKey: job.DedupeKey,
			Payload:   normalizeJSON(job.Payload),
			CreatedAt: createdAt,
			RunAfter:  runAfter.UTC(),
		})
	}
	return occurrence, tx.Commit()
}

// EnqueueScheduleOccurrenceMember publishes one journaled member: the job
// insert and the member's publication mark commit in ONE transaction, so no
// crash boundary can separate them. Calling it again for an already-published
// member returns the recorded job without inserting anything. A member whose
// dedupe key already names an active job adopts that job instead of stacking
// a duplicate.
func (s *Store) EnqueueScheduleOccurrenceMember(ctx context.Context, id ScheduleID, due time.Time, ordinal int, now time.Time) (Job, error) {
	if id == "" {
		return Job{}, errors.New("schedule ID is required")
	}
	now = normalizeTime(now)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Job{}, err
	}
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	member, err := scanScheduleOccurrenceMember(tx.QueryRowContext(ctx, scheduleOccurrenceMemberSelect+`
		WHERE schedule_id = ? AND due_ms = ? AND ordinal = ?`, id, timeToMillis(due), ordinal))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ValidationError("schedule occurrence member not found")
	}
	if err != nil {
		return Job{}, err
	}
	if member.JobID != "" {
		published, err := scheduleOccurrenceJobTx(ctx, tx, member.JobID)
		if err != nil {
			return Job{}, err
		}
		return published, tx.Commit()
	}
	job, err := normalizeNewJob(Job{
		Kind:      member.Kind,
		Class:     member.Class,
		Priority:  member.Priority,
		DedupeKey: member.DedupeKey,
		Payload:   member.Payload,
		CreatedAt: member.CreatedAt,
		RunAfter:  member.RunAfter,
	})
	if err != nil {
		return Job{}, err
	}
	job, err = enqueueJobTx(ctx, tx, job)
	if err != nil {
		return Job{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE schedule_occurrence_member SET job_id = ?, enqueued_at_ms = ?
		WHERE schedule_id = ? AND due_ms = ? AND ordinal = ? AND job_id IS NULL
	`, job.ID, timeToMillis(now), id, timeToMillis(due), ordinal)
	if err := jobMutationResult(result, err, ValidationError("schedule occurrence member already published")); err != nil {
		return Job{}, err
	}
	return job, tx.Commit()
}

// CompleteScheduleOccurrence journals the occurrence's completion instant.
// It refuses while any member is still pending, is idempotent (the first
// journaled completion instant wins), and prunes older completed occurrences
// of the same schedule so the journal stays bounded while the newest
// completion survives for post-crash cursor recovery.
func (s *Store) CompleteScheduleOccurrence(ctx context.Context, id ScheduleID, due, completedAt time.Time) error {
	if id == "" {
		return errors.New("schedule ID is required")
	}
	completedAt = normalizeTime(completedAt)
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
	var pending int
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM schedule_occurrence_member
		WHERE schedule_id = ? AND due_ms = ? AND job_id IS NULL
	`, id, timeToMillis(due)).Scan(&pending); err != nil {
		return err
	}
	if pending != 0 {
		return ValidationError("schedule occurrence has unpublished members")
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE schedule_occurrence SET completed_at_ms = ?
		WHERE schedule_id = ? AND due_ms = ? AND completed_at_ms IS NULL
	`, timeToMillis(completedAt), id, timeToMillis(due))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		var one int
		err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM schedule_occurrence WHERE schedule_id = ? AND due_ms = ?
		`, id, timeToMillis(due)).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return ValidationError("schedule occurrence not found")
		}
		if err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM schedule_occurrence
		WHERE schedule_id = ? AND completed_at_ms IS NOT NULL AND due_ms < ?
	`, id, timeToMillis(due)); err != nil {
		return err
	}
	return tx.Commit()
}

func validateScheduleOccurrenceJob(job Job) error {
	if job.Kind == "" {
		return ValidationError("schedule occurrence job kind is required")
	}
	if job.ID != "" {
		return ValidationError("schedule occurrence job must not carry a preset ID")
	}
	if job.Status != "" && job.Status != JobStatusQueued {
		return ValidationError("schedule occurrence job must be queued")
	}
	if len(job.Checkpoint) != 0 {
		return ValidationError("schedule occurrence job must not carry a checkpoint")
	}
	return nil
}

func jobClassOrDefault(class JobClass) JobClass {
	if class == "" {
		return JobClassBackground
	}
	return class
}

func scheduleOccurrenceJobTx(ctx context.Context, tx *sql.Tx, id JobID) (Job, error) {
	query := "SELECT " + jobColumns + " FROM job j LEFT JOIN job_checkpoint c ON c.job_id = j.id WHERE j.id = ?"
	job, err := scanJob(tx.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrJobNotFound
	}
	return job, err
}

func scanScheduleOccurrence(scanner interface{ Scan(dest ...any) error }) (ScheduleOccurrence, error) {
	var occurrence ScheduleOccurrence
	var due, started int64
	var completed sql.NullInt64
	if err := scanner.Scan(&occurrence.ScheduleID, &due, &started, &completed); err != nil {
		return ScheduleOccurrence{}, err
	}
	occurrence.Due = millisToTime(due)
	occurrence.StartedAt = millisToTime(started)
	if completed.Valid {
		occurrence.CompletedAt = millisToTime(completed.Int64)
	}
	return occurrence, nil
}

const scheduleOccurrenceMemberSelect = `
	SELECT ordinal, job_kind, job_class, job_priority, job_dedupe_key,
	       job_payload_json, job_created_at_ms, job_run_after_ms, job_id, enqueued_at_ms
	FROM schedule_occurrence_member
`

func scheduleOccurrenceMembersSQL(ctx context.Context, db *sql.DB, id ScheduleID, due time.Time) ([]ScheduleOccurrenceMember, error) {
	rows, err := db.QueryContext(ctx, scheduleOccurrenceMemberSelect+`
		WHERE schedule_id = ? AND due_ms = ? ORDER BY ordinal`, id, timeToMillis(due))
	if err != nil {
		return nil, err
	}
	return collectScheduleOccurrenceMembers(rows)
}

func scheduleOccurrenceMembersTx(ctx context.Context, tx *sql.Tx, id ScheduleID, due time.Time) ([]ScheduleOccurrenceMember, error) {
	rows, err := tx.QueryContext(ctx, scheduleOccurrenceMemberSelect+`
		WHERE schedule_id = ? AND due_ms = ? ORDER BY ordinal`, id, timeToMillis(due))
	if err != nil {
		return nil, err
	}
	return collectScheduleOccurrenceMembers(rows)
}

func collectScheduleOccurrenceMembers(rows *sql.Rows) ([]ScheduleOccurrenceMember, error) {
	defer rows.Close()
	members := []ScheduleOccurrenceMember{}
	for rows.Next() {
		member, err := scanScheduleOccurrenceMember(rows)
		if err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func scanScheduleOccurrenceMember(scanner interface{ Scan(dest ...any) error }) (ScheduleOccurrenceMember, error) {
	var member ScheduleOccurrenceMember
	var dedupe sql.NullString
	var payload string
	var created, runAfter int64
	var jobID sql.NullString
	var enqueued sql.NullInt64
	if err := scanner.Scan(&member.Ordinal, &member.Kind, &member.Class, &member.Priority,
		&dedupe, &payload, &created, &runAfter, &jobID, &enqueued); err != nil {
		return ScheduleOccurrenceMember{}, err
	}
	if dedupe.Valid {
		member.DedupeKey = dedupe.String
	}
	member.Payload = []byte(payload)
	member.CreatedAt = millisToTime(created)
	member.RunAfter = millisToTime(runAfter)
	if jobID.Valid {
		member.JobID = JobID(jobID.String)
	}
	if enqueued.Valid {
		member.EnqueuedAt = millisToTime(enqueued.Int64)
	}
	return member, nil
}
