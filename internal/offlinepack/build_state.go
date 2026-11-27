package offlinepack

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"sync"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Build failure reasons are the closed, client-safe codes a failed background
// build reports (CORE-1743). The job log keeps the underlying error; clients
// only ever see one of these codes, never a path or a raw message.
const (
	FailureEmptyTarget       = "empty_target"
	FailureUnknownTargetKind = "unknown_target_kind"
	FailureUnknownQuality    = "unknown_quality"
	FailureDeviceAttestation = "device_attestation_failed"
	FailureSourceUnavailable = "source_unavailable"
	FailureTranscode         = "transcode_failed"
	FailureByteBound         = "byte_bound_exceeded"
	FailureBuild             = "build_failed"
	// FailurePartsUnsupported names a multi-Part Media the requesting client
	// did not declare it can store (ADR-0157 Amendment A).
	FailurePartsUnsupported = "parts_unsupported"
)

var (
	// ErrItemTooLarge is returned when one item's bytes exceed MaxItemSize.
	ErrItemTooLarge = errors.New("offline pack item exceeds size bound")
	// ErrBuildBudget is returned when a build's bytes exceed its byte budget.
	ErrBuildBudget = errors.New("offline pack exceeds cumulative byte budget")
	// ErrTranscode wraps any failure to produce a transcoded item.
	ErrTranscode = errors.New("offline pack transcode")
	// ErrDeviceAttestation wraps a missing or rejected device-key attestation.
	ErrDeviceAttestation = errors.New("offline pack device-key attestation")
	// ErrBuildSealed is returned when a build cannot be retried because its
	// pack has already sealed; the caller reads the pack instead.
	ErrBuildSealed = errors.New("offline pack build has already sealed")
	// ErrBuildDevice is returned when a build is retried from a device other
	// than the one its pack is sealed to.
	ErrBuildDevice = errors.New("offline pack build belongs to another device")
)

// FailureReason maps a build error to its client-safe reason code.
func FailureReason(err error) string {
	switch {
	case errors.Is(err, ErrEmptyTarget):
		return FailureEmptyTarget
	case errors.Is(err, ErrUnknownTargetKind):
		return FailureUnknownTargetKind
	case errors.Is(err, ErrUnknownQuality):
		return FailureUnknownQuality
	case errors.Is(err, ErrDeviceAttestation):
		return FailureDeviceAttestation
	case errors.Is(err, ErrTranscode):
		return FailureTranscode
	case errors.Is(err, ErrItemTooLarge), errors.Is(err, ErrOriginalItemTooLarge), errors.Is(err, ErrBuildBudget):
		return FailureByteBound
	case errors.Is(err, ErrManifestPartsUnsupported):
		return FailurePartsUnsupported
	case errors.Is(err, fs.ErrNotExist):
		return FailureSourceUnavailable
	default:
		return FailureBuild
	}
}

// TrackBuild records the pending building row for a queued background build
// payload, so offlinePacks lists the pack before it seals, and returns that
// row. Tracking is idempotent. A payload without a pre-assigned pack ID, or
// one whose pack has already sealed, tracks nothing and returns a zero row.
func (s *Service) TrackBuild(ctx context.Context, payload []byte) (store.OfflinePackBuild, error) {
	var job BuildJobPayload
	if err := json.Unmarshal(payload, &job); err != nil {
		return store.OfflinePackBuild{}, err
	}
	if job.PackID == "" {
		return store.OfflinePackBuild{}, nil
	}
	if !ValidPackID(job.PackID) {
		return store.OfflinePackBuild{}, errors.New("offline pack ID is malformed")
	}
	build, err := s.store.TrackOfflinePackBuild(ctx, store.OfflinePackBuild{
		PackID: job.PackID, ProfileID: job.ProfileID, DeviceID: job.DeviceID,
		TargetKind: job.TargetKind, TargetRef: job.TargetRef, Quality: job.Quality,
		Payload: payload,
	}, s.now())
	if errors.Is(err, store.ErrOfflinePackBuildNotFound) {
		return store.OfflinePackBuild{}, nil
	}
	return build, err
}

