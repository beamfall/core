package server

import (
	"context"
	"log/slog"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
)

// SystemSurfaceDisclosureSnapshotVersion is the version of the disclosure
// decision nested on client system-surface responses.
const SystemSurfaceDisclosureSnapshotVersion = "1"

// SystemSurfaceDisclosure is the server-evaluated ADR-0023 §7 disclosure
// decision for the response subject. MetadataAllowed is true only when the
// subject is visible to every Profile with Reveal off. Missing/failed authority
// is represented as false so old or partially wired servers fail closed.
type SystemSurfaceDisclosure struct {
	SnapshotVersion string `json:"snapshotVersion"`
	MetadataAllowed bool   `json:"metadataAllowed"`
}

// ManifestResponse is the /api/manifest wire response. Document remains
// embedded so the existing JSON shape is unchanged apart from the additive,
// required disclosure decision.
type ManifestResponse struct {
	manifest.Document
	SystemSurfaceDisclosure SystemSurfaceDisclosure `json:"systemSurfaceDisclosure"`
}

// PlaybackInfoResponse is the /api/playback-info wire response. InfoResponse
// remains embedded so existing clients continue to decode the original fields.
type PlaybackInfoResponse struct {
	playback.InfoResponse
	SystemSurfaceDisclosure SystemSurfaceDisclosure `json:"systemSurfaceDisclosure"`
}

type systemSurfaceDisclosureProvider interface {
	SystemSurfaceManifestMetadataAllowed(context.Context, auth.Scope, manifest.Document) (bool, error)
	SystemSurfaceMediaMetadataAllowed(context.Context, auth.Scope, store.MediaID) (bool, error)
}

func (s *Server) manifestSystemSurfaceDisclosure(
	ctx context.Context,
	scope auth.Scope,
	document manifest.Document,
) SystemSurfaceDisclosure {
	disclosure := SystemSurfaceDisclosure{SnapshotVersion: SystemSurfaceDisclosureSnapshotVersion}
	provider, ok := s.graph.(systemSurfaceDisclosureProvider)
	if !ok {
		return disclosure
	}
	allowed, err := provider.SystemSurfaceManifestMetadataAllowed(ctx, scope, document)
	if err != nil {
		slog.Error("system-surface manifest disclosure evaluation failed", "error", err)
		return disclosure
	}
	disclosure.MetadataAllowed = allowed
	return disclosure
}

func (s *Server) mediaSystemSurfaceDisclosure(
	ctx context.Context,
	scope auth.Scope,
	mediaID store.MediaID,
) SystemSurfaceDisclosure {
	disclosure := SystemSurfaceDisclosure{SnapshotVersion: SystemSurfaceDisclosureSnapshotVersion}
	provider, ok := s.graph.(systemSurfaceDisclosureProvider)
	if !ok {
		return disclosure
	}
	allowed, err := provider.SystemSurfaceMediaMetadataAllowed(ctx, scope, mediaID)
	if err != nil {
		slog.Error("system-surface Media disclosure evaluation failed", "error", err)
		return disclosure
	}
	disclosure.MetadataAllowed = allowed
	return disclosure
}
