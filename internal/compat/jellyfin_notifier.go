package compat

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/beamfall/core/internal/store"
)

const (
	mediaUpdateBodyLimit = 64 << 10
	mediaUpdateMaxPaths  = 256
)

// systemConfiguration answers the *arr connection test with an empty
// configuration; the tools only log it.
func (h *Handler) systemConfiguration(w http.ResponseWriter, r *http.Request, _ net.IP) {
	if _, ok := h.integrationScope(w, r, integrationToken(r.Header), store.IntegrationScopeRead); !ok {
		return
	}
	writeJSON(w, struct{}{})
}

// adminNotification authenticates the caller and then refuses: Beamfall has
// no admin notification inbox, and a 404 lets the tools report that instead of
// a silent success (ADR-0171 §2.8).
func (h *Handler) adminNotification(w http.ResponseWriter, r *http.Request, _ net.IP) {
	if _, ok := h.integrationScope(w, r, integrationToken(r.Header), store.IntegrationScopeRead); !ok {
		return
	}
	http.NotFound(w, r)
}

type mediaUpdateRequest struct {
	Updates []struct {
		Path       string `json:"Path"`
		UpdateType string `json:"UpdateType"`
	} `json:"Updates"`
}

// mediaUpdated queues scans for the eligible Library roots that contain the
// updated paths. A well-formed request always gets 204, whether or not a root
// matched, and root matching runs after the response is written, so neither
// the response nor its timing can be used to probe server paths.
func (h *Handler) mediaUpdated(w http.ResponseWriter, r *http.Request, _ net.IP) {
	token := integrationToken(r.Header)
	if _, ok := h.integrationScope(w, r, token, store.IntegrationScopeRefresh); !ok {
		return
	}
	defer r.Body.Close()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, mediaUpdateBodyLimit))
	var request mediaUpdateRequest
	if err != nil || json.Unmarshal(body, &request) != nil || len(request.Updates) > mediaUpdateMaxPaths {
		http.Error(w, "invalid media update", http.StatusBadRequest)
		return
	}
	// Read the scope again now the body has arrived: a grant or session
	// withdrawn during the upload must not queue a scan.
	scope, ok := h.integrationScope(w, r, token, store.IntegrationScopeRefresh)
	if !ok {
		return
	}
	paths := make([]string, 0, len(request.Updates))
	for _, update := range request.Updates {
		if update.Path != "" {
			paths = append(paths, update.Path)
		}
	}
	queue := len(paths) > 0
	if queue && !slices.ContainsFunc(paths, absolutePath) {
		// Lidarr posts paths relative to its own root folder, which name no
		// server location: scan every eligible root instead.
		paths = nil
	}
	// The shared limiter admits at most one of these per interval.
	refresh := queue && h.enqueueRefresh != nil && h.allowRefresh()
	ctx := context.WithoutCancel(r.Context())
	w.WriteHeader(http.StatusNoContent)
	if refresh {
		h.background.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, refreshInterval)
			defer cancel()
			h.enqueueRefresh(ctx, scope, paths)
		})
	}
}

// absolutePath reports whether path is a POSIX, drive-letter or UNC absolute
// path, whichever platform the tool runs on.
func absolutePath(path string) bool {
	switch {
	case strings.HasPrefix(path, "/"), strings.HasPrefix(path, `\\`):
		return true
	case len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/'):
		c := path[0] | 0x20
		return c >= 'a' && c <= 'z'
	}
	return false
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
