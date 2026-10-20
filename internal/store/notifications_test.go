package store

// Tests for the ADR-0039 in-app notification inbox (#32): CreateNotification,
// ListNotifications, MarkNotificationRead, UnreadNotificationCount, the reveal-
// filtering conformance (a notification referencing restricted content must
// never reach an unrevealed session — the hard rule, mirroring the embedding
// store's TestSemanticRevealContract), and recipient-gating (a Profile reads and
// marks only its own notifications).

import (
	"context"
	"testing"
)

// --- CreateNotification validation -------------------------------------------

func TestCreateNotificationValidation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	base := Notification{
		ID:                 "n1",
		RecipientProfileID: "profile-default",
		Kind:               NotificationSystem,
		Title:              "Hello",
	}

	cases := map[string]func(Notification) Notification{
		"empty ID":        func(n Notification) Notification { n.ID = ""; return n },
		"empty recipient": func(n Notification) Notification { n.RecipientProfileID = ""; return n },
		"empty kind":      func(n Notification) Notification { n.Kind = ""; return n },
		"empty title":     func(n Notification) Notification { n.Title = ""; return n },
	}
	for name, mutate := range cases {
		if err := st.CreateNotification(ctx, mutate(base)); err == nil {
			t.Errorf("%s: CreateNotification accepted invalid notification", name)
		}
	}
	// The valid baseline succeeds.
	if err := st.CreateNotification(ctx, base); err != nil {
		t.Fatalf("CreateNotification valid: %v", err)
	}
}

// --- CRUD round-trip: create → list → mark read → unread count ---------------

func TestNotificationCRUDRoundTrip(t *testing.T) {
	st := openConformanceSeed(t)
	ctx := context.Background()
	// Adult profile-default can see both libraries; revealed filter so a
	// library-bearing notification is visible regardless of content reference.
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}

	if err := st.CreateNotification(ctx, Notification{
		ID:                 "n-sys",
		RecipientProfileID: "profile-default",
		Kind:               NotificationSystem,
		Title:              "Import finished",
		Body:               "Your library scan completed.",
	}); err != nil {
		t.Fatalf("create n-sys: %v", err)
	}
	if err := st.CreateNotification(ctx, Notification{
		ID:                 "n-ext",
		RecipientProfileID: "profile-default",
		Kind:               NotificationExtensionRequest,
		Title:              "More time?",
		Ref:                "ext-req-123",
		LibraryID:          "library-local",
	}); err != nil {
		t.Fatalf("create n-ext: %v", err)
	}

	// Both are unread; list returns newest first (n-ext created after n-sys).
	list, err := st.ListNotifications(ctx, "profile-default", revealed, false, 50)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list returned %d notifications, want 2", len(list))
	}
	// The extension-request notification round-trips its opaque ref unchanged.
	var sawRef bool
	for _, n := range list {
		if n.ID == "n-ext" {
			sawRef = n.Ref == "ext-req-123"
		}
		if !n.ReadAt.IsZero() {
			t.Errorf("notification %s is read before any mark", n.ID)
		}
	}
	if !sawRef {
		t.Error("n-ext did not round-trip its ref")
	}

	count, err := st.UnreadNotificationCount(ctx, "profile-default", revealed)
	if err != nil {
		t.Fatalf("unread count: %v", err)
	}
	if count != 2 {
		t.Fatalf("unread count = %d, want 2", count)
	}

	// Mark one read.
	found, err := st.MarkNotificationRead(ctx, "n-sys", "profile-default", revealed)
	if err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if !found {
		t.Fatal("mark read reported not found for an owned, visible notification")
	}

	// Marking again is idempotent and still reports found.
	found, err = st.MarkNotificationRead(ctx, "n-sys", "profile-default", revealed)
	if err != nil {
		t.Fatalf("mark read again: %v", err)
	}
	if !found {
		t.Fatal("re-mark read reported not found")
	}

	// Unread count drops to 1; unreadOnly list omits the read one.
	count, err = st.UnreadNotificationCount(ctx, "profile-default", revealed)
	if err != nil {
		t.Fatalf("unread count after mark: %v", err)
	}
	if count != 1 {
		t.Fatalf("unread count after mark = %d, want 1", count)
	}
	unread, err := st.ListNotifications(ctx, "profile-default", revealed, true, 50)
	if err != nil {
		t.Fatalf("list unread: %v", err)
	}
	if len(unread) != 1 || unread[0].ID != "n-ext" {
		t.Fatalf("unreadOnly list = %v, want only n-ext", unread)
	}
}

// --- Reveal-filtering conformance (ADR-0039 §2.5 + invariant: reveal contract) -
//
// The hard rule: a notification referencing restricted content (library_id of a
// restricted Library) MUST NOT be returned to an unrevealed caller, must not
// appear in the unread count, and must not be markable. Mirrors the store's
// TestSemanticRevealContract. The conformance seed gives:
//   - library-local   (not restricted)
//   - library-archive (is_restricted = 1)
// profile-default can see both when revealed, only library-local when not.

