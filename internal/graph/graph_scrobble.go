package graph

import (
	"context"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Scrobble Integration Plugin link + eligibility resolvers. These are
// the Core seam the plugin's per-Profile consent model rides on: a Profile links
// its own third-party scrobble account (linkScrobble / unlinkScrobble), and the
// operator marks Libraries scrobble-eligible (setScrobbleEligibility). Core
// stays domain-agnostic (invariant #1): the service value is an opaque lowercase
// token Core validates by SHAPE only and never names — "trakt"/"lastfm"/
// "listenbrainz" live exclusively in the plugin's adapter layer. The OAuth token
// / session key is a secret: it is written to the 0600 secure store and the link
// row carries only its non-secret ref (invariant #5); no resolver here ever
// echoes the token back.

// ScrobbleLinkInfo is the NON-SECRET link view returned to the client after a
// link/unlink, and the "Where your data goes" read. It deliberately has no token
// field — the credential lives only in the secure store (invariant #5).
type ScrobbleLinkInfo struct {
	Service      string `json:"service"`
	AccountLabel string `json:"accountLabel,omitempty"`
	LinkedAt     string `json:"linkedAt,omitempty"`
	Linked       bool   `json:"linked"`
}

// ScrobbleEligibilityInfo echoes a Library's resolved scrobble eligibility after
// a toggle (operator surface).
type ScrobbleEligibilityInfo struct {
	LibraryID string `json:"libraryID"`
	Eligible  bool   `json:"eligible"`
}

// --- Status read surfaces (ADR-0098 area 7) ---------------------------------
//
// SCROBBLE-1 shipped only the link/eligibility MUTATIONS; ADR-0098 area 7 adds
// the read/status surfaces the scrobble UI needs. These are Core seams the
// plugin's diagnostic state rides on, kept domain-agnostic (invariant #1): the
// service value is an opaque token (see validScrobbleService), and the
// dead-letter/skip/adapter feeds carry only generic ids + reasons, never a
// SecretRef or token (invariant #5). No watch/listen data reaches the account
// plane (invariant #7) — these reads stay server-local.

// ScrobbleDeadLetterInfo is one exhausted-submission record for the operator
// status panel: which Profile/service failed on which Media, why, and when. It
// holds no secret (invariant #5).
type ScrobbleDeadLetterInfo struct {
	ProfileID string `json:"profileID"`
	Service   string `json:"service"`
	MediaID   string `json:"mediaID"`
	Attempts  int    `json:"attempts"`
	LastError string `json:"lastError"`
	At        string `json:"at"`
}

// ScrobbleSkipInfo is one skip-with-reason record (a watched event NOT
// scrobbled and why) — an observable non-failure.
type ScrobbleSkipInfo struct {
	ProfileID string `json:"profileID"`
	MediaID   string `json:"mediaID"`
	Service   string `json:"service,omitempty"`
	Reason    string `json:"reason"`
	At        string `json:"at"`
}

// ScrobbleAdapterInfo is one configured adapter's non-secret descriptor: the
// service token, the media class it scrobbles, and the External-ID schemes it
// consumes. "Configured" is the health signal — the closed launch-adapter set is
// known at boot; there is no per-request liveness ping (that would be a new
// outbound call and privacy-noisy).
type ScrobbleAdapterInfo struct {
	Service         string   `json:"service"`
	MediaClass      string   `json:"mediaClass"`
	IdentitySchemes []string `json:"identitySchemes"`
}

// ScrobbleStatusInfo is the admin-gated diagnostic read: the dead-letter +
// skip feeds and the configured adapters (ADR-0098 area 7).
type ScrobbleStatusInfo struct {
	DeadLetters []ScrobbleDeadLetterInfo `json:"deadLetters"`
	Skips       []ScrobbleSkipInfo       `json:"skips"`
	Adapters    []ScrobbleAdapterInfo    `json:"adapters"`
}

// ScrobbleStatusSource is the read-only status seam the scrobble dispatcher
// satisfies (interface-at-consumer, ADR-0016). The composition root adapts the
// dispatcher's FileDeadLetter / MemorySkipSink / adapter registry into this
// generic view; the graph never imports the plugin. Nil (plugin off) → the
// resolvers return empty, never an error.
type ScrobbleStatusSource interface {
	DeadLetters() []ScrobbleDeadLetterInfo
	Skips() []ScrobbleSkipInfo
	Adapters() []ScrobbleAdapterInfo
}

// scrobbleLinksRead returns the SESSION Profile's own non-secret scrobble links
// (ADR-0098 area 7). Profile-scoped like the link mutations: a Profile reads
// only its OWN links, and an anonymous session (no Profile) is refused — the
// same guard as linkScrobble/unlinkScrobble. No token is ever returned
// (invariant #5).
func (h *Handler) scrobbleLinksRead(ctx context.Context, scope auth.Scope) ([]ScrobbleLinkInfo, error) {
	if scope.ProfileID == "" {
		return nil, clientError("a profile session is required")
	}
	rows, err := h.store.ScrobbleLinksFor(ctx, scope.ProfileID)
	if err != nil {
		return nil, err
	}
	infos := make([]ScrobbleLinkInfo, 0, len(rows))
	for _, row := range rows {
		linkedAt := ""
		if !row.LinkedAt.IsZero() {
			linkedAt = row.LinkedAt.UTC().Format(time.RFC3339)
		}
		infos = append(infos, ScrobbleLinkInfo{
			Service:      row.Service,
			AccountLabel: row.AccountLabel,
			LinkedAt:     linkedAt,
			Linked:       true,
		})
	}
	return infos, nil
}

// scrobbleEligibilityRead returns per-Library scrobble eligibility for the
// Libraries VISIBLE to the scope (ADR-0098 area 7). Visibility-filtered through
// the scope's LibraryFilter, so a hidden/Sealed/restricted-unrevealed Library a
// session cannot see never appears — the eligibility read can never leak a
// Library's existence.
func (h *Handler) scrobbleEligibilityRead(ctx context.Context, scope auth.Scope) ([]ScrobbleEligibilityInfo, error) {
	libraries, err := h.store.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	infos := make([]ScrobbleEligibilityInfo, 0, len(libraries))
	for _, library := range libraries {
		eligible, err := h.store.ScrobbleLibraryEligible(ctx, library.ID)
		if err != nil {
			return nil, err
		}
		infos = append(infos, ScrobbleEligibilityInfo{
			LibraryID: string(library.ID),
			Eligible:  eligible,
		})
	}
	return infos, nil
}

// scrobbleStatusRead returns the admin-gated diagnostic status (dead-letter +
// skip feeds + configured adapters, ADR-0098 area 7). The caller gates on an
// open Admin Session (the dead-letter/skip feeds expose cross-Profile ids, so
// they are an operator surface, the SAME tier as setScrobbleEligibility). A nil
// status source (plugin off) returns the empty (non-nil) snapshot, never errors.
func (h *Handler) scrobbleStatusRead() ScrobbleStatusInfo {
	status := ScrobbleStatusInfo{
		DeadLetters: []ScrobbleDeadLetterInfo{},
		Skips:       []ScrobbleSkipInfo{},
		Adapters:    []ScrobbleAdapterInfo{},
	}
	if h.scrobbleStatus == nil {
		return status
	}
	if dl := h.scrobbleStatus.DeadLetters(); dl != nil {
		status.DeadLetters = dl
	}
	if sk := h.scrobbleStatus.Skips(); sk != nil {
		status.Skips = sk
	}
	if ad := h.scrobbleStatus.Adapters(); ad != nil {
		status.Adapters = ad
	}
	return status
}

// validScrobbleService accepts the lowercase service-token SHAPE only (letters,
// digits, hyphen). Core never enumerates the service names (invariant #1: that
// vocabulary is the plugin's); it only refuses a value that could escape a
// secure-store filename or carry path separators.
func validScrobbleService(service string) bool {
	if service == "" || len(service) > 64 {
		return false
	}
	for _, r := range service {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

// scrobbleSecretRef derives the deterministic 0600 secure-store key for a
// (Profile, service) link's credential. Both components are already path-safe
// tokens, so the ref is a stable, collision-free secretFiles entry carrying no
// credential. The ref is what the link row stores; the secret bytes live only in
// the file this ref names.
func scrobbleSecretRef(profileID store.ProfileID, service string) string {
	return "scrobble-" + string(profileID) + "-" + service
}

// linkScrobble links the SESSION's own Profile to one scrobble service (ADR-0049
// §2.2: per-Profile consent — a Profile links its own account, never the
// operator on its behalf). The credential (OAuth token / session key) is written
// to the 0600 secure store FIRST, then the non-secret link row records the ref —
// so a stored ref always resolves and the token never touches the catalog DB or
// a log line (invariant #5). The credential is required; a blank one is refused
// rather than recording a useless link. Relinking replaces the prior credential.
func (h *Handler) linkScrobble(ctx context.Context, scope auth.Scope, req request) (ScrobbleLinkInfo, error) {
	if scope.ProfileID == "" {
		return ScrobbleLinkInfo{}, clientError("a profile session is required")
	}
	service := strings.ToLower(strings.TrimSpace(stringVar(req, "service")))
	credential := stringVar(req, "credential")
	accountLabel := strings.TrimSpace(stringVar(req, "accountLabel"))
	if !validScrobbleService(service) {
		return ScrobbleLinkInfo{}, clientError("invalid scrobble service")
	}
	if strings.TrimSpace(credential) == "" {
		return ScrobbleLinkInfo{}, clientError("a scrobble credential is required")
	}
	// The credential is a secret (invariant #5): refuse the link up front when no
	// secure store is wired rather than plaintext-storing or silently dropping it.
	if h.subscriptionSecrets == nil {
		return ScrobbleLinkInfo{}, clientError("scrobble credentials are unavailable on this server")
	}
	ref := scrobbleSecretRef(scope.ProfileID, service)
	// Secure store first, then the link row: a recorded ref must always resolve.
	if err := h.subscriptionSecrets.SetSecret(ref, credential); err != nil {
		return ScrobbleLinkInfo{}, err
	}
	now := time.Now().UTC()
	if err := h.store.UpsertScrobbleLink(ctx, store.ScrobbleLink{
		ProfileID:    scope.ProfileID,
		Service:      service,
		SecretRef:    ref,
		AccountLabel: accountLabel,
		LinkedAt:     now,
	}); err != nil {
		return ScrobbleLinkInfo{}, err
	}
	return ScrobbleLinkInfo{
		Service:      service,
		AccountLabel: accountLabel,
		LinkedAt:     now.Format(time.RFC3339),
		Linked:       true,
	}, nil
}

// unlinkScrobble severs the session Profile's link to one service (ADR-0049 §2.9
// one-tap unlink). It clears the 0600 credential file AND deletes the link row,
// so neither the secret nor the record outlives the unlink. Both steps are
// idempotent (a missing secret/link is success), so a retried unlink is safe.
func (h *Handler) unlinkScrobble(ctx context.Context, scope auth.Scope, req request) (ScrobbleLinkInfo, error) {
	if scope.ProfileID == "" {
		return ScrobbleLinkInfo{}, clientError("a profile session is required")
	}
	service := strings.ToLower(strings.TrimSpace(stringVar(req, "service")))
	if !validScrobbleService(service) {
		return ScrobbleLinkInfo{}, clientError("invalid scrobble service")
	}
	// Clear the credential first (a stored ref must never outlive its secret),
	// then drop the row. SetSecret("") removes the 0600 file.
	if h.subscriptionSecrets != nil {
		if err := h.subscriptionSecrets.SetSecret(scrobbleSecretRef(scope.ProfileID, service), ""); err != nil {
			return ScrobbleLinkInfo{}, err
		}
	}
	if err := h.store.DeleteScrobbleLink(ctx, scope.ProfileID, service); err != nil {
		return ScrobbleLinkInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityScrobbleUnlink, "scrobble_link", service, ""); err != nil {
		return ScrobbleLinkInfo{}, err
	}
	return ScrobbleLinkInfo{Service: service, Linked: false}, nil
}

// setScrobbleEligibility marks a Library scrobble-eligible or not (ADR-0049 §2.3
// per-Library opt-in). It is an OPERATOR surface (admin-gated in the dispatch):
// eligibility is the operator's per-Library decision, distinct from a Profile's
// own account link. Adult/Restricted Libraries are never eligible by default —
// the default is "no row = not eligible" — so enabling one is a deliberate,
// explicit operator act, never a silent default.
func (h *Handler) setScrobbleEligibility(ctx context.Context, scope auth.Scope, req request) (ScrobbleEligibilityInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return ScrobbleEligibilityInfo{}, clientError("libraryID is required")
	}
	eligible := boolVar(req, "eligible")
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return ScrobbleEligibilityInfo{}, err
	}
	if !visible {
		return ScrobbleEligibilityInfo{}, clientError("unknown library")
	}
	if err := h.store.SetScrobbleLibraryEligibility(ctx, store.LibraryID(libraryID), eligible); err != nil {
		return ScrobbleEligibilityInfo{}, err
	}
	return ScrobbleEligibilityInfo{LibraryID: libraryID, Eligible: eligible}, nil
}
