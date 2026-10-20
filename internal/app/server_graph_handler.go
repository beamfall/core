package app

import (
	"context"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/observability"
	"github.com/beamfall/core/internal/server"
)

// serverGraphHandler adapts graph's audit outcome type at the composition root.
type serverGraphHandler struct {
	*graph.Handler
}

func (h serverGraphHandler) FinishAuditedCommand(
	ctx context.Context,
	scope auth.Scope,
	operation, operationID string,
	outcome server.AuditOutcome,
) error {
	return h.Handler.FinishAuditedCommand(
		ctx,
		scope,
		operation,
		operationID,
		observability.AuditOutcome(outcome),
	)
}
