package graph

import (
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const transferOwnerMutation = `{"query":"mutation T($profileID: String!){ transferProfileOwner(profileID: $profileID){ profileID isOwner } }","variables":{"profileID":"%s"}}`

// TestTransferProfileOwnerMovesFlagUnderAdminSession pins ADR-0007 §4: with an
// open Admin Session, transferProfileOwner moves the single owner flag to the
// named adult Profile. feature:admin-session
func TestTransferProfileOwnerMovesFlagUnderAdminSession(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	// The seeded admin caller (profile-default) starts as owner.
	if err := st.SetProfileOwner(t.Context(), "profile-default"); err != nil {
		t.Fatalf("designate initial owner: %v", err)
	}
	heir, err := st.CreateProfile(t.Context(), "Heir", store.ProfileAdult,
		store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("create heir profile: %v", err)
	}

	body := strings.Replace(transferOwnerMutation, "%s", string(heir.ID), 1)
	data, errs := post(t, handler, body, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("transferProfileOwner errors = %#v", errs)
	}
	var result TransferProfileOwnerResult
	mustUnmarshal(t, data["transferProfileOwner"], &result)
	if result.ProfileID != string(heir.ID) || !result.IsOwner {
		t.Fatalf("result = %#v, want {profileID:%s isOwner:true}", result, heir.ID)
	}

	// The flag moved: heir owns, the prior owner does not (single-owner invariant).
	if owned, _ := st.ProfileIsOwner(t.Context(), heir.ID); !owned {
		t.Fatal("heir is not owner after transfer")
	}
	if owned, _ := st.ProfileIsOwner(t.Context(), "profile-default"); owned {
		t.Fatal("prior owner still owns after transfer")
	}
}

// TestTransferProfileOwnerRequiresAdminSession is the load-bearing gate: a
// non-admin manager (and a plain viewer) is refused. feature:admin-session
func TestTransferProfileOwnerRequiresAdminSession(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	// NOTE: no openManagerAdminSession — the manager session is NOT admin.

	body := strings.Replace(transferOwnerMutation, "%s", "profile-default", 1)
	if _, errs := post(t, handler, body, authorizeManager); len(errs) == 0 ||
		!strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("non-admin transferProfileOwner errors = %#v, want admin-session gate", errs)
	}
	if _, errs := post(t, handler, body, authorizeViewer); len(errs) == 0 ||
		!strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("viewer transferProfileOwner errors = %#v, want admin-session gate", errs)
	}
}

// TestTransferProfileOwnerRejectsManagedTarget pins the adult-only rule: even
// with an open Admin Session, a managed Profile can never be made owner.
// feature:admin-session
func TestTransferProfileOwnerRejectsManagedTarget(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	body := strings.Replace(transferOwnerMutation, "%s", "profile-managed", 1)
	_, errs := post(t, handler, body, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "adult profile") {
		t.Fatalf("managed-target transferProfileOwner errors = %#v, want adult-only refusal", errs)
	}
}
