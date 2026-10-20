package server

import (
	"context"
	"errors"

	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/streamtoken"
)

var (
	errCompatStreamTokenDecision          = errors.New("stream token compatibility decision unavailable")
	errCompatInvalidationStoreUnavailable = errors.New("compatibility invalidation store is required")
)

type compatStreamTokenResolver struct {
	server *Server
}

var _ streamtoken.DecisionResolver = compatStreamTokenResolver{}

func (s *Server) bindCompatStreamTokens() {
	// Structural recovery of the durable tombstone seam from a dependency the
	// composition root has already supplied (the same idiom New uses for the
	// artwork importer). The decision registry must own its journal before the
	// first request, and the compatibility surface is constructed inside New, so
	// there is no later setter to wait for. A missing store is still attached as
	// an unavailable journal: startup logs the load failure once and every
	// decision resolve fails closed instead of silently downgrading to memory.
	if s.compat != nil {
		invalidations, _ := s.jobStats.(compatInvalidationStore)
		var refusals *compatRefusalFileJournal
		if s.compatRefusalDir != "" {
			refusals = resolveCompatRefusalFileJournal(s.compatRefusalDir)
		}
		s.compat.attachInvalidationJournal(
			compatInvalidationJournal{store: invalidations, refusals: refusals},
			streamtoken.MaxStreamTokenTTL,
		)
	}
	resolver := compatStreamTokenResolver{server: s}
	if s.playback != nil {
		s.playback.SetCompatDecisionResolver(resolver)
	}
	if s.media != nil {
		s.media.SetCompatDecisionResolver(resolver)
	}
}

func (r compatStreamTokenResolver) Resolve(ctx context.Context, subject string, want streamtoken.DecisionWant) (string, bool, error) {
	if r.server == nil || r.server.compat == nil || r.server.auth == nil || want.SessionID == "" {
		return "", false, errCompatStreamTokenDecision
	}
	scope, err := r.server.auth.Scope(ctx, want.SessionID)
	if err != nil {
		return "", false, err
	}
	if compatSubject(scope) != subject || r.server.compat.isRefused(subject) {
		return "", false, errCompatStreamTokenDecision
	}
	policy := r.server.compat.snapshot()
	record, err := r.server.compat.registry.Resolve(subject, capabilities.ResolveKey{
		Subject:         subject,
		Distribution:    capabilities.Distribution(scope.DistributionClass),
		AuthFingerprint: compatAuthFingerprint(scope),
		Generation:      policy.Generation,
	}, r.server.compat.now())
	// The two states this ticket exists to separate. ErrInvalidated means the
	// server KNOWS this subject was refused — a fact that now survives a restart —
	// so it is surfaced as an error and the verify path rejects the token, stamped
	// or not. ErrNoDecision means the server has genuinely never heard of the
	// subject, which keeps the established unbound behavior; mapping that to an
	// error instead would reject every in-flight token at every restart.
	if errors.Is(err, capabilities.ErrInvalidated) {
		return "", false, err
	}
	if errors.Is(err, capabilities.ErrNoDecision) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !compatServable(record.Result) || record.DecisionID == "" {
		return "", false, errCompatStreamTokenDecision
	}
	return record.DecisionID, true, nil
}
