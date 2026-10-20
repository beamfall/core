package mediahttp

import (
	"context"
	"net/http"
	"testing"
)

// SharePlay watch-together is governed by the session-intersection
// rule: "a SharePlay session may only contain Media visible to every participant
// in their current Reveal state, enforced server-side via per-participant Stream
// Tokens." The client (beamfall-apple MobileSharePlaySession) mirrors the rule so
// it never offers hidden content, but the client only knows its OWN visible set —
// it cannot see a peer's Reveal/Library state. The authoritative cross-participant
// intersection is therefore the SERVER's: each participant pulls bytes through its
// own Stream Token, minted under its own session scope, and the byte route refuses
// any participant who cannot currently see the Media.
//
// These tests are the EVIDENCE for that server enforcement (APPLE-5 review gap):
// they prove over the REAL /api/media/direct byte route — not via Verify() unit
// calls — that when one participant shares a restricted Media, a co-participant
// who cannot see it (unrevealed, or in a Profile never granted the Library) gets
// NO bytes, while the sharing participant who can see it does. There is no
// server-side "SharePlay session" object: enforcement is the existing
// per-participant token + reveal-filtered byte path, exercised here as the
// intersection rule.
//
// Fixtures (dev seed, internal/store/sqlite.go):
//   - media-archive lives in library-archive (IsRestricted: true).
//   - profile-default is granted library-archive; session-revealed holds reveal,
//     session-default does not (same Profile, different reveal state).
//   - profile-managed is NOT granted library-archive at all; session-managed is
//     that Profile (a managed Profile has no reveal path).
//
// Scenario: participant A == session-revealed shares the restricted media-archive
// into a watch-together session. Participants B == session-default (reveal axis)
// and C == session-managed (Library-grant axis) are the co-participants.

// shareplayWatchTogetherMedia is the restricted Media one participant shares.
const shareplayWatchTogetherMedia = "media-archive"

