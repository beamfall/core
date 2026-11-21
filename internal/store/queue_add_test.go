package store

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func queueAddFixture(ctx context.Context, st *Store, profileID ProfileID, mediaID MediaID, position *int, filter LibraryFilter) error {
	var sessionID SessionID
	if profileID == "profile-default" {
		sessionID = "session-revealed"
	}
	_, err := st.QueueAddForProfile(ctx, profileID, sessionID, mediaID, position, filter, testQueueCertification)
	return err
}

func TestQueueAddResponseMatchesQueueReadAfterMute(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	const profile = "profile-default"
	const session = "session-default"
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	target := importQueueMedia(t, st, "queue-after-mute")
	for _, id := range []MediaID{"media-alpha", target} {
		if _, err := st.QueueAddForProfile(ctx, profile, session, id, nil, filter, testQueueCertification); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetMute(ctx, profile, MuteKindMedia, "media-alpha", false, true); err != nil {
		t.Fatal(err)
	}
	newTarget := importQueueMedia(t, st, "queue-after-mute-tail")
	committed, err := st.QueueAddForProfile(ctx, profile, session, newTarget, nil, filter, testQueueCertification)
	if err != nil {
		t.Fatal(err)
	}
	query := SourceQuery{Kind: "queue", ProfileID: profile}
	read := mustSourceItems(t, st, filter, query)
	if len(read) != 2 || read[0].ID != target || read[1].ID != newTarget {
		t.Fatalf("queue read after mute = %#v", read)
	}
	if !slices.EqualFunc(committed.Items, read, func(a, b SourceItem) bool { return a.ID == b.ID }) {
		t.Fatalf("queueAdd response = %#v, want the same order as the queue read", committed.Items)
	}
	if err := st.SetMute(ctx, profile, MuteKindMedia, "media-alpha", false, false); err != nil {
		t.Fatal(err)
	}
	assertQueueIDs(t, st, filter, profile, []MediaID{"media-alpha", target, newTarget})
}

func TestQueueAddWithoutSessionCannotUseReveal(t *testing.T) {
	st := openSeeded(t)
	const profile = "profile-default"
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-archive"}, Revealed: true}
	_, err := st.QueueAddForProfile(t.Context(), profile, "", "media-archive", nil, filter, testQueueCertification)
	if !errors.Is(err, ErrQueueMediaNotFound) {
		t.Fatalf("queue add without a Reveal session = %v, want hidden-target refusal", err)
	}
	assertQueueIDs(t, st, filter, profile, nil)
}

func TestQueueAddRefusesAfterRelock(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	const profile = "profile-default"
	const session = "session-revealed"

	preflight := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}
	if _, err := st.Media(ctx, "media-archive", preflight); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if err := queueAddFixture(ctx, st, profile, "media-alpha", nil, preflight); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionRevealed(ctx, session, false); err != nil {
		t.Fatal(err)
	}

	for _, position := range []*int{queueIntPtr(0), nil} {
		_, err := st.QueueAddForProfile(ctx, profile, session, "media-archive", position, preflight, testQueueCertification)
		if !errors.Is(err, ErrQueueMediaNotFound) {
			t.Fatalf("queue add after relock = %v, want hidden-target refusal", err)
		}
		assertQueueIDs(t, st, preflight, profile, []MediaID{"media-alpha"})
	}
}

func TestQueueAddChecksCurrentCertification(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	const profile = "profile-default"
	const session = "session-default"
	library, ids := seedCertifiedLibrary(t, st, "starlog", false, "low", "high")
	preflight := LibraryFilter{AllowedIDs: []LibraryID{"library-local", library}}
	if _, err := st.Media(ctx, ids["high"], preflight); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if _, err := st.SetProfileCertificationCeiling(ctx, profile, ProfileCertificationCeiling{
		PluginID: "starlog", Ceiling: "low", FilterVisibility: "locked",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := st.QueueAddForProfile(ctx, profile, session, ids["low"], nil, preflight, testQueueCertification); err != nil {
		t.Fatalf("queue eligible media: %v", err)
	}
	if err := queueAddFixture(ctx, st, profile, "media-alpha", nil, preflight); err != nil {
		t.Fatal(err)
	}
	_, err := st.QueueAddForProfile(ctx, profile, session, ids["high"], queueIntPtr(0), preflight, testQueueCertification)
	if !errors.Is(err, ErrQueueMediaLocked) {
		t.Fatalf("queue add after ceiling change = %v, want locked-target refusal", err)
	}
	assertQueueIDs(t, st, preflight, profile, []MediaID{ids["low"], "media-alpha"})

	if err := st.SetMediaFields(ctx, ids["low"], []MediaField{
		{Name: certificationField, Type: EntityFieldText, Text: "high"},
	}); err != nil {
		t.Fatal(err)
	}
	_, err = st.QueueAddForProfile(ctx, profile, session, ids["low"], nil, preflight, testQueueCertification)
	if !errors.Is(err, ErrQueueMediaLocked) {
		t.Fatalf("queue add after recertification = %v, want locked-target refusal", err)
	}
	assertQueueIDs(t, st, preflight, profile, []MediaID{ids["low"], "media-alpha"})
}

func testQueueCertification(ceilings []ProfileCertificationCeiling) []CertificationConstraint {
	var constraints []CertificationConstraint
	for _, ceiling := range ceilings {
		constraints = append(constraints, CertificationConstraint{
			PluginID: ceiling.PluginID, Allowed: []string{ceiling.Ceiling}, Locked: ceiling.FilterVisibility == "locked",
		})
	}
	return constraints
}
