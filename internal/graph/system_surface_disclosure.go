package graph

import (
	"context"
	"errors"
	"log/slog"
	"reflect"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// SystemSurfaceDisclosureSnapshotVersion versions the disclosure decision
// carried by GraphQL Navigation Manifest responses. Keep this value aligned
// with the REST response contract in internal/server.
const SystemSurfaceDisclosureSnapshotVersion = "1"

// SystemSurfaceDisclosure is the server-evaluated ADR-0023 §7 decision for
// metadata persisted on an OS-owned surface.
type SystemSurfaceDisclosure struct {
	SnapshotVersion string `json:"snapshotVersion"`
	MetadataAllowed bool   `json:"metadataAllowed"`
}

// NavigationManifestResponse preserves the existing manifest JSON shape and
// adds the versioned disclosure authority consumed by TV snapshot builders.
type NavigationManifestResponse struct {
	manifest.Document
	SystemSurfaceDisclosure SystemSurfaceDisclosure `json:"systemSurfaceDisclosure"`
}

func (h *Handler) navigationManifest(
	ctx context.Context,
	scope auth.Scope,
) (NavigationManifestResponse, error) {
	document, err := h.manifest.Document(ctx, scope)
	if err != nil {
		return NavigationManifestResponse{}, err
	}
	disclosure := SystemSurfaceDisclosure{SnapshotVersion: SystemSurfaceDisclosureSnapshotVersion}
	allowed, err := h.SystemSurfaceManifestMetadataAllowed(ctx, scope, document)
	if err != nil {
		slog.Error("system-surface Navigation Manifest disclosure evaluation failed", "error", err)
	} else {
		disclosure.MetadataAllowed = allowed
	}
	return NavigationManifestResponse{
		Document:                document,
		SystemSurfaceDisclosure: disclosure,
	}, nil
}

// SystemSurfaceMediaMetadataAllowed is the server-authoritative ADR-0023 §7
// disclosure decision for one Media. True means the Media metadata is visible
// to every Profile with Reveal off; every missing or indeterminate state fails
// closed.
func (h *Handler) SystemSurfaceMediaMetadataAllowed(
	ctx context.Context,
	scope auth.Scope,
	mediaID store.MediaID,
) (bool, error) {
	profiles, err := h.systemSurfaceProfiles(ctx, scope.ProfileID)
	if err != nil || len(profiles) == 0 || mediaID == "" {
		return false, err
	}
	return h.mediaMetadataVisibleToAllProfiles(ctx, scope, profiles, mediaID)
}

// SystemSurfaceManifestMetadataAllowed evaluates the exact Navigation Manifest
// that the server is about to return. Passing the materialized document closes
// the response/evaluation TOCTOU gap: the decision can never describe a second
// manifest generated after Profile or Library policy changed.
func (h *Handler) SystemSurfaceManifestMetadataAllowed(
	ctx context.Context,
	scope auth.Scope,
	current manifest.Document,
) (bool, error) {
	profiles, err := h.systemSurfaceProfiles(ctx, scope.ProfileID)
	if err != nil || len(profiles) == 0 {
		return false, err
	}
	if current.ProfileID != string(scope.ProfileID) {
		return false, nil
	}
	return h.manifestMetadataVisibleToAllProfiles(ctx, current, profiles)
}

func (h *Handler) systemSurfaceProfiles(
	ctx context.Context,
	profileID store.ProfileID,
) ([]store.Profile, error) {
	if h.store == nil || profileID == "" {
		return nil, nil
	}
	profiles, err := h.store.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	if !profileSetContains(profiles, profileID) {
		return nil, nil
	}
	return profiles, nil
}

func profileSetContains(profiles []store.Profile, profileID store.ProfileID) bool {
	for _, profile := range profiles {
		if profile.ID == profileID {
			return true
		}
	}
	return false
}

func (h *Handler) mediaMetadataVisibleToAllProfiles(
	ctx context.Context,
	scope auth.Scope,
	profiles []store.Profile,
	mediaID store.MediaID,
) (bool, error) {
	visible, err := h.mediaMetadataVisible(ctx, mediaID, scope.LibraryFilter())
	if err != nil || !visible {
		return false, err
	}
	for _, profile := range profiles {
		profileScope, err := h.noRevealScope(ctx, profile)
		if err != nil {
			return false, err
		}
		visible, err := h.mediaMetadataVisible(ctx, mediaID, profileScope.LibraryFilter())
		if err != nil || !visible {
			return false, err
		}
	}
	return true, nil
}

func (h *Handler) mediaMetadataVisible(
	ctx context.Context,
	mediaID store.MediaID,
	filter store.LibraryFilter,
) (bool, error) {
	media, err := h.store.Media(ctx, mediaID, filter)
	if err == nil {
		return !h.librarySealed(media.LibraryID), nil
	}
	var validation store.ValidationError
	if errors.As(err, &validation) {
		return false, nil
	}
	return false, err
}

func (h *Handler) manifestMetadataVisibleToAllProfiles(
	ctx context.Context,
	current manifest.Document,
	profiles []store.Profile,
) (bool, error) {
	if h.manifest == nil {
		return false, nil
	}
	if h.manifestContainsSealedLibrary(current) {
		return false, nil
	}
	metadata := disclosedManifestMetadata(current)
	for _, profile := range profiles {
		profileScope, err := h.noRevealScope(ctx, profile)
		if err != nil {
			return false, err
		}
		document, err := h.manifest.Document(ctx, profileScope)
		if err != nil {
			return false, err
		}
		if h.manifestContainsSealedLibrary(document) {
			return false, nil
		}
		if !reflect.DeepEqual(metadata, disclosedManifestMetadata(document)) {
			return false, nil
		}
	}
	return true, nil
}

type systemSurfaceManifestMetadata struct {
	Libraries  []manifest.LibrarySummary
	Navigation []manifest.NavigationNode
	Layouts    []manifest.Layout
	Theme      manifest.Theme
}

func disclosedManifestMetadata(document manifest.Document) systemSurfaceManifestMetadata {
	return systemSurfaceManifestMetadata{
		Libraries:  document.Libraries,
		Navigation: document.Navigation,
		Layouts:    document.Layouts,
		Theme:      document.Theme,
	}
}

func (h *Handler) manifestContainsSealedLibrary(document manifest.Document) bool {
	for _, library := range document.Libraries {
		if h.librarySealed(store.LibraryID(library.ID)) {
			return true
		}
	}
	return false
}

// noRevealScope reconstructs a Profile's server read policy without inheriting
// anything from the requesting session. Disabled Profiles remain in the set:
// the contract says every Profile, and retaining them is the conservative
// disclosure posture for persisted system surfaces.
func (h *Handler) noRevealScope(ctx context.Context, profile store.Profile) (auth.Scope, error) {
	scope := auth.Scope{
		ProfileID:     profile.ID,
		Profile:       profile.Kind,
		LibraryIDs:    append([]store.LibraryID(nil), profile.Library...),
		Certification: h.disclosureCertificationConstraints(profile.CertificationCeilings),
	}
	if profile.Kind == store.ProfileManaged {
		granted, err := h.store.GrantedMediaIDsForProfile(ctx, profile.ID)
		if err != nil {
			return auth.Scope{}, err
		}
		scope.GrantedMediaIDs = granted
	}
	return scope, nil
}

func (h *Handler) disclosureCertificationConstraints(
	ceilings []store.ProfileCertificationCeiling,
) []store.CertificationConstraint {
	constraints := make([]store.CertificationConstraint, 0, len(ceilings))
	for _, ceiling := range ceilings {
		constraint := store.CertificationConstraint{
			PluginID:     ceiling.PluginID,
			AllowUnrated: ceiling.AllowUnrated,
			Locked:       ceiling.FilterVisibility == "locked",
		}
		if h.plugins != nil {
			if bundle, ok := h.plugins.Bundle(plugin.ID(ceiling.PluginID)); ok {
				for i, value := range bundle.CertificationScale {
					if value == ceiling.Ceiling {
						constraint.Allowed = append([]string(nil), bundle.CertificationScale[:i+1]...)
						break
					}
				}
			}
		}
		constraints = append(constraints, constraint)
	}
	return constraints
}
