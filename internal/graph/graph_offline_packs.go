package graph

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/offlinepack"
	"github.com/beamfall/core/internal/store"
)

// OfflinePackInfo is the wire view of an Offline Pack (ADR-0098 area 6). It
// carries the opaque target identity, lifecycle state, item count, and reconnect
// progress — never the manifest contents (those are fetched as bytes through a
// Pack-subject Stream Token, not the GraphQL surface). Domain-agnostic: the
// target kind is whatever resolver was registered, never a named music type in
// core code.
type OfflinePackInfo struct {
	ID         string `json:"id"`
	TargetKind string `json:"targetKind"`
	TargetRef  string `json:"targetRef"`
	State      string `json:"state"`
	ItemCount  int    `json:"itemCount"`
	BytesTotal int64  `json:"bytesTotal"`
	BytesReady int64  `json:"bytesReady"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	ExpiresAt  string `json:"expiresAt"`
	// ManifestToken / KeyToken carry the distinct signed Offline Pack grant the
	// client uses to fetch the manifest and device-wrapped key material.
	// They are minted only for a servable pack the caller owns; a revoked/expired
	// pack returns empty token fields (the byte routes would deny them anyway).
	ManifestToken string `json:"manifestToken,omitempty"`
	KeyToken      string `json:"keyToken,omitempty"`
}

func offlinePackInfo(pack store.OfflinePack) OfflinePackInfo {
	info := OfflinePackInfo{
		ID:         string(pack.ID),
		TargetKind: pack.TargetKind,
		TargetRef:  pack.TargetRef,
		State:      string(pack.State),
		ItemCount:  pack.ItemCount,
		BytesTotal: pack.BytesTotal,
		BytesReady: pack.BytesReady,
		CreatedAt:  formatOptionalTime(pack.CreatedAt),
		UpdatedAt:  formatOptionalTime(pack.UpdatedAt),
	}
	if pack.ExpiresAt != nil {
		info.ExpiresAt = formatOptionalTime(*pack.ExpiresAt)
	}
	return info
}

// withPackTokens mints the Offline Pack grant for a servable, owned
// pack and attaches them to the info. Best-effort: a mint failure (e.g. the pack
// just became unservable) leaves the token fields empty rather than failing the
// whole read.
func (h *Handler) withPackTokens(ctx context.Context, scope auth.Scope, pack store.OfflinePack, info OfflinePackInfo) OfflinePackInfo {
	if pack.State != store.OfflinePackReady {
		return info
	}
	if token, err := h.offlinePacks.IssuePackGrant(ctx, scope, pack.ID); err == nil {
		info.ManifestToken = token
		info.KeyToken = token
	}
	return info
}

// offlinePacks lists the caller's Offline Packs.
func (h *Handler) offlinePacks_(ctx context.Context, scope auth.Scope) ([]OfflinePackInfo, error) {
	if h.offlinePacks == nil {
		return nil, clientError("offline packs unavailable")
	}
	packs, err := h.offlinePacks.Packs(ctx, scope.ProfileID)
	if err != nil {
		return nil, err
	}
	out := make([]OfflinePackInfo, 0, len(packs))
	for _, p := range packs {
		out = append(out, h.withPackTokens(ctx, scope, p, offlinePackInfo(p)))
	}
	return out, nil
}

// createOfflinePack builds a pack from an opaque (targetKind, targetRef). The
// registered resolver (generic "media" or a Music resolver) translates the
// target into the visible Media, applies Reveal/Sealed filtering, and freezes
// the list. A target that resolves to no visible Media, or an unknown kind,
// errors and creates no pack.
func (h *Handler) createOfflinePack(ctx context.Context, scope auth.Scope, req request) (OfflinePackInfo, error) {
	if h.offlinePacks == nil {
		return OfflinePackInfo{}, clientError("offline packs unavailable")
	}
	if h.store == nil || h.jobRunner == nil {
		return OfflinePackInfo{}, clientError("offline pack job runner unavailable")
	}
	kind := trimmedVariable(req, "targetKind")
	if kind == "" {
		return OfflinePackInfo{}, clientError("targetKind is required")
	}
	build := offlinepack.BuildPackRequest{
		TargetKind:       kind,
		TargetRef:        stringVar(req, "targetRef"),
		SelectedVersions: selectedVersionsFromVariables(req),
	}
	var err error
	build.DevicePublicKey, err = decodeOfflinePackBytes(req, "devicePublicKey")
	if err != nil {
		return OfflinePackInfo{}, err
	}
	build.DeviceAttestation, err = decodeOfflinePackBytes(req, "deviceAttestation")
	if err != nil {
		return OfflinePackInfo{}, err
	}
	payload, err := offlinepack.BuildJobPayloadFor(scope, build)
	if err != nil {
		return OfflinePackInfo{}, err
	}
	started := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := h.store.EnqueueJob(ctx, store.Job{
		Kind:      offlinepack.BuildJobKind,
		Class:     store.JobClassInteractive,
		DedupeKey: offlinePackBuildDedupeKey(scope.ProfileID, kind, build.TargetRef),
		Payload:   payload,
		CreatedAt: started,
	}); err != nil {
		return OfflinePackInfo{}, err
	}
	if _, err := h.jobRunner.RunOne(ctx, []store.JobClass{store.JobClassInteractive}, started); err != nil {
		return OfflinePackInfo{}, err
	}
	pack, err := h.latestOfflinePack(ctx, scope.ProfileID, kind, build.TargetRef, started)
	if err != nil {
		return OfflinePackInfo{}, err
	}
	return h.withPackTokens(ctx, scope, pack, offlinePackInfo(pack)), nil
}

func decodeOfflinePackBytes(req request, name string) ([]byte, error) {
	value := trimmedVariable(req, name)
	if value == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, clientError(name + " must be unpadded base64url")
	}
	return decoded, nil
}

func selectedVersionsFromVariables(req request) map[store.MediaID]string {
	raw, ok := req.Variables["selectedVersions"].(map[string]any)
	if !ok {
		return nil
	}
	selected := make(map[store.MediaID]string, len(raw))
	for id, value := range raw {
		version, ok := value.(string)
		if !ok {
			continue
		}
		selected[store.MediaID(id)] = version
	}
	return selected
}

func offlinePackBuildDedupeKey(profileID store.ProfileID, kind, ref string) string {
	return string(profileID) + "\x1f" + kind + "\x1f" + ref
}

func (h *Handler) latestOfflinePack(ctx context.Context, profileID store.ProfileID, kind, ref string, since time.Time) (store.OfflinePack, error) {
	packs, err := h.offlinePacks.Packs(ctx, profileID)
	if err != nil {
		return store.OfflinePack{}, err
	}
	for _, pack := range packs {
		if pack.TargetKind == kind && pack.TargetRef == ref && !pack.CreatedAt.Before(since) {
			return pack, nil
		}
	}
	return store.OfflinePack{}, clientError("offline pack job did not produce a pack")
}

// revokeOfflinePack revokes a caller-owned pack. The serving layer then denies
// further bytes/key material for it.
func (h *Handler) revokeOfflinePack(ctx context.Context, scope auth.Scope, req request) (OfflinePackInfo, error) {
	if h.offlinePacks == nil {
		return OfflinePackInfo{}, clientError("offline packs unavailable")
	}
	id := store.OfflinePackID(trimmedVariable(req, "id"))
	if id == "" {
		return OfflinePackInfo{}, clientError("id is required")
	}
	pack, err := h.offlinePacks.Revoke(ctx, id, scope.ProfileID)
	if err != nil {
		return OfflinePackInfo{}, err
	}
	return offlinePackInfo(pack), nil
}

// syncOfflinePackProgress records client-side reconnect progress for one
// caller-owned pack. It is self-scoped like create/list/revoke; cross-Profile
// ids read as not-found through the service.
func (h *Handler) syncOfflinePackProgress(ctx context.Context, scope auth.Scope, req request) (OfflinePackInfo, error) {
	if h.offlinePacks == nil {
		return OfflinePackInfo{}, clientError("offline packs unavailable")
	}
	id := store.OfflinePackID(trimmedVariable(req, "id"))
	if id == "" {
		return OfflinePackInfo{}, clientError("id is required")
	}
	pack, err := h.offlinePacks.SyncProgress(ctx, id, scope.ProfileID, int64(intVar(req, "bytesReady")), int64(intVar(req, "bytesTotal")))
	if err != nil {
		return OfflinePackInfo{}, err
	}
	return h.withPackTokens(ctx, scope, pack, offlinePackInfo(pack)), nil
}
