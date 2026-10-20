package graph

import (
	"context"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Watcher status (ADR-0032 — the fsnotify per-Library Watcher), graph half.
// This field is a manager-gated operator-Console read: per visible Library, it
// reports whether the Watcher is actively watching, whether watching is
// available at all, and an honest "watching: unavailable" degradation reason
// (ADR-0032 — the Watcher is an accelerator, Schedules are the correctness
// backstop). The read is Library-filtered through scope.LibraryFilter() like
// the rest of the contract: a hidden Library never appears, so the field is
// never an existence oracle.

// WatcherStatusProvider is the consumer-defined seam the app wires
// the watcher package into. Nil (the default) means the Watcher is honestly
// unavailable: the resolver reports every Library as unavailable rather than
// guessing it is running.
type WatcherStatusProvider interface {
	// Status reports the aggregate Watcher state for one Library. Active is
	// true when at least one root is actively watched; Available is true when
	// watching is possible (fsnotify started) even if no event has fired yet;
	// Error carries the token-free degradation reason when watching failed.
	Status(libraryID store.LibraryID) (active bool, available bool, reason string)
}

// WatcherStatusInfo is the operator-Console view of one Library's Watcher
// state. The fixed "state" vocabulary is watching / available / unavailable —
// named by shape, never by domain (invariant #1).
type WatcherStatusInfo struct {
	LibraryID string `json:"libraryID"`
	Title     string `json:"title"`
	// State is the honest Watcher state: "watching" (a root is actively
	// watched), "available" (watching is possible, not yet confirmed active),
	// or "unavailable" (watching failed or no provider is wired — Schedules
	// remain the correctness backstop).
	State string `json:"state"`
	// Reason is the token-free degradation detail, present only when State is
	// "unavailable" and the provider supplied one.
	Reason string `json:"reason,omitempty"`
}

// SetWatcherStatus wires the Watcher status provider. Nil (the default) leaves
// the watcherStatus field reporting every Library as unavailable — honest for
// a build with no Watcher running (Schedules still cover correctness).
func (h *Handler) SetWatcherStatus(provider WatcherStatusProvider) {
	h.watcherProvider = provider
}

// watcherStatus resolves the manager-gated per-Library Watcher status over the
// Libraries the caller can see. With no provider wired, every visible Library
// reports "unavailable" — the Watcher is an accelerator, never faked running.
func (h *Handler) watcherStatus(ctx context.Context, scope auth.Scope) ([]WatcherStatusInfo, error) {
	if h.store == nil {
		return []WatcherStatusInfo{}, nil
	}
	libraries, err := h.store.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	out := make([]WatcherStatusInfo, 0, len(libraries))
	for _, library := range libraries {
		info := WatcherStatusInfo{
			LibraryID: string(library.ID),
			Title:     library.Title,
			State:     "unavailable",
		}
		if h.watcherProvider != nil {
			active, available, reason := h.watcherProvider.Status(library.ID)
			switch {
			case active:
				info.State = "watching"
			case available:
				info.State = "available"
			default:
				info.State = "unavailable"
				info.Reason = reason
			}
		}
		out = append(out, info)
	}
	return out, nil
}
