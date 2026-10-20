package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestChannelCRUDBothSourceFormsAndOrderings pins CHAN-1:
// a Channel round-trips both source forms — a smart-collection reference and an
// inline query — and both ordering policies, storing only the source expression
// and policy inputs (never a generated track order).
func TestChannelCRUDBothSourceFormsAndOrderings(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Unix(1000, 0).UTC()

	// Form 1: an inline-query source with the chronological ordering policy.
	inline, err := st.CreateChannel(ctx, Channel{
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
		Name:       "  Chronology  ",
		SourceExpr: `{"kind":"all-items"}`,
		Ordering:   ChannelOrderingChronological,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if inline.Name != "Chronology" {
		t.Fatalf("name not trimmed: %q", inline.Name)
	}
	if !strings.HasPrefix(string(inline.ID), "channel-") {
		t.Fatalf("id = %q, want channel-… prefix", inline.ID)
	}
	if inline.Ordering != ChannelOrderingChronological {
		t.Fatalf("ordering = %q, want chronological", inline.Ordering)
	}
	if inline.Repeat != ChannelRepeatOff {
		t.Fatalf("repeat defaulted to %q, want off", inline.Repeat)
	}

	// Form 2: a smart-collection-reference source with the shuffle ordering
	// policy, and a non-default repeat policy.
	ref, err := st.CreateChannel(ctx, Channel{
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
		Name:       "Shuffle Ref",
		SourceExpr: `{"kind":"smart-collection","value":"collection-xyz"}`,
		Ordering:   ChannelOrderingShuffle,
		Repeat:     ChannelRepeatAll,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Ordering != ChannelOrderingShuffle || ref.Repeat != ChannelRepeatAll {
		t.Fatalf("policies = %q/%q, want shuffle/all", ref.Ordering, ref.Repeat)
	}

	// List returns both, newest first, with the source expressions verbatim.
	channels, err := st.Channels(ctx, "profile-default", "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 2 {
		t.Fatalf("Channels len = %d, want 2", len(channels))
	}
	if channels[0].ID != ref.ID || channels[1].ID != inline.ID {
		t.Fatalf("order = %v, want newest first", []ChannelID{channels[0].ID, channels[1].ID})
	}
	if channels[1].SourceExpr != `{"kind":"all-items"}` {
		t.Fatalf("source expr not verbatim: %q", channels[1].SourceExpr)
	}

	// Edit: rename and swap the inline channel's ordering; unspecified fields
	// (source expr, repeat) survive.
	updated, err := st.UpdateChannel(ctx, Channel{ID: inline.ID, Name: "Chronology 2", Ordering: ChannelOrderingShuffle})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Chronology 2" || updated.Ordering != ChannelOrderingShuffle {
		t.Fatalf("update = %q/%q, want Chronology 2/shuffle", updated.Name, updated.Ordering)
	}
	if updated.SourceExpr != `{"kind":"all-items"}` {
		t.Fatalf("update clobbered source expr: %q", updated.SourceExpr)
	}

	// Delete removes it; a second delete fails gracefully.
	if _, err := st.DeleteChannel(ctx, inline.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeleteChannel(ctx, inline.ID); err != ErrChannelNotFound {
		t.Fatalf("re-delete err = %v, want ErrChannelNotFound", err)
	}
	remaining, err := st.Channels(ctx, "profile-default", "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != ref.ID {
		t.Fatalf("remaining = %v, want [%s]", remaining, ref.ID)
	}
}

// TestChannelRejectsInvalidPolicies pins the ordering/repeat validation.
func TestChannelRejectsInvalidPolicies(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Unix(1000, 0).UTC()

	if _, err := st.CreateChannel(ctx, Channel{
		ProfileID: "profile-default",
		LibraryID: "library-local",
		Name:      "Bad Ordering",
		Ordering:  "marathon",
	}, now); err == nil {
		t.Fatal("marathon ordering must be rejected in v1")
	}
	if _, err := st.CreateChannel(ctx, Channel{
		ProfileID: "profile-default",
		LibraryID: "library-local",
		Name:      "Bad Repeat",
		Repeat:    "bounce",
	}, now); err == nil {
		t.Fatal("unknown repeat policy must be rejected")
	}
}

// TestChannelSharedVisibleAlongsideProfile pins the union read: a server-shared
// Channel (empty ProfileID) and a per-Profile Channel both surface for the
// owning Profile.
func TestChannelSharedVisibleAlongsideProfile(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Unix(1000, 0).UTC()

	shared, err := st.CreateChannel(ctx, Channel{
		LibraryID:  "library-local",
		Name:       "House Channel",
		SourceExpr: `{"kind":"all-items"}`,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if shared.ProfileID != "" {
		t.Fatalf("shared channel profile = %q, want empty", shared.ProfileID)
	}
	if _, err := st.CreateChannel(ctx, Channel{
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
		Name:       "My Channel",
		SourceExpr: `{"kind":"all-items"}`,
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	channels, err := st.Channels(ctx, "profile-default", "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 2 {
		t.Fatalf("Channels len = %d, want 2 (shared + own)", len(channels))
	}
}
