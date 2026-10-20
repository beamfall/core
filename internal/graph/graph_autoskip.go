package graph

import (
	"context"

	"github.com/beamfall/core/internal/store"
)

// Per-Profile auto-skip preference (ADR-0048 Decision 2, master issue ENR-6).
//
// Auto-skip is three independent, Profile-global booleans — one per
// skip-segment kind (intro / recap / credits) — that tell a viewer client to
// seek past a segment of that kind without a button tap. They are *advisory*:
// the server only stores and exposes them. Per ADR-0048's "client seeks, the
// server never moves the playhead" rule (ADR-0029 §6 / ADR-0030 client-owned
// transport), Core never acts on these — there is deliberately no server-side
// seek logic keyed off them anywhere.
//
// Storage reuses the existing generic per-Profile preference seam
// (profile_client_prefs, ADR-0014): each boolean is one client-namespaced key
// whose value is "on" when enabled. No new schema, no migration — exactly the
// ENR-2 recommend opt-in pattern (graph_enrichment.go's recommendOptInKey).
// They are read and set over the wire through the generic clientPrefs /
// setClientPref graph operations (graph.go), so they roam in the per-Profile
// tvOS/mobile Snapshot alongside every other client-owned pref;
// no dedicated graph field or Snapshot struct is needed or added.
//
// The keys share the "autoskip." namespace so a client can fold the whole
// preference out of the clientPrefs list with a single namespace match.
//
// ADR-0193 Decision 2/5 adds the fourth key, autoskip.ad, for the `ad`
// skip-segment kind. It is keyed on the KIND ALONE, exactly like the other
// three: detection flavor (inserted / host-read) never reaches a client and
// never gates auto-skip, so "one `ad` kind, one `autoskip.ad` boolean" holds and
// ADR-0048's one-boolean-per-kind model is unmodified.
//
// RESERVED, NOT IMPLEMENTED: `sponsor` is a non-binding
// placeholder, not a live segment kind, so there is deliberately no
// autoskip.sponsor key and no branch reading one. If a future amendment ships
// the host-read/inserted split, its ANTICIPATED migration rule is that
// autoskip.sponsor initializes from autoskip.ad by READ-TIME FALLBACK — an
// absent `sponsor` key resolves to the `ad` value — never a one-time backfill
// write, so the split is behavior-preserving and needs no pref migration. That
// contract is recorded in exactly two places — here and the web client's mirror
// of this key namespace, internal/web/app/src/areas/viewer/lib/preferences.ts —
// and it is not code on either side of the seam.
const (
	autoSkipIntroKey   = "autoskip.intro"
	autoSkipRecapKey   = "autoskip.recap"
	autoSkipCreditsKey = "autoskip.credits"
	autoSkipAdKey      = "autoskip.ad"
)

// AutoSkipPrefs is the resolved four-boolean auto-skip preference for one
// Profile. Every field defaults false (auto-skip off) — the opt-in posture
// ADR-0048 Decision 2 requires because auto-skip silently moves the playhead.
type AutoSkipPrefs struct {
	Intro   bool
	Recap   bool
	Credits bool
	Ad      bool
}

// autoSkipPrefs reads the Profile's four auto-skip booleans from the generic
// per-Profile preference store, defaulting every kind to off. Default off means
// an absent key, an empty Profile, or any value other than "on" resolves false.
// A read error fails closed (all off) so a transient store error never silently
// flips a Profile into surprising auto-seek behavior. This is a read-only
// convenience over ClientPrefs; the server never consumes the result to move a
// playhead — clients do the seeking.
func (h *Handler) autoSkipPrefs(ctx context.Context, profileID store.ProfileID) AutoSkipPrefs {
	var prefs AutoSkipPrefs
	if profileID == "" {
		return prefs
	}
	stored, err := h.store.ClientPrefs(ctx, profileID)
	if err != nil {
		return prefs
	}
	for _, p := range stored {
		switch p.Key {
		case autoSkipIntroKey:
			prefs.Intro = p.Value == "on"
		case autoSkipRecapKey:
			prefs.Recap = p.Value == "on"
		case autoSkipCreditsKey:
			prefs.Credits = p.Value == "on"
		case autoSkipAdKey:
			prefs.Ad = p.Value == "on"
		}
	}
	return prefs
}
