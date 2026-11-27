package graph

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
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
	// Progress is the server-side build progress from 0 to 1 (CORE-1743, CORE-3548).
	// A background build reports it while building and keeps it when it fails;
	// a sealed pack reports 1.
	Progress float64 `json:"progress"`
	// TranscodeSpeed is the live FFmpeg encoding speed multiplier (e.g. 14.1
	// for 14.1x real-time) while a background build is actively transcoding a
	// video item, and omitted otherwise (CORE-3548).
	TranscodeSpeed float64 `json:"transcodeSpeed,omitempty"`
	// FailureReason is a closed code naming why a background build failed
	// (state "failed"): empty_target, unknown_target_kind, unknown_quality,
	// device_attestation_failed, source_unavailable, transcode_failed,
	// byte_bound_exceeded, parts_unsupported or build_failed. Empty in every
	// other state.
	FailureReason string `json:"failureReason,omitempty"`
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
		Progress:   1,
	}
	if pack.ExpiresAt != nil {
		info.ExpiresAt = formatOptionalTime(*pack.ExpiresAt)
	}
	return info
}

// offlinePackBuildInfo is the wire view of a background build that has not
// sealed yet: state building or failed, its progress and, when failed, its
// reason code. Item and byte counts stay zero until the pack seals.
func offlinePackBuildInfo(build store.OfflinePackBuild) OfflinePackInfo {
	info := OfflinePackInfo{
		ID:         string(build.PackID),
		TargetKind: build.TargetKind,
		TargetRef:  build.TargetRef,
		State:      string(build.State),
		CreatedAt:  formatOptionalTime(build.CreatedAt),
		UpdatedAt:  formatOptionalTime(build.UpdatedAt),
		Progress:   build.Progress,
	}
	if build.State == store.OfflinePackBuildFailed {
		info.FailureReason = build.FailureReason
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

// offlinePacks lists the caller's Offline Packs: background builds that have
// not sealed yet (building or failed) first, then sealed packs. Builds are
// read before packs, so a build that seals between the two reads is listed
// once, as its pack.
func (h *Handler) offlinePacks_(ctx context.Context, scope auth.Scope) ([]OfflinePackInfo, error) {
	if h.offlinePacks == nil {
		return nil, clientError("offline packs unavailable")
	}
	builds, err := h.offlinePacks.Builds(ctx, scope.ProfileID)
	if err != nil {
		return nil, err
	}
	packs, err := h.offlinePacks.Packs(ctx, scope.ProfileID)
	if err != nil {
		return nil, err
	}
	sealed := make(map[store.OfflinePackID]bool, len(packs))
	for _, p := range packs {
		sealed[p.ID] = true
	}
	out := make([]OfflinePackInfo, 0, len(builds)+len(packs))
	for _, b := range builds {
		if !sealed[b.PackID] {
			info := offlinePackBuildInfo(b)
			if b.State == store.OfflinePackBuildBuilding {
				info.TranscodeSpeed = h.offlinePacks.BuildTranscodeSpeed(b.PackID)
			}
			out = append(out, info)
		}
	}
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
		ManifestParts:    boolVar(req, "manifestParts"),
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
	if quality := trimmedVariable(req, "quality"); quality != "" {
		build.Quality, err = offlinepack.NormalizeQuality(quality)
		if err != nil {
			return OfflinePackInfo{}, clientError("quality must be original, high, medium or low")
		}
		if err := h.admitOfflinePackBuild(ctx, scope, build); err != nil {
			return OfflinePackInfo{}, err
		}
		return h.queueOfflinePackBuild(ctx, scope, build)
	}
	if err := h.admitOfflinePackBuild(ctx, scope, build); err != nil {
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
		DedupeKey: offlinePackBuildDedupeKey(scope.ProfileID, kind, build.TargetRef) + manifestPartsKey(build.ManifestParts),
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

// offlinePackBuilding is the state a background build reports before its
// pack row exists.
const offlinePackBuilding = "building"

// admitOfflinePackBuild refuses, before any job is queued, a request the
// build would fail on its per-item bound. Every other admission failure is
// left to the job, which reports it as before.
func (h *Handler) admitOfflinePackBuild(ctx context.Context, scope auth.Scope, build offlinepack.BuildPackRequest) error {
	err := h.offlinePacks.AdmitBuild(ctx, scope, build)
	if errors.Is(err, offlinepack.ErrOriginalItemTooLarge) {
		return clientError("original quality is limited to 4 GiB per item; choose high, medium or low quality")
	}
	if errors.Is(err, offlinepack.ErrManifestPartsUnsupported) {
		return clientError("this item has several files; update the app to download it")
	}
	return nil
}

// queueOfflinePackBuild queues a background build under a pre-assigned pack ID
// and returns at once. A repeat request while that build is active returns the
// active job's ID, so clients can poll offlinePacks for it.
func (h *Handler) queueOfflinePackBuild(ctx context.Context, scope auth.Scope, build offlinepack.BuildPackRequest) (OfflinePackInfo, error) {
	var err error
	build.PackID, err = offlinepack.NewPackID()
	if err != nil {
		return OfflinePackInfo{}, err
	}
	payload, err := offlinepack.BuildJobPayloadFor(scope, build)
	if err != nil {
		return OfflinePackInfo{}, err
	}
	job, err := h.store.EnqueueJob(ctx, store.Job{
		Kind:      offlinepack.BuildJobKind,
		Class:     store.JobClassBackground,
		DedupeKey: offlinePackQueueDedupeKey(scope, build.TargetKind, build.TargetRef, build.Quality, build.SelectedVersions, build.ManifestParts),
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return OfflinePackInfo{}, err
	}
	// The dedupe key may have matched an earlier active build: its payload
	// holds the ID that build will use.
	var queued offlinepack.BuildJobPayload
	if err := json.Unmarshal(job.Payload, &queued); err != nil || !offlinepack.ValidPackID(queued.PackID) {
		return OfflinePackInfo{}, errors.New("offline pack build job carries no pack ID")
	}
	// Record the pending row now, so offlinePacks lists the build before its
	// job starts (CORE-1743).
	tracked, err := h.offlinePacks.TrackBuild(ctx, job.Payload)
	if err != nil {
		return OfflinePackInfo{}, err
	}
	if tracked.PackID != "" {
		return offlinePackBuildInfo(tracked), nil
	}
	// The matched build sealed while its job was still finishing.
	if pack, err := h.offlinePacks.Pack(ctx, queued.PackID, scope.ProfileID); err == nil {
		return h.withPackTokens(ctx, scope, pack, offlinePackInfo(pack)), nil
	}
	return OfflinePackInfo{
		ID: string(queued.PackID), TargetKind: build.TargetKind, TargetRef: build.TargetRef, State: offlinePackBuilding,
	}, nil
}

// retryOfflinePack restarts the caller's failed background build under the
// same pack ID (CORE-1743). It is idempotent by pack ID: a build that is
// already building is returned as-is without queueing a second job, and a
// pack that has since sealed is returned as the pack.
func (h *Handler) retryOfflinePack(ctx context.Context, scope auth.Scope, req request) (OfflinePackInfo, error) {
	if h.offlinePacks == nil {
		return OfflinePackInfo{}, clientError("offline packs unavailable")
	}
	if h.store == nil {
		return OfflinePackInfo{}, clientError("offline pack job runner unavailable")
	}
	id := store.OfflinePackID(trimmedVariable(req, "id"))
	if id == "" {
		return OfflinePackInfo{}, clientError("id is required")
	}
	build, payload, err := h.offlinePacks.RetryBuild(ctx, scope, id)
	switch {
	case errors.Is(err, offlinepack.ErrBuildSealed):
		pack, err := h.offlinePacks.Pack(ctx, id, scope.ProfileID)
		if err != nil {
			return OfflinePackInfo{}, err
		}
		return h.withPackTokens(ctx, scope, pack, offlinePackInfo(pack)), nil
	case errors.Is(err, store.ErrOfflinePackBuildNotFound):
		return OfflinePackInfo{}, clientError("offline pack build not found")
	case errors.Is(err, offlinepack.ErrBuildDevice):
		return OfflinePackInfo{}, clientError("retry the offline pack build from the device that requested it")
	case err != nil:
		return OfflinePackInfo{}, err
	}
	if payload != nil {
		// RetryBuild committed the row to building: from here every path
		// either queues its job or puts the row back to failed, and none of
		// it honours the request's cancellation, which would strand the row
		// building with no job.
		detached := context.WithoutCancel(ctx)
		var job offlinepack.BuildJobPayload
		if err := json.Unmarshal(payload, &job); err != nil {
			if failErr := h.offlinePacks.FailBuild(detached, id, offlinepack.FailureBuild); failErr != nil {
				return OfflinePackInfo{}, errors.Join(err, failErr)
			}
			return OfflinePackInfo{}, err
		}
		queued, err := h.store.EnqueueJob(detached, store.Job{
			Kind:  offlinepack.BuildJobKind,
			Class: store.JobClassBackground,
			// The original build's key: an operator jobRetry keeps that key, so
			// a retry from either side joins the other's active job instead of
			// running a second builder for the same pack ID.
			DedupeKey: offlinePackQueueDedupeKey(scope, job.TargetKind, job.TargetRef, job.Quality, job.SelectedVersions, job.ManifestParts),
			Payload:   payload,
			CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			if failErr := h.offlinePacks.FailBuild(detached, id, offlinepack.FailureBuild); failErr != nil {
				return OfflinePackInfo{}, errors.Join(err, failErr)
			}
			return OfflinePackInfo{}, err
		}
		// The key also matches a newer pack built from the same selection.
		// Joining its job would leave this pack building with no worker, so
		// put it back to failed, where it stays retryable.
		var matched offlinepack.BuildJobPayload
		if err := json.Unmarshal(queued.Payload, &matched); err != nil || matched.PackID != id {
			if failErr := h.offlinePacks.FailBuild(detached, id, offlinepack.FailureBuild); failErr != nil {
				return OfflinePackInfo{}, failErr
			}
			return OfflinePackInfo{}, clientError("another offline pack build for the same selection is running; retry when it finishes")
		}
		// A running job for this pack ID is the one whose failure this retry
		// is answering: the build writes failed before its job turns
		// terminal, so joining it would leave the row building once that job
		// ends. A queued or yielded job will still run the build, so joining
		// one is safe; a running one is refused and the row stays failed.
		if queued.Status == store.JobStatusRunning {
			if failErr := h.offlinePacks.FailBuild(detached, id, offlinepack.FailureBuild); failErr != nil {
				return OfflinePackInfo{}, failErr
			}
			return OfflinePackInfo{}, clientError("the previous offline pack build is still finishing; retry shortly")
		}
	}
	return offlinePackBuildInfo(build), nil
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

// selectedVersionsKey is a canonical form of a pack's version choices, so
// builds of different versions of the same target never share a job.
func selectedVersionsKey(selected map[store.MediaID]string) string {
	parts := make([]string, 0, len(selected))
	for id, version := range selected {
		parts = append(parts, strconv.Quote(string(id))+"="+strconv.Quote(version))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// offlinePackQueueDedupeKey keys a background build. The device is part of
// the key: a pack is sealed to one device key, so another device of the same
// Profile must not reuse this build.
func offlinePackQueueDedupeKey(scope auth.Scope, kind, ref, quality string, selected map[store.MediaID]string, manifestParts bool) string {
	return offlinePackBuildDedupeKey(scope.ProfileID, kind, ref) + "\x1f" + string(scope.DeviceID) + "\x1f" + quality + "\x1f" + selectedVersionsKey(selected) + manifestPartsKey(manifestParts)
}

// manifestPartsKey separates the dedupe keys of builds whose manifests differ
// in item shape (ADR-0157 Amendment A), so a legacy client never joins a
// parts-aware build. It is empty for a legacy build, which keeps that key.
func manifestPartsKey(manifestParts bool) string {
	if manifestParts {
		return "\x1fparts"
	}
	return ""
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
	if errors.Is(err, store.ErrOfflinePackNotFound) {
		return h.dismissOfflinePackBuild(ctx, scope, id, err)
	}
	if err != nil {
		return OfflinePackInfo{}, err
	}
	return offlinePackInfo(pack), nil
}

// dismissOfflinePackBuild revokes a pack that never sealed: a failed build is
// dismissed from the list, and an in-progress build has its live job(s)
// cancelled and its build row removed. Any other ID keeps the pack lookup's
// not-found error.
func (h *Handler) dismissOfflinePackBuild(ctx context.Context, scope auth.Scope, id store.OfflinePackID, notFound error) (OfflinePackInfo, error) {
	builds, err := h.offlinePacks.Builds(ctx, scope.ProfileID)
	if err != nil {
		return OfflinePackInfo{}, err
	}
	for _, build := range builds {
		if build.PackID != id {
			continue
		}
		if build.State == store.OfflinePackBuildFailed {
			if err := h.offlinePacks.DismissBuild(ctx, id, scope.ProfileID); err != nil {
				return OfflinePackInfo{}, err
			}
		} else {
			if h.store != nil {
				live, err := h.store.ListJobs(ctx, store.JobStatusQueued, store.JobStatusRunning, store.JobStatusYielded)
				if err != nil {
					return OfflinePackInfo{}, err
				}
				for _, job := range live {
					if job.Kind != offlinepack.BuildJobKind {
						continue
					}
					var payload offlinepack.BuildJobPayload
					if json.Unmarshal(job.Payload, &payload) != nil || payload.PackID != id {
						continue
					}
					prior, cancelErr := h.store.CancelJobWithPrior(ctx, job.ID, time.Now().UTC())
					if cancelErr != nil && !errors.Is(cancelErr, store.ErrJobTerminal) && !errors.Is(cancelErr, store.ErrJobNotFound) {
						return OfflinePackInfo{}, cancelErr
					}
					if cancelErr == nil && prior == store.JobStatusRunning {
						if canceler, ok := h.jobRunner.(RunningJobCanceler); ok {
							canceler.CancelRunning(job.ID)
						}
					}
				}
			}
			if err := h.offlinePacks.CancelBuild(ctx, id, scope.ProfileID); err != nil {
				return OfflinePackInfo{}, err
			}
		}
		info := offlinePackBuildInfo(build)
		info.State = string(store.OfflinePackRevoked)
		info.FailureReason = ""
		return info, nil
	}
	return OfflinePackInfo{}, notFound
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
