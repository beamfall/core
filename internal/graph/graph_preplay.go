package graph

import (
	"context"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Pre-play queue (CORE-1701): items a client plays before a feature, cinema
// mode style. Core owns the mechanics (the Owner's clip list, the per-Profile
// opt-in, visibility); an active plugin's PrePlayPolicy decides whether a
// feature gets pre-play at all and which suggested items to add. Core never
// decides what kind of Media a feature is.
//
// The per-Profile settings reuse the generic client preference seam
// (profile_client_prefs, ADR-0014) like autoskip.*: they hold no content
// references, so the Owner's clip IDs live in server configuration instead.
const (
	prePlayEnabledKey   = "preplay.enabled"
	prePlayExtrasKey    = "preplay.extras"
	prePlayDefaultCount = 2
	prePlayMaxCount     = 10

	// PrePlayRoleClip marks one of the Owner's clips in a pre-play queue.
	PrePlayRoleClip = "clip"
	// PrePlayRoleSuggested marks an item the plugin policy suggested.
	PrePlayRoleSuggested = "suggested"
)

// PrePlayRequest asks a policy for its plan before FeatureID plays. Filter is
// the requesting Profile's playback filter; every read the policy makes must
// honour it. Count is how many suggested items the Profile wants.
type PrePlayRequest struct {
	ProfileID store.ProfileID
	Filter    store.LibraryFilter
	FeatureID store.MediaID
	Count     int
	Seed      uint64
}

// PrePlayPlan is a policy's answer. Eligible reports whether the feature gets
// pre-play at all, the Owner's clips included; MediaIDs are the suggested
// items in play order, each shown with Label.
type PrePlayPlan struct {
	Eligible bool
	MediaIDs []store.MediaID
	Label    string
}

// PrePlayPolicy is the plugin seam behind prePlayQueue, contributed through
// app.Hooks.PrePlayPolicy by an active plugin.
type PrePlayPolicy interface {
	PlanPrePlay(ctx context.Context, req PrePlayRequest) (PrePlayPlan, error)
}

// PrePlayItem is one entry of a pre-play queue.
type PrePlayItem struct {
	MediaID         string `json:"mediaID"`
	Title           string `json:"title"`
	DurationSeconds int    `json:"durationSeconds"`
	Role            string `json:"role"`
	Label           string `json:"label"`
}

// PrePlayClipsInfo is the Owner's pre-play clip list as Media the Owner sees.
type PrePlayClipsInfo struct {
	Items []PrePlayItem `json:"items"`
}

// prePlaySettings resolves the Profile's opt-in and suggested-item count.
// Absent or unreadable preferences mean pre-play is off.
func (h *Handler) prePlaySettings(ctx context.Context, profileID store.ProfileID) (bool, int) {
	if profileID == "" {
		return false, 0
	}
	stored, err := h.store.ClientPrefs(ctx, profileID)
	if err != nil {
		return false, 0
	}
	enabled, count := false, prePlayDefaultCount
	for _, p := range stored {
		switch p.Key {
		case prePlayEnabledKey:
			enabled = p.Value == "on"
		case prePlayExtrasKey:
			if n, err := strconv.Atoi(strings.TrimSpace(p.Value)); err == nil {
				count = min(max(n, 0), prePlayMaxCount)
			}
		}
	}
	return enabled, count
}

// prePlaySeed varies the suggested order per Profile, feature and day, so a
// replay the same day keeps its order and the next day brings a new one.
func prePlaySeed(profileID store.ProfileID, featureID store.MediaID, now time.Time) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(string(profileID) + "\x00" + string(featureID) + "\x00" + now.UTC().Format(time.DateOnly)))
	return h.Sum64()
}

