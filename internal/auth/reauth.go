package auth

import (
	"context"

	"github.com/beamfall/core/internal/store"
)

// VerifyAdminReauth re-verifies the owner's server admin password as a step-up
// check for an already signed-in Manager session (ADR-0243 D2, ADR-0085). Even
// when an Admin Session is active, destructive or security-elevating actions
// may require a fresh password proof on the caller's throttled admin lane.
func (s *Service) VerifyAdminReauth(ctx context.Context, scope Scope, password string) error {
	if scope.SessionID == "" {
		return ErrAdminPassword
	}
	if scope.DeviceRole != store.DeviceRoleManager {
		return ErrNotManager
	}
	now := s.now()
	attemptKey := adminAttemptKey(scope)
	if retryAt, err := s.store.AuthBackoffUntil(ctx, adminAttemptAction, attemptKey, now, adminBackoff); err != nil {
		return err
	} else if !retryAt.IsZero() {
		return ErrAdminLocked
	}
	if err := s.verifyAdminReauth(ctx, scope, password, attemptKey, now); err != nil {
		return err
	}
	s.recordAdminAttempt(ctx, attemptKey, now, true)
	return nil
}