// Builds lists the caller's background builds that have not sealed yet.
func (s *Service) Builds(ctx context.Context, profileID store.ProfileID) ([]store.OfflinePackBuild, error) {
	return s.store.OfflinePackBuildsForProfile(ctx, profileID)
}

// RetryBuild restarts the caller's failed build under the same pack ID. It
// returns the build row and, when this call restarted it, the job payload to
// queue. A build that is already building returns a nil payload, so a
// repeated retry queues nothing. The payload keeps the original target,
// quality, Versions and device key but takes the caller's current access
// scope, so a retry never widens what the first request could read.
func (s *Service) RetryBuild(ctx context.Context, scope auth.Scope, id store.OfflinePackID) (store.OfflinePackBuild, []byte, error) {
	if !ValidPackID(id) {
		return store.OfflinePackBuild{}, nil, store.ErrOfflinePackBuildNotFound
	}
	if _, err := s.store.OfflinePackByID(ctx, id, scope.ProfileID); err == nil {
		return store.OfflinePackBuild{}, nil, ErrBuildSealed
	} else if !errors.Is(err, store.ErrOfflinePackNotFound) {
		return store.OfflinePackBuild{}, nil, err
	}
	build, err := s.store.OfflinePackBuildByID(ctx, id, scope.ProfileID)
	if err != nil {
		return store.OfflinePackBuild{}, nil, err
	}
	if build.DeviceID != scope.DeviceID {
		return store.OfflinePackBuild{}, nil, ErrBuildDevice
	}
	if build.State != store.OfflinePackBuildFailed {
		return build, nil, nil
	}
	var job BuildJobPayload
	if err := json.Unmarshal(build.Payload, &job); err != nil {
		return store.OfflinePackBuild{}, nil, err
	}
	payload, err := BuildJobPayloadFor(scope, BuildPackRequest{
		TargetKind: job.TargetKind, TargetRef: job.TargetRef, ExpiresAt: job.ExpiresAt,
		DevicePublicKey: job.DevicePublicKey, DeviceAttestation: job.DeviceAttestation,
		SelectedVersions: job.SelectedVersions, Quality: job.Quality, PackID: id,
		ManifestParts: job.ManifestParts,
	})
	if err != nil {
		return store.OfflinePackBuild{}, nil, err
	}
	restarted, err := s.store.RestartOfflinePackBuild(ctx, id, scope.ProfileID, payload, s.now())
	if err != nil {
		return store.OfflinePackBuild{}, nil, err
	}
	if !restarted {
		build, err = s.store.OfflinePackBuildByID(ctx, id, scope.ProfileID)
		if err != nil {
			return store.OfflinePackBuild{}, nil, err
		}
		return build, nil, nil
	}
	// The row is building now and only the caller's enqueue gives it a job,
	// so a request cancelled from here on must not abandon it: reread
	// without the request's cancellation, and put the row back to failed
	// (retryable) if the reread still fails.
	detached := context.WithoutCancel(ctx)
	build, err = s.store.OfflinePackBuildByID(detached, id, scope.ProfileID)
	if err != nil {
		if failErr := s.store.FailOfflinePackBuild(detached, id, FailureBuild, s.now()); failErr != nil {
			return store.OfflinePackBuild{}, nil, errors.Join(err, failErr)
		}
		return store.OfflinePackBuild{}, nil, err
	}
	return build, payload, nil
}

// FailBuild marks a building row failed with a closed reason code, for a
// retry whose job could not be queued.
func (s *Service) FailBuild(ctx context.Context, id store.OfflinePackID, reason string) error {
	return s.store.FailOfflinePackBuild(ctx, id, reason, s.now())
}

// BuildJobCanceled moves the build of a cancelled job back to failed. An
// operator cancel makes the job terminal, so no worker will ever finish that
// build; failed keeps it retryable and dismissable. Call it only once no
// worker holds the job: jobCancel does for a job no worker had claimed, and
// RunBuildJob does after a cancelled running job's worker returns. A payload
// without a pack ID (an untracked build) needs nothing.
func (s *Service) BuildJobCanceled(ctx context.Context, payload []byte) error {
	var job BuildJobPayload
	if err := json.Unmarshal(payload, &job); err != nil {
		return err
	}
	if job.PackID == "" {
		return nil
	}
	return s.store.FailOfflinePackBuild(context.WithoutCancel(ctx), job.PackID, FailureBuild, s.now())
}

