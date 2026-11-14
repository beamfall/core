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

// rebindVerifiedBuild records the verified build a native device now runs
// (CORE-1393). Enrollment maps a device to the build it enrolled with, so a
// native app update would otherwise present a build with no mapping and be
// refused as unknown-build until the device re-paired. Devices enrolled before
// the mapping existed have no row at all and recover the same way.
//
// Only a native device credential qualifies: scope.DeviceID comes from the
// device's own authenticated session, never from the request body, and only
// that device's row is written, so a sibling device gains nothing. The build
// must pass EnrollmentBindingFor against the device's server-held enrollment
// (known usable catalog record, surface including the device's pinned surface,
// artifact digest, rollback floor, role and distribution), exactly as at
// enrollment. The catalog's rollback floor is the downgrade bound: a device may
// move back to an older build that is still at or above the floor, so one that
// upgraded to a build its server cannot serve yet can reinstall the build it
// ran before. A verified build is recorded even when the handshake is then
// refused for another reason, so the refusal names that reason instead of
// unknown-build. A build that does not verify leaves the mapping unchanged.
// As at enrollment, a mapping that cannot be read or written is logged and
// left as it was, and the handshake is then refused as an unknown build rather
// than failed.
func (s *Server) rebindVerifiedBuild(ctx context.Context, scope auth.Scope, contract capabilities.ClientContract, enrollment capabilities.Enrollment, catalog capabilities.BuildCatalog) {
	if err := s.rebindVerifiedBuildOnce(ctx, scope, contract, enrollment, catalog); err != nil {
		slog.WarnContext(ctx, "handshake build mapping failed", "device", scope.DeviceID, "surface", contract.Surface, "build", contract.BuildID, "err", err)
	}
}

func (s *Server) rebindVerifiedBuildOnce(ctx context.Context, scope auth.Scope, contract capabilities.ClientContract, enrollment capabilities.Enrollment, catalog capabilities.BuildCatalog) error {
	if s.deviceBuilds == nil || scope.DeviceID == "" || scope.SessionID == "" || scope.APITokenID != "" || scope.APIKeyID != "" {
		return nil
	}
	contract = capabilities.NormalizeClientContract(contract)
	surface := capabilities.Surface(contract.Surface)
	if !capabilities.IsNativeSurface(surface) {
		return nil
	}
	bindings, err := s.deviceBuilds.DeviceBuildBindings(ctx, scope.DeviceID)
	if err != nil {
		return err
	}
	for _, bound := range bindings {
		if capabilities.Surface(bound.Surface) == surface && bound.BuildID == contract.BuildID {
			return nil
		}
	}
	binding, finding := capabilities.EnrollmentBindingFor(contract, enrollment, catalog)
	if finding != capabilities.ProvenanceVerified {
		return nil
	}
	if err := s.deviceBuilds.BindDeviceBuild(ctx, scope.DeviceID, store.DeviceSurface(binding.Surface), binding.BuildID, s.compat.now()); err != nil {
		return err
	}
	slog.InfoContext(ctx, "handshake build rebound", "device", scope.DeviceID, "surface", surface, "build", binding.BuildID)
	return nil
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
