package offlinepack

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"

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
	})
	if err != nil {
		return store.OfflinePackBuild{}, nil, err
	}
	restarted, err := s.store.RestartOfflinePackBuild(ctx, id, scope.ProfileID, payload, s.now())
	if err != nil {
		return store.OfflinePackBuild{}, nil, err
	}
	build, err = s.store.OfflinePackBuildByID(ctx, id, scope.ProfileID)
	if err != nil {
		return store.OfflinePackBuild{}, nil, err
	}
	if !restarted {
		return build, nil, nil
	}
	return build, payload, nil
}

// FailBuild marks a building row failed with a closed reason code, for a
// retry whose job could not be queued.
func (s *Service) FailBuild(ctx context.Context, id store.OfflinePackID, reason string) error {
	return s.store.FailOfflinePackBuild(ctx, id, reason, s.now())
}

// DismissBuild removes the caller's failed build from the list.
func (s *Service) DismissBuild(ctx context.Context, id store.OfflinePackID, profileID store.ProfileID) error {
	return s.store.DeleteFailedOfflinePackBuild(ctx, id, profileID)
}

// recordBuildFailure moves a tracked build to failed after its job returned
// err. A cancelled job yields and reruns, so it stays building.
func (s *Service) recordBuildFailure(ctx context.Context, id store.OfflinePackID, err error) error {
	if id == "" || ctx.Err() != nil {
		return nil
	}
	return s.store.FailOfflinePackBuild(context.WithoutCancel(ctx), id, FailureReason(err), s.now())
}

// buildProgress reports a tracked build's coarse progress as it transcodes,
// hashes and seals each item. The final step (persisting the sealed pack) is
// never reported, so a building row stays below 1 until it becomes ready.
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
			total++
		}
	}
	return &buildProgress{s: s, ctx: ctx, id: id, total: total}
}

// step records one finished unit of work. Progress is best-effort: a failed
// write never fails the build.
func (p *buildProgress) step() {
	if p == nil || p.id == "" {
		return
	}
	p.done++
	_ = p.s.store.SetOfflinePackBuildProgress(p.ctx, p.id, float64(p.done)/float64(p.total), p.s.now())
}