func TestNotificationRevealContract(t *testing.T) {
	st := openConformanceSeed(t)
	ctx := context.Background()

	// A notice with NO content reference — visible regardless of reveal state.
	if err := st.CreateNotification(ctx, Notification{
		ID:                 "n-open",
		RecipientProfileID: "profile-default",
		Kind:               NotificationSystem,
		Title:              "System notice",
	}); err != nil {
		t.Fatalf("create n-open: %v", err)
	}
	// A notice referencing RESTRICTED content (library-archive).
	if err := st.CreateNotification(ctx, Notification{
		ID:                 "n-restricted",
		RecipientProfileID: "profile-default",
		Kind:               NotificationExtensionRequest,
		Title:              "Restricted content event",
		LibraryID:          "library-archive",
	}); err != nil {
		t.Fatalf("create n-restricted: %v", err)
	}

	unrevealed := LibraryFilter{Revealed: false}
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}

	// Unrevealed caller: the restricted notification must be invisible in the
	// list, absent from the unread count, and unmarkable.
	list, err := st.ListNotifications(ctx, "profile-default", unrevealed, false, 50)
	if err != nil {
		t.Fatalf("list unrevealed: %v", err)
	}
	for _, n := range list {
		if n.ID == "n-restricted" {
			t.Fatalf("ListNotifications leaked restricted n-restricted to unrevealed caller")
		}
	}
	// The content-free notice is still visible to its recipient.
	var sawOpen bool
	for _, n := range list {
		if n.ID == "n-open" {
			sawOpen = true
		}
	}
	if !sawOpen {
		t.Fatal("content-free n-open should be visible to an unrevealed recipient")
	}

	count, err := st.UnreadNotificationCount(ctx, "profile-default", unrevealed)
	if err != nil {
		t.Fatalf("count unrevealed: %v", err)
	}
	if count != 1 {
		t.Fatalf("unrevealed unread count = %d, want 1 (only n-open, not the restricted one)", count)
	}

	// Marking the restricted notification read as an unrevealed caller is a
	// not-found no-op — never an existence oracle, never a write.
	found, err := st.MarkNotificationRead(ctx, "n-restricted", "profile-default", unrevealed)
	if err != nil {
		t.Fatalf("mark restricted unrevealed: %v", err)
	}
	if found {
		t.Fatal("MarkNotificationRead reported found for a restricted notification to an unrevealed caller")
	}
	// Prove it was NOT marked: a revealed caller still sees it as unread.
	revList, err := st.ListNotifications(ctx, "profile-default", revealed, true, 50)
	if err != nil {
		t.Fatalf("list revealed unread: %v", err)
	}
	var restrictedStillUnread bool
	for _, n := range revList {
		if n.ID == "n-restricted" {
			restrictedStillUnread = n.ReadAt.IsZero()
		}
	}
	if !restrictedStillUnread {
		t.Fatal("restricted notification was marked read by an unrevealed caller (reveal bypass)")
	}

	// Revealed caller: both notifications are visible.
	revealedList, err := st.ListNotifications(ctx, "profile-default", revealed, false, 50)
	if err != nil {
		t.Fatalf("list revealed: %v", err)
	}
	var sawRestricted bool
	for _, n := range revealedList {
		if n.ID == "n-restricted" {
			sawRestricted = true
		}
	}
	if !sawRestricted {
		t.Fatal("revealed caller should see the restricted notification")
	}
}

// --- Recipient-gating: a Profile reads/marks only its own notifications ------

func TestNotificationRecipientGating(t *testing.T) {
	st := openConformanceSeed(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}

	// Address a notification to profile-default only.
	if err := st.CreateNotification(ctx, Notification{
		ID:                 "n-for-default",
		RecipientProfileID: "profile-default",
		Kind:               NotificationSystem,
		Title:              "Private to default",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// profile-managed must NOT see it.
	managedList, err := st.ListNotifications(ctx, "profile-managed", filter, false, 50)
	if err != nil {
		t.Fatalf("list managed: %v", err)
	}
	for _, n := range managedList {
		if n.ID == "n-for-default" {
			t.Fatal("profile-managed read another Profile's notification")
		}
	}
	if c, _ := st.UnreadNotificationCount(ctx, "profile-managed", filter); c != 0 {
		t.Fatalf("profile-managed unread count = %d, want 0 (notification is not theirs)", c)
	}

	// profile-managed cannot mark it read — reported not found, not mutated.
	found, err := st.MarkNotificationRead(ctx, "n-for-default", "profile-managed", filter)
	if err != nil {
		t.Fatalf("managed mark: %v", err)
	}
	if found {
		t.Fatal("profile-managed marked another Profile's notification read")
	}
	// Owner still sees it unread.
	ownerList, err := st.ListNotifications(ctx, "profile-default", filter, true, 50)
	if err != nil {
		t.Fatalf("owner list: %v", err)
	}
	var ownerSeesUnread bool
	for _, n := range ownerList {
		if n.ID == "n-for-default" {
			ownerSeesUnread = n.ReadAt.IsZero()
		}
	}
	if !ownerSeesUnread {
		t.Fatal("owner's notification was marked read by a non-recipient")
	}

	// The owner can read and mark it.
	found, err = st.MarkNotificationRead(ctx, "n-for-default", "profile-default", filter)
	if err != nil {
		t.Fatalf("owner mark: %v", err)
	}
	if !found {
		t.Fatal("owner could not mark their own notification read")
	}
}