// JobReader reads a job's current row (satisfied by *store.Store).
type JobReader interface {
	Job(ctx context.Context, id store.JobID) (store.Job, error)
}

// RunBuildJob is the job runner handler for BuildJobKind. It runs the build
// and, when the run stopped because an operator cancelled its job while it was
// running, moves the build to failed (CORE-1866). That waits until this worker
// has returned, so a retry under the same pack ID never overlaps a worker
// still writing the pack folder.
func (s *Service) RunBuildJob(ctx context.Context, jobs JobReader, job store.Job) error {
	_, err := s.HandleBuildJob(ctx, job.Payload)
	if err == nil || ctx.Err() == nil {
		return err
	}
	current, readErr := jobs.Job(context.WithoutCancel(ctx), job.ID)
	if readErr != nil {
		return errors.Join(err, readErr)
	}
	if current.Status != store.JobStatusCanceled {
		return err
	}
	return errors.Join(err, s.BuildJobCanceled(ctx, job.Payload))
}

// JobLister lists jobs by status (satisfied by *store.Store).
type JobLister interface {
	ListJobs(ctx context.Context, statuses ...store.JobStatus) ([]store.Job, error)
}

// ReconcileBuilds fails every building row that no live build job will finish
// and returns how many it failed. Run it at startup: a process that stopped
// between an operator cancel of a running build and that worker's return
// leaves the job canceled and the build building, and RunBuildJob never runs
// again for it (CORE-1866). It reads the building rows before the jobs, so a
// retry that restarts a build and queues its job in between is not failed.
func (s *Service) ReconcileBuilds(ctx context.Context, jobs JobLister) (int, error) {
	building, err := s.store.BuildingOfflinePackIDs(ctx)
	if err != nil || len(building) == 0 {
		return 0, err
	}
	live, err := jobs.ListJobs(ctx, store.JobStatusQueued, store.JobStatusRunning, store.JobStatusYielded)
	if err != nil {
		return 0, err
	}
	held := map[store.OfflinePackID]bool{}
	for _, job := range live {
		var payload BuildJobPayload
		if job.Kind == BuildJobKind && json.Unmarshal(job.Payload, &payload) == nil {
			held[payload.PackID] = true
		}
	}
	failed := 0
	for _, id := range building {
		if held[id] {
			continue
		}
		if err := s.store.FailOfflinePackBuild(ctx, id, FailureBuild, s.now()); err != nil {
			return failed, err
		}
		failed++
	}
	return failed, nil
}

// DismissBuild removes the caller's failed build from the list.
func (s *Service) DismissBuild(ctx context.Context, id store.OfflinePackID, profileID store.ProfileID) error {
	return s.store.DeleteFailedOfflinePackBuild(ctx, id, profileID)
}

// CancelBuild removes the caller's build row regardless of state when the user
// cancels an in-progress download.
func (s *Service) CancelBuild(ctx context.Context, id store.OfflinePackID, profileID store.ProfileID) error {
	return s.store.DeleteOfflinePackBuild(ctx, id, profileID)
}

// recordBuildFailure moves a tracked build to failed after its job returned
// err. A cancelled job yields and reruns, so it stays building.
func (s *Service) recordBuildFailure(ctx context.Context, id store.OfflinePackID, err error) error {
	if id == "" || ctx.Err() != nil {
		return nil
	}
	return s.store.FailOfflinePackBuild(context.WithoutCancel(ctx), id, FailureReason(err), s.now())
}

// BuildTranscodeSpeed returns the live FFmpeg encoding speed multiplier (e.g.
// 14.1 for 14.1x) while id is actively transcoding a video item, or 0 otherwise.
func (s *Service) BuildTranscodeSpeed(id store.OfflinePackID) float64 {
	if s == nil || id == "" {
		return 0
	}
	s.speedMu.RLock()
	defer s.speedMu.RUnlock()
	return s.buildSpeeds[id]
}

