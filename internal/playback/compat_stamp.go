package playback

import (
	"context"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/streamtoken"
)

// SetCompatDecisionResolver wires the server-owned compatibility decision
// resolver into the shared Stream Token issuer. Issue is the central mint
// choke point, so every playback stream variant receives the same binding.
func (s *Service) SetCompatDecisionResolver(resolver streamtoken.DecisionResolver) {
	if s.tokens != nil {
		s.tokens.SetCompatDecisionResolver(resolver)
	}
}

func (s *Service) compatDecisionContext(ctx context.Context, scope auth.Scope) context.Context {
	if s.tokens == nil {
		return ctx
	}
	grant, err := streamtoken.StampCompatDecision(ctx, streamtoken.Grant{SessionID: scope.SessionID}, s.tokens.CompatDecisionResolver())
	return streamtoken.ContextWithCompatDecisionStamp(ctx, grant, err)
}
