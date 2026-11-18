package identify

import (
	"context"
	"errors"
	"fmt"

	"github.com/beamfall/core/internal/store"
)

// ErrNothingToRefresh reports that a Media item has no remembered
// identification candidate, so there is no lookup to re-run; Fix match is the
// way to identify it.
var ErrNothingToRefresh = errors.New("identify: no identification to refresh")

// ErrScraperUnavailable reports that the plugin or scraper that identified a
// Media item is no longer active, so its candidate cannot be looked up again.
var ErrScraperUnavailable = errors.New("identify: scraper unavailable")

// ProvenanceReader reads the scraper and candidate that last identified a
// Media item. The store implements it next to SetMediaMetadataProvenance; a
// ProvenanceStore without it makes every refresh ErrNothingToRefresh.
type ProvenanceReader interface {
	MediaMetadataProvenance(ctx context.Context, mediaID store.MediaID) (store.MediaMetadataProvenance, bool, error)
}

// Refresh re-runs the lookup that last identified mediaID and applies it
// through Apply at operator rank, so locked fields and locked artwork slots
// keep their values. Re-applying the remembered candidate also keeps a pinned
// match: the candidate is the one the operator chose. Visibility gating
// belongs to the caller.
func (s *Service) Refresh(ctx context.Context, mediaID store.MediaID) error {
	if mediaID == "" {
		return errors.New("identify: media ID is required")
	}
	reader, ok := s.provenance.(ProvenanceReader)
	if !ok {
		return ErrNothingToRefresh
	}
	record, found, err := reader.MediaMetadataProvenance(ctx, mediaID)
	if err != nil {
		return err
	}
	if !found || record.CandidateID == "" {
		return ErrNothingToRefresh
	}
	if _, _, err := s.spec(record.PluginID, record.ScraperID); err != nil {
		return fmt.Errorf("%w: %w", ErrScraperUnavailable, err)
	}
	return s.Apply(ctx, mediaID, record.PluginID, record.ScraperID, record.CandidateID)
}
