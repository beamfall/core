package graph

import (
	"context"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// TransferProfileOwnerResult echoes the Profile that holds the owner flag after
// a transfer. IsOwner is always true on success — the field makes
// the post-state explicit for the caller.
type TransferProfileOwnerResult struct {
	ProfileID string `json:"profileID"`
	IsOwner   bool   `json:"isOwner"`
}

// transferProfileOwner moves the single owner flag to the named adult Profile.
// It is Admin-Session-gated in the execute() dispatch: once an
// owner is designated, only that owner can hold an Admin Session, so this is the
// owner deliberately handing ownership to another adult. The store enforces the
// single-owner invariant and the adult-only rule (a managed or unknown target is
// refused there), so this resolver only validates the argument and audits the
// transfer (naming the new owner — an operator audit trail, not content).
func (h *Handler) transferProfileOwner(ctx context.Context, scope auth.Scope, req request) (TransferProfileOwnerResult, error) {
	target := trimmedVariable(req, "profileID")
	if target == "" {
		return TransferProfileOwnerResult{}, clientError("profileID is required")
	}
	if err := h.store.SetProfileOwner(ctx, store.ProfileID(target)); err != nil {
		return TransferProfileOwnerResult{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityOwnerTransfer, "profile", target, ""); err != nil {
		return TransferProfileOwnerResult{}, err
	}
	return TransferProfileOwnerResult{ProfileID: target, IsOwner: true}, nil
}
