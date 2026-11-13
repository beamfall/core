package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/store"
)

// enrollDeviceRequest is the native enrollment body. ClientContract is the
// optional Client Contract of the first-party build enrolling; when it verifies
// against the release build catalog for the new device's role and distribution
// class, enrollment records the exact device-to-build mapping ADR-0063
// provenance requires. Without it, or when it does not verify, the device still
// enrolls but no build is mapped to it, so its compatibility handshake is
// refused as an unknown build.
type enrollDeviceRequest struct {
	pairing.EnrollRequest
	ClientContract *capabilities.ClientContract `json:"clientContract,omitempty"`
}

// deviceBuildStore keeps the exact device-to-build mapping (ADR-0063).
type deviceBuildStore interface {
	BindDeviceBuild(context.Context, store.DeviceID, store.DeviceSurface, string, time.Time) error
	DeviceBuildBindings(context.Context, store.DeviceID) ([]store.DeviceBuildBinding, error)
}

// bindEnrolledBuild records the device-to-build mapping for a device that has
// just enrolled. The enrollment it verifies is derived from server-held state,
// as at handshake: the enrolled session, and the device's stored role and
// distribution class. Only a build that fully verifies apart from the mapping
// itself is recorded. A build that does not verify is logged and left unmapped;
// an error means the mapping could not be read or written, and the caller
// leaves the device enrolled but unmapped.
func (s *Server) bindEnrolledBuild(ctx context.Context, resp pairing.EnrollResponse, contract capabilities.ClientContract) error {
	if s.deviceBuilds == nil || s.deviceSurfaces == nil {
		return errors.New("device build store unavailable")
	}
	device, err := s.deviceSurfaces.Device(ctx, store.DeviceID(resp.DeviceID))
	if err != nil {
		return err
	}
	scope := auth.Scope{
		SessionID:         store.SessionID(resp.SessionID),
		DeviceID:          device.ID,
		DeviceRole:        device.Role,
		DistributionClass: device.DistributionClass,
	}
	binding, finding := capabilities.EnrollmentBindingFor(contract, compatEnrollment(scope), s.compat.snapshot().Builds)
	if finding != capabilities.ProvenanceVerified {
		slog.InfoContext(ctx, "enrollment build not mapped", "device", device.ID, "surface", contract.Surface, "build", contract.BuildID, "finding", string(finding))
		return nil
	}
	return s.deviceBuilds.BindDeviceBuild(ctx, device.ID, store.DeviceSurface(binding.Surface), binding.BuildID, s.compat.now())
}

// deviceBuildPolicy returns policy with the caller's own device-to-build
// mappings added to its build catalog for one evaluation. Only the scope's
// device's rows are added, keyed to this scope's enrollment, so a sibling
// device or session with the same role and distribution gains nothing. The
// installed policy and its generation are untouched.
func (s *Server) deviceBuildPolicy(ctx context.Context, scope auth.Scope, policy capabilities.Policy) (capabilities.Policy, error) {
	if s.deviceBuilds == nil || scope.DeviceID == "" {
		return policy, nil
	}
	bindings, err := s.deviceBuilds.DeviceBuildBindings(ctx, scope.DeviceID)
	if err != nil || len(bindings) == 0 {
		return policy, err
	}
	subject := compatSubject(scope)
	policy.Builds = policy.Builds.Clone()
	for _, binding := range bindings {
		policy.Builds.EnrollmentBindings = append(policy.Builds.EnrollmentBindings, capabilities.EnrollmentBuildBinding{
			EnrollmentID: subject,
			Surface:      capabilities.Surface(binding.Surface),
			BuildID:      binding.BuildID,
		})
	}
	return policy, nil
}