// prePlayQueue returns the items to play before mediaID: the Owner's clips,
// then the policy's suggested items, each visible to the caller. It is empty
// unless the Profile opted in and the policy calls the feature eligible; a
// policy failure degrades to an empty queue so the feature still plays.
func (h *Handler) prePlayQueue(ctx context.Context, scope auth.Scope, req request) ([]PrePlayItem, error) {
	featureID := store.MediaID(trimmedVariable(req, "mediaID"))
	if featureID == "" {
		return nil, clientError("mediaID is required")
	}
	filter := scope.PlaybackFilter()
	if _, err := h.store.Media(ctx, featureID, filter); err != nil {
		return nil, clientError("unknown media")
	}
	queue := []PrePlayItem{}
	enabled, count := h.prePlaySettings(ctx, scope.ProfileID)
	if !enabled || h.prePlayPolicy == nil {
		return queue, nil
	}
	plan, err := h.prePlayPolicy.PlanPrePlay(ctx, PrePlayRequest{
		ProfileID: scope.ProfileID,
		Filter:    filter,
		FeatureID: featureID,
		Count:     count,
		Seed:      prePlaySeed(scope.ProfileID, featureID, time.Now()),
	})
	if err != nil || !plan.Eligible {
		return queue, nil
	}
	clips, err := h.store.PrePlayClips(ctx)
	if err != nil {
		return nil, err
	}
	suggested := plan.MediaIDs
	if len(suggested) > count {
		suggested = suggested[:count]
	}
	ids := make([]store.MediaID, 0, len(clips)+len(suggested))
	ids = append(append(ids, clips...), suggested...)
	visible, err := h.store.MediaByIDs(ctx, ids, filter)
	if err != nil {
		return nil, err
	}
	seen := map[store.MediaID]bool{featureID: true}
	add := func(id store.MediaID, role, label string) {
		media, ok := visible[id]
		if !ok || seen[id] {
			return
		}
		seen[id] = true
		queue = append(queue, PrePlayItem{
			MediaID: string(id), Title: media.Title, DurationSeconds: media.DurationSeconds,
			Role: role, Label: label,
		})
	}
	for _, id := range clips {
		add(id, PrePlayRoleClip, "")
	}
	for _, id := range suggested {
		add(id, PrePlayRoleSuggested, plan.Label)
	}
	return queue, nil
}

// prePlayClips returns the Owner's clip list, dropping clips the Owner can no
// longer see (trashed or moved out of reach).
func (h *Handler) prePlayClips(ctx context.Context, scope auth.Scope) (PrePlayClipsInfo, error) {
	clips, err := h.store.PrePlayClips(ctx)
	if err != nil {
		return PrePlayClipsInfo{}, err
	}
	return h.prePlayClipsInfo(ctx, scope, clips)
}

// setPrePlayClips replaces the Owner's clip list. Every ID must be visible to
// the Owner, so the mutation cannot probe hidden Media.
func (h *Handler) setPrePlayClips(ctx context.Context, scope auth.Scope, req request) (PrePlayClipsInfo, error) {
	if _, ok := req.Variables["mediaIDs"]; !ok {
		return PrePlayClipsInfo{}, clientError("mediaIDs is required")
	}
	raw := stringSliceVar(req, "mediaIDs")
	ids := make([]store.MediaID, 0, len(raw))
	for _, id := range raw {
		ids = append(ids, store.MediaID(id))
	}
	if len(ids) > store.MaxPrePlayClips {
		return PrePlayClipsInfo{}, clientError("too many pre-play clips")
	}
	visible, err := h.store.MediaByIDs(ctx, ids, scope.LibraryFilter())
	if err != nil {
		return PrePlayClipsInfo{}, err
	}
	for _, id := range ids {
		if _, ok := visible[id]; !ok {
			return PrePlayClipsInfo{}, clientError("unknown pre-play clip " + string(id))
		}
	}
	saved, err := h.store.SetPrePlayClips(ctx, ids, time.Now().UTC())
	if err != nil {
		return PrePlayClipsInfo{}, err
	}
	return h.prePlayClipsInfo(ctx, scope, saved)
}

func (h *Handler) prePlayClipsInfo(ctx context.Context, scope auth.Scope, clips []store.MediaID) (PrePlayClipsInfo, error) {
	info := PrePlayClipsInfo{Items: []PrePlayItem{}}
	if len(clips) == 0 {
		return info, nil
	}
	visible, err := h.store.MediaByIDs(ctx, clips, scope.LibraryFilter())
	if err != nil {
		return PrePlayClipsInfo{}, err
	}
	for _, id := range clips {
		if media, ok := visible[id]; ok {
			info.Items = append(info.Items, PrePlayItem{
				MediaID: string(id), Title: media.Title, DurationSeconds: media.DurationSeconds, Role: PrePlayRoleClip,
			})
		}
	}
	return info, nil
}
