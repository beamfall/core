package compat

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"time"

	"github.com/beamfall/core/internal/store"
)

// ticksPerSecond converts seconds to the compatibility API's 100 ns ticks.
const ticksPerSecond = 10_000_000

// compatSession is one now-playing entry for dashboards (ADR-0171 §2.3). It
// carries no device fields and no native IDs, and nothing that controls a
// session: SupportsRemoteControl is always false.
type compatSession struct {
	ID                    string            `json:"Id"`
	UserID                string            `json:"UserId"`
	UserName              string            `json:"UserName"`
	ServerID              string            `json:"ServerId"`
	SupportsRemoteControl bool              `json:"SupportsRemoteControl"`
	NowPlayingItem        compatPlayingItem `json:"NowPlayingItem"`
	PlayState             compatPlayState   `json:"PlayState"`
}

type compatPlayingItem struct {
	compatItem
	SeriesName        string `json:"SeriesName,omitempty"`
	ParentIndexNumber *int   `json:"ParentIndexNumber,omitempty"`
	RunTimeTicks      int64  `json:"RunTimeTicks,omitempty"`
}

type compatPlayState struct {
	PositionTicks int64 `json:"PositionTicks"`
}

// sessions serves Sessions: the playbacks of opted-in Profiles active within
// the live-session window whose Media is a visible catalog item. It reads the
// same Stream Token ledger as the Console's live sessions, under the catalog
// filter, so a Reveal-restricted, sealed or unmapped item is never listed.
func (h *Handler) sessions(w http.ResponseWriter, r *http.Request, _ net.IP) {
	libraries, filter, ok := h.catalogScope(w, r)
	if !ok {
		return
	}
	out := []compatSession{}
	if len(libraries) == 0 {
		writeJSON(w, out)
		return
	}
	ctx := r.Context()
	profiles, err := h.optedInProfiles(ctx)
	if err != nil {
		http.Error(w, "compatibility sessions unavailable", http.StatusInternalServerError)
		return
	}
	optedIn := make(map[store.ProfileID]bool, len(profiles))
	for _, profile := range profiles {
		optedIn[profile.ID] = true
	}
	now := time.Now()
	if h.sessionsNow != nil {
		now = h.sessionsNow()
	}
	active, err := h.store.ActiveSessions(ctx, now, filter)
	if err != nil {
		http.Error(w, "compatibility sessions unavailable", http.StatusInternalServerError)
		return
	}
	var playing []store.ActiveSession
	var ids []string
	for _, session := range active {
		if !optedIn[session.ProfileID] || session.LastActiveAt.Before(now.Add(-store.LiveSessionActivityWindow)) {
			continue
		}
		playing = append(playing, session)
		ids = append(ids, string(session.MediaID))
	}
	found, err := h.lookupNodes(ctx, filter, libraries, ids)
	if err != nil {
		http.Error(w, "compatibility sessions unavailable", http.StatusInternalServerError)
		return
	}
	for _, session := range playing {
		hit, ok := found[string(session.MediaID)]
		if !ok || !hit.node.leaf {
			continue
		}
		out = append(out, compatSession{
			ID:     h.compatSessionID(session.SessionID, session.MediaID),
			UserID: h.compatUserID(session.ProfileID), UserName: session.ProfileName,
			ServerID:       h.serverID,
			NowPlayingItem: h.playingItem(hit.node, session.DurationSeconds),
			PlayState:      compatPlayState{PositionTicks: int64(session.PositionSeconds) * ticksPerSecond},
		})
	}
	writeJSON(w, out)
}

func (h *Handler) playingItem(node *catalogNode, durationSeconds int) compatPlayingItem {
	item := compatPlayingItem{compatItem: h.itemJSON(node), RunTimeTicks: int64(durationSeconds) * ticksPerSecond}
	item.MediaSources = nil
	if node.parent != nil {
		item.SeriesName = node.top.name
		if node.parent != node.top {
			item.ParentIndexNumber = node.parent.index
		}
	}
	return item
}

// compatSessionID is an opaque, stable ID for one session's playback of one
// Media item, so a dashboard can key entries without learning the native
// session ID.
func (h *Handler) compatSessionID(session store.SessionID, media store.MediaID) string {
	sum := sha256.Sum256([]byte("beamfall compat session\x00" + h.serverID + "\x00" + string(session) + "\x00" + string(media)))
	return hex.EncodeToString(sum[:16])
}

// itemCounts serves Items/Counts: how many visible items of each declared
// item type the eligible Libraries hold, filtered before counting. Each field
// is the item type followed by "Count", and every type an eligible Library
// declares is present, at zero when it holds none; core names no item type.
// It counts current copies of the index's nodes, so an item trashed, hidden or
// unmapped since the index was built is never counted.
func (h *Handler) itemCounts(w http.ResponseWriter, r *http.Request, _ net.IP) {
	libraries, filter, ok := h.catalogScope(w, r)
	if !ok {
		return
	}
	counts := map[string]int{}
	for _, lib := range libraries {
		for _, types := range []map[string]string{lib.interop.MediaTypes, lib.interop.KindTypes} {
			for _, itemType := range types {
				if itemType != "" {
					counts[itemType+"Count"] += 0
				}
			}
		}
		index, err := h.libraryIndex(r.Context(), filter, lib)
		if err != nil {
			http.Error(w, "compatibility catalog unavailable", http.StatusInternalServerError)
			return
		}
		nodes := make([]*catalogNode, 0, len(index.byID))
		for _, node := range index.byID {
			nodes = append(nodes, node)
		}
		if nodes, err = h.visibleNodes(r.Context(), filter, lib, nodes); err != nil {
			http.Error(w, "compatibility catalog unavailable", http.StatusInternalServerError)
			return
		}
		for _, node := range nodes {
			if node.itemType != "" {
				counts[node.itemType+"Count"]++
			}
		}
	}
	writeJSON(w, counts)
}
