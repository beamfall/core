package store

import (
	"errors"
	"testing"
)

func TestQueueAddRefusesAfterRelock(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	const profile = "profile-default"
	const session = "session-revealed"

	preflight := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}
	if _, err := st.Media(ctx, "media-archive", preflight); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if err := st.QueueAdd(ctx, profile, "media-alpha", nil, preflight); err != nil {
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
	if err := st.QueueAdd(ctx, profile, "media-alpha", nil, preflight); err != nil {
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
