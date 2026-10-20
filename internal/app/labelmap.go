package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// ApplyLabelMaps resolves one plugin's AI labels and applies surviving targets
// through the existing store admission seams.
func ApplyLabelMaps(ctx context.Context, st *store.Store, bundle plugin.Bundle, mediaID store.MediaID, labels []plugin.Label, fallbackFloor float64, filter store.LibraryFilter) (plugin.LabelMapResult, error) {
	if _, err := st.Media(ctx, mediaID, filter); err != nil {
		return plugin.LabelMapResult{}, err
	}
	result := plugin.ResolveLabelMaps(bundle, labels, fallbackFloor)
	now := time.Now().UTC()
	run, err := beginLabelMapRun(ctx, st, bundle, result, now)
	if err != nil {
		return result, err
	}
	for _, target := range result.Tags {
		pending := labelMapPendingTag(bundle.ID, mediaID, labelMapTagKind(target), target.Tag, now, run.ID)
		if err := st.ProposePendingAITag(ctx, pending); err != nil {
			return result, err
		}
		if err := st.PromotePendingAITagInRun(ctx, pending.ID, run.ID, filter); err != nil {
			if labelMapPromotionAlreadySettled(err) {
				continue
			}
			return result, err
		}
	}
	for _, target := range result.Entities {
		pending := labelMapPendingTag(bundle.ID, mediaID, "entity:"+target.Kind+":"+target.Relation, labelMapEntityName(target), now, run.ID)
		if err := st.ProposePendingAITag(ctx, pending); err != nil {
			return result, err
		}
	}
	if result.Unmatched > 0 {
		err := st.RecordEnrichmentFailure(ctx, mediaID, labelMapStatusKind(bundle.ID), fmt.Sprintf("unmatched labels: %d", result.Unmatched))
		return result, err
	}
	return result, nil
}

func beginLabelMapRun(ctx context.Context, st *store.Store, bundle plugin.Bundle, result plugin.LabelMapResult, now time.Time) (store.EnrichmentRun, error) {
	if len(result.Tags)+len(result.Entities) == 0 {
		return store.EnrichmentRun{}, nil
	}
	return st.BeginEnrichmentRun(ctx, "labelmap:"+string(bundle.ID), now)
}

func labelMapStatusKind(id plugin.ID) store.EmbeddingKind {
	return store.EmbeddingKind("labelmap:" + string(id))
}

func labelMapPendingTag(bundleID plugin.ID, mediaID store.MediaID, kind, value string, now time.Time, runID string) store.PendingAITag {
	return store.PendingAITag{
		ID:         labelMapProposalID(bundleID, mediaID, kind, value),
		MediaID:    mediaID,
		TagKind:    kind,
		Value:      value,
		ProposedBy: "labelmap:" + string(bundleID),
		ProposedAt: now,
		RunID:      runID,
	}
}

func labelMapTagKind(target plugin.LabelTarget) string {
	return "tag:" + plugin.NormalizeLabelMapLabel(target.Tag)
}

func labelMapEntityName(target plugin.LabelTarget) string {
	if strings.TrimSpace(target.EntityName) != "" {
		return strings.TrimSpace(target.EntityName)
	}
	return plugin.NormalizeLabelMapLabel(target.Label)
}

func labelMapPromotionAlreadySettled(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "pending tag not found") ||
		strings.Contains(msg, "pending tag is not awaiting approval") ||
		strings.Contains(msg, "pending tag belongs to another enrichment run")
}

func labelMapProposalID(bundleID plugin.ID, mediaID store.MediaID, kind, value string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		string(bundleID),
		string(mediaID),
		kind,
		value,
	}, "\x00")))
	return "labelmap-" + hex.EncodeToString(sum[:12])
}
