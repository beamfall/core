package playback

import (
	"context"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

type decisionLookup interface {
	Media(context.Context, store.MediaID, store.LibraryFilter) (store.Media, error)
	Profile(context.Context, store.ProfileID) (store.Profile, error)
	LibraryRestricted(context.Context, store.LibraryID) (bool, error)
}

type decisionGrantsKey struct{}

type decisionGrants struct {
	records    []func(context.Context)
	observe    func(decision)
	restricted *bool
}

func unavailableDecision() decision {
	return decision{verdict: "unavailable", reason: "media-unavailable", streams: []StreamChoice{}}
}

// Reuse the restriction value from the token when assembling the response.
func (s *Service) decisionRestricted(ctx context.Context, library store.LibraryID) (bool, error) {
	batch := ctx.Value(decisionGrantsKey{}).(*decisionGrants)
	if batch.restricted != nil {
		return *batch.restricted, nil
	}
	restricted, err := s.decisionLookup.LibraryRestricted(ctx, library)
	if err == nil {
		batch.restricted = &restricted
	}
	return restricted, err
}

func (s *Service) issueDecisionGrant(ctx context.Context, grant streamtoken.Grant) (string, error) {
	batch, _ := ctx.Value(decisionGrantsKey{}).(*decisionGrants)
	if batch == nil {
		return s.tokens.Issue(ctx, grant)
	}
	token, record, err := s.tokens.PrepareIssue(ctx, grant)
	if err == nil {
		batch.records = append(batch.records, record)
	}
	return token, err
}