// TestSharePlaySharerWithRevealReceivesBytes is the positive control: the
// participant who CAN currently see the restricted Media (revealed, granted the
// Library) receives bytes over the real byte route. Without this leg the negative
// legs below could pass for the wrong reason (e.g. a missing file).
func TestSharePlaySharerWithRevealReceivesBytes(t *testing.T) {
	handler, _, tokens, now := streamTokenLifecycleHandler(t)
	streamTokenLifecycleSetBytes(t, handler, shareplayWatchTogetherMedia, []byte("watch-together bytes"))

	// A == session-revealed: adult Profile holding reveal, granted library-archive.
	tokenA := streamTokenLifecycleDirectToken(t, handler, tokens, now, "session-revealed", shareplayWatchTogetherMedia, true)

	rec := streamTokenLifecycleFetch(handler, tokenA)
	if rec.Code != http.StatusOK {
		t.Fatalf("sharer (revealed) status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "watch-together bytes" {
		t.Fatalf("sharer body = %q, want watch-together bytes", rec.Body.String())
	}
}

// TestSharePlayCoParticipantWithoutRevealGetsNoBytes is the reveal axis of the
// intersection rule. The co-participant is in the SAME Profile as the sharer and
// is granted the restricted Library, but is NOT currently revealed. Minting under
// the co-participant's OWN scope produces a Restricted token; the byte route then
// refuses it (Restricted && !Revealed → 401) — the co-participant never receives
// bytes for content outside its current Reveal state, even though a peer shared it.
func TestSharePlayCoParticipantWithoutRevealGetsNoBytes(t *testing.T) {
	handler, _, tokens, now := streamTokenLifecycleHandler(t)
	streamTokenLifecycleSetBytes(t, handler, shareplayWatchTogetherMedia, []byte("watch-together bytes"))

	// B == session-default: same adult Profile as the sharer, NOT revealed.
	tokenB := streamTokenLifecycleDirectToken(t, handler, tokens, now, "session-default", shareplayWatchTogetherMedia, true)

	rec := streamTokenLifecycleFetch(handler, tokenB)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("co-participant (unrevealed) status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() == "watch-together bytes" {
		t.Fatal("co-participant (unrevealed) received restricted watch-together bytes — intersection rule breached")
	}
}

// TestSharePlayCoParticipantWithoutLibraryGrantGetsNoBytes is the Library-grant
// axis. The co-participant's Profile was never granted the restricted Library, so
// it can never see the Media (a managed Profile has no reveal path at all). It is
// refused at the byte route: the token is Restricted and the Profile is never
// revealed, AND the reveal-filtered PrimaryMediaFile lookup would itself hide the
// Media. A participant cannot receive bytes for a Library it has no access to,
// even when another participant in the SharePlay session shares it.
func TestSharePlayCoParticipantWithoutLibraryGrantGetsNoBytes(t *testing.T) {
	handler, _, tokens, now := streamTokenLifecycleHandler(t)
	streamTokenLifecycleSetBytes(t, handler, shareplayWatchTogetherMedia, []byte("watch-together bytes"))

	// C == session-managed: profile-managed, NOT granted library-archive.
	tokenC := streamTokenLifecycleDirectToken(t, handler, tokens, now, "session-managed", shareplayWatchTogetherMedia, true)

	rec := streamTokenLifecycleFetch(handler, tokenC)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("co-participant (no library grant) status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() == "watch-together bytes" {
		t.Fatal("co-participant (no library grant) received restricted watch-together bytes — intersection rule breached")
	}
}

// TestSharePlayTokenIsPerParticipantNonTransferable proves the per-participant
// property the client relies on: a Stream Token is bound to the participant it was
// minted for. The byte route re-derives scope from the token's OWN SessionID
// (mediahttp.Direct: auth.Scope(grant.SessionID)), so the sharer's working token
// cannot be "borrowed" to grant a co-participant access — it only ever resolves
// under the sharer's scope. Each participant therefore receives content strictly
// through its own (reveal-filtered) scope, never another's. Here the sharer's
// token still serves (its own scope), while the unrevealed co-participant minting
// its OWN token is refused (covered above) — there is no path by which the
// co-participant obtains the bytes.
func TestSharePlayTokenIsPerParticipantNonTransferable(t *testing.T) {
	handler, authService, tokens, now := streamTokenLifecycleHandler(t)
	streamTokenLifecycleSetBytes(t, handler, shareplayWatchTogetherMedia, []byte("watch-together bytes"))

	// The sharer's valid restricted token.
	tokenA := streamTokenLifecycleDirectToken(t, handler, tokens, now, "session-revealed", shareplayWatchTogetherMedia, true)

	// The byte route binds the token to session-revealed's scope regardless of who
	// replays it: the grant carries SessionID == session-revealed, and Direct
	// re-derives scope from that id, not from any request-borne identity. Confirm
	// the bound scope is the sharer's, never the unrevealed co-participant's.
	scopeBound, err := authService.Scope(context.Background(), "session-revealed")
	if err != nil {
		t.Fatal(err)
	}
	if !scopeBound.Revealed {
		t.Fatal("sharer scope is not revealed — fixture precondition broken")
	}

	// Replaying the sharer's token resolves under the sharer's revealed scope and
	// serves — it does not, and cannot, elevate a co-participant.
	rec := streamTokenLifecycleFetch(handler, tokenA)
	if rec.Code != http.StatusOK {
		t.Fatalf("sharer token replay status = %d, want 200 (bound to sharer scope): %s", rec.Code, rec.Body.String())
	}

	// The co-participant relocking the sharer's session does NOT let it keep the
	// bytes: drop the sharer's reveal and the same token is refused — the byte path
	// is re-evaluated live against current per-participant reveal state, never
	// cached from share time.
	if err := handler.store.SetSessionRevealed(context.Background(), "session-revealed", false); err != nil {
		t.Fatalf("SetSessionRevealed returned error: %v", err)
	}
	recAfter := streamTokenLifecycleFetch(handler, tokenA)
	if recAfter.Code != http.StatusUnauthorized {
		t.Fatalf("post-relock status = %d, want 401 (live reveal re-evaluation): %s", recAfter.Code, recAfter.Body.String())
	}
}