func (s *Service) setBuildTranscodeSpeed(id store.OfflinePackID, speed float64) {
	if s == nil || id == "" {
		return
	}
	s.speedMu.Lock()
	defer s.speedMu.Unlock()
	if math.IsNaN(speed) || math.IsInf(speed, 0) || speed <= 0 {
		delete(s.buildSpeeds, id)
		return
	}
	if s.buildSpeeds == nil {
		s.buildSpeeds = map[store.OfflinePackID]float64{}
	}
	s.buildSpeeds[id] = speed
}

func (s *Service) clearBuildTranscodeSpeed(id store.OfflinePackID) {
	if s == nil || id == "" {
		return
	}
	s.speedMu.Lock()
	delete(s.buildSpeeds, id)
	s.speedMu.Unlock()
}

const (
	// transcodeProgressWeight weights each video transcode step relative to
	// hashing, encrypting, and final DB sealing (each weight 1), so live
	// percentage tracks the CPU-bound transcode rather than stalling near 0%.
	transcodeProgressWeight = 8
	transcodeWriteInterval  = time.Second
	transcodeWriteMinDelta  = 0.01
)

// buildProgress reports a tracked build's progress as it transcodes, hashes,
// and seals each item. The final step (persisting the sealed pack) is never
// reported, so a building row stays below 1 until it becomes ready.
type buildProgress struct {
	s           *Service
	ctx         context.Context
	id          store.OfflinePackID
	done, total int
}

func (s *Service) newBuildProgress(ctx context.Context, id store.OfflinePackID, sources []packBuildSource) *buildProgress {
	total := 2*len(sources) + 1
	for _, source := range sources {
		if source.transcode {
			total += transcodeProgressWeight
		}
	}
	return &buildProgress{s: s, ctx: ctx, id: id, total: total}
}

func (p *buildProgress) packID() store.OfflinePackID {
	if p == nil {
		return ""
	}
	return p.id
}

// step records one finished hash or encrypt unit of work. Progress is
// best-effort: a failed write never fails the build.
func (p *buildProgress) step() {
	if p == nil || p.id == "" || p.total <= 0 {
		return
	}
	p.done++
	p.record(float64(p.done) / float64(p.total))
}

// transcodeStep records one finished item transcode and clears its live speed.
func (p *buildProgress) transcodeStep() {
	if p == nil || p.id == "" || p.total <= 0 {
		return
	}
	p.s.clearBuildTranscodeSpeed(p.id)
	p.done += transcodeProgressWeight
	p.record(float64(p.done) / float64(p.total))
}

func (p *buildProgress) record(progress float64) {
	if p == nil || p.id == "" {
		return
	}
	_ = p.s.store.SetOfflinePackBuildProgress(p.ctx, p.id, progress, p.s.now())
}

// transcodeReporter returns a callback that streams live FFmpeg transcode
// progress and speed for the next transcode item, or nil for untracked builds.
func (p *buildProgress) transcodeReporter(itemDuration time.Duration) func(TranscodeProgress) {
	if p == nil || p.id == "" || p.total <= 0 {
		return nil
	}
	var (
		mu          sync.Mutex
		base        = p.done
		lastWritten = -1.0
		lastWriteAt time.Time
	)
	return func(tp TranscodeProgress) {
		if tp.Speed > 0 && !math.IsNaN(tp.Speed) && !math.IsInf(tp.Speed, 0) {
			p.s.setBuildTranscodeSpeed(p.id, tp.Speed)
		} else {
			p.s.clearBuildTranscodeSpeed(p.id)
		}
		dur := itemDuration
		if dur <= 0 {
			dur = tp.Duration
		}
		if dur <= 0 || tp.OutTime <= 0 {
			return
		}
		frac := math.Min(1, math.Max(0, float64(tp.OutTime)/float64(dur)))
		overall := (float64(base) + frac*float64(transcodeProgressWeight)) / float64(p.total)
		mu.Lock()
		if overall <= lastWritten {
			mu.Unlock()
			return
		}
		now := time.Now()
		shouldWrite := lastWritten < 0 || overall-lastWritten >= transcodeWriteMinDelta || now.Sub(lastWriteAt) >= transcodeWriteInterval
		if shouldWrite {
			lastWritten = overall
			lastWriteAt = now
		}
		mu.Unlock()
		if shouldWrite {
			p.record(overall)
		}
	}
}
