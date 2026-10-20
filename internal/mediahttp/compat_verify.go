package mediahttp

import (
	"context"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/streamtoken"
)

// SetCompatDecisionResolver wires the server-owned compatibility resolver into
// the shared Stream Token service.
func (h *Handler) SetCompatDecisionResolver(resolver streamtoken.DecisionResolver) {
	if h.tokens != nil {
		h.tokens.SetCompatDecisionResolver(resolver)
	}
}

func (h *Handler) verifyCompatDecision(ctx context.Context, authenticated streamtoken.AuthenticatedGrant, scope auth.Scope) (streamtoken.Grant, error) {
	grant, err := h.tokens.VerifyAuthenticated(authenticated, scope)
	if err != nil {
		return streamtoken.Grant{}, err
	}
	if err := h.tokens.VerifyCompatDecision(ctx, grant); err != nil {
		return streamtoken.Grant{}, err
	}
	return grant, nil
}
