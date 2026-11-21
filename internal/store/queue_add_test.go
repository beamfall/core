package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func queueAddFixture(ctx context.Context, st *Store, profileID ProfileID, mediaID MediaID, position *int, filter LibraryFilter) error {
	var sessionID SessionID
	if profileID == "profile-default" {
		sessionID = "session-revealed"
	}
	_, err := st.QueueAddForProfile(ctx, profileID, sessionID, "", mediaID, position, filter, testQueueCertification)
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
		if _, err := st.QueueAddForProfile(ctx, profile, session, "", id, nil, filter, testQueueCertification); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetMute(ctx, profile, MuteKindMedia, "media-alpha", false, true); err != nil {
		t.Fatal(err)
	}
	newTarget := importQueueMedia(t, st, "queue-after-mute-next")
	committed, err := st.QueueAddForProfile(ctx, profile, session, "", newTarget, queueIntPtr(1), filter, testQueueCertification)
	if err != nil {
		t.Fatal(err)
	}
	query := SourceQuery{Kind: "queue", ProfileID: profile}
	read := mustSourceItems(t, st, filter, query)
	if len(read) != 2 || read[0].ID != newTarget || read[1].ID != target {
		t.Fatalf("queue read after mute = %#v", read)
	}
	if !slices.EqualFunc(committed.Items, read, func(a, b SourceItem) bool { return a.ID == b.ID }) {
		t.Fatalf("queueAdd response = %#v, want the same order as the queue read", committed.Items)
	}
	query.Limit, query.Offset = 1, 1
	if page := mustSourceItems(t, st, filter, query); len(page) != 1 || page[0].ID != target {
		t.Fatalf("second visible page = %#v, want %s", page, target)
	}
	if err := st.SetMute(ctx, profile, MuteKindMedia, "media-alpha", false, false); err != nil {
		t.Fatal(err)
	}
	assertQueueIDs(t, st, filter, profile, []MediaID{"media-alpha", newTarget, target})
}

func TestQueueAddCommitsMutedTargetsWithoutReturningThem(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	const profile = "profile-default"
	const session = "session-default"
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	queued := importQueueMedia(t, st, "queue-muted-existing")
	unqueued := importQueueMedia(t, st, "queue-muted-new")
	for _, id := range []MediaID{"media-alpha", queued} {
		if _, err := st.QueueAddForProfile(ctx, profile, session, "", id, nil, filter, testQueueCertification); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []MediaID{queued, unqueued} {
		if err := st.SetMute(ctx, profile, MuteKindMedia, string(id), false, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []MediaID{queued, unqueued} {
		committed, err := st.QueueAddForProfile(ctx, profile, session, "", id, queueIntPtr(0), filter, testQueueCertification)
		if err != nil {
			t.Fatalf("queue add for muted target %s: %v", id, err)
		}
		if len(committed.Items) != 1 || committed.Items[0].ID != "media-alpha" {
			t.Fatalf("queue add response for muted target %s = %#v, want only media-alpha", id, committed.Items)
		}
	}
	assertQueueIDs(t, st, filter, profile, []MediaID{"media-alpha"})
	for _, id := range []MediaID{queued, unqueued} {
		if err := st.SetMute(ctx, profile, MuteKindMedia, string(id), false, false); err != nil {
			t.Fatal(err)
		}
	}
	assertQueueIDs(t, st, filter, profile, []MediaID{unqueued, queued, "media-alpha"})
}

func TestQueueAddMatchesQueueReadWithEncryptedCatalogMutes(t *testing.T) {
	for _, kind := range []MuteKind{MuteKindEntity, MuteKindTag} {
		t.Run(string(kind), func(t *testing.T) {
			st, library, _, entity, muted := openEncryptedPackLibrary(t)
			ctx := t.Context()
			const profile = "profile-default"
			const session = "session-revealed"
			filter := LibraryFilter{AllowedIDs: []LibraryID{library, "library-archive"}, Revealed: true}
			other, _, err := st.ImportMediaFile(ctx, encryptedImport(library, "sha256:enc-queue-visible", "/enc/queue-visible.mkv"))
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []MediaID{muted, other.ID} {
				if _, err := st.QueueAddForProfile(ctx, profile, session, "", id, nil, filter, testQueueCertification); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.SetMute(ctx, profile, kind, string(entity.ID), false, true); err != nil {
				t.Fatal(err)
			}
			if _, err := st.QueueAddForProfile(ctx, profile, session, "", muted, nil, filter, testQueueCertification); err != nil {
				t.Fatalf("queue add for muted encrypted target: %v", err)
			}
			committed, err := st.QueueAddForProfile(ctx, profile, session, "", "media-archive", queueIntPtr(1), filter, testQueueCertification)
			if err != nil {
				t.Fatal(err)
			}
			// Like the queue read, the response resolves entity and tag mutes
			// against the main catalog, where this encrypted relation is absent.
			want := []MediaID{other.ID, "media-archive", muted}
			if !slices.EqualFunc(committed.Items, want, func(item SourceItem, id MediaID) bool { return item.ID == id }) {
				t.Errorf("committed response = %#v, want %v", committed.Items, want)
			}
			assertQueueIDs(t, st, filter, profile, want)
			cipher := copyEncryptedLibraryDBCipher{}
			if err := st.SealEncryptedLibraryDB(ctx, library, cipher); err != nil {
				t.Fatal(err)
			}
			assertQueueIDs(t, st, filter, profile, []MediaID{"media-archive"})
			if _, err := st.QueueAddForProfile(ctx, profile, session, "", muted, nil, filter, testQueueCertification); !errors.Is(err, ErrQueueMediaNotFound) {
				t.Errorf("queue add for sealed target = %v, want hidden-target refusal", err)
			}
			if err := st.UnsealEncryptedLibraryDB(ctx, library, cipher); err != nil {
				t.Fatal(err)
			}
			assertQueueIDs(t, st, filter, profile, want)
		})
	}
}

func TestQueueAddRefusesInvalidatedRevealSession(t *testing.T) {
	for _, test := range []struct {
		name       string
		invalidate func(context.Context, *Store) error
		want       error
	}{
		{"revoked device", func(ctx context.Context, st *Store) error { return st.RevokeDevice(ctx, "device-tv") }, ErrDeviceRevoked},
		{"expired session", func(ctx context.Context, st *Store) error {
			_, err := st.catalogW.ExecContext(ctx, `UPDATE auth_session SET expires_at_ms = ? WHERE id = 'session-revealed'`, timeToMillis(time.Now().Add(-time.Minute)))
			return err
		}, ErrSessionExpired},
	} {
		t.Run(test.name, func(t *testing.T) {
			st := openSeeded(t)
			ctx := t.Context()
			const profile = "profile-default"
			const sessionID = "session-revealed"
			filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}
			if _, err := st.Media(ctx, "media-archive", filter); err != nil {
				t.Fatalf("preflight: %v", err)
			}
			if _, err := st.QueueAddForProfile(ctx, profile, sessionID, "", "media-alpha", nil, filter, testQueueCertification); err != nil {
				t.Fatal(err)
			}
			if err := test.invalidate(ctx, st); err != nil {
				t.Fatal(err)
			}
			session, err := st.Session(ctx, sessionID)
			if err != nil || !session.Revealed {
				t.Fatalf("fixture must retain Reveal: session = %#v, err = %v", session, err)
			}
			if _, err := st.QueueAddForProfile(ctx, profile, sessionID, "", "media-archive", queueIntPtr(0), filter, testQueueCertification); !errors.Is(err, test.want) {
				t.Errorf("queue add after invalidation = %v, want %v", err, test.want)
			}
			assertQueueIDs(t, st, filter, profile, []MediaID{"media-alpha"})
		})
	}
}

func TestQueueAddWithoutSessionCannotUseReveal(t *testing.T) {
	st := openSeeded(t)
	const profile = "profile-default"
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-archive"}, Revealed: true}
	_, err := st.QueueAddForProfile(t.Context(), profile, "", "", "media-archive", nil, filter, testQueueCertification)
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
		_, err := st.QueueAddForProfile(ctx, profile, session, "", "media-archive", position, preflight, testQueueCertification)
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

	if _, err := st.QueueAddForProfile(ctx, profile, session, "", ids["low"], nil, preflight, testQueueCertification); err != nil {
		t.Fatalf("queue eligible media: %v", err)
	}
	if err := queueAddFixture(ctx, st, profile, "media-alpha", nil, preflight); err != nil {
		t.Fatal(err)
	}
	_, err := st.QueueAddForProfile(ctx, profile, session, "", ids["high"], queueIntPtr(0), preflight, testQueueCertification)
	if !errors.Is(err, ErrQueueMediaLocked) {
		t.Fatalf("queue add after ceiling change = %v, want locked-target refusal", err)
	}
	assertQueueIDs(t, st, preflight, profile, []MediaID{ids["low"], "media-alpha"})

	if err := st.SetMediaFields(ctx, ids["low"], []MediaField{
		{Name: certificationField, Type: EntityFieldText, Text: "high"},
	}); err != nil {
		t.Fatal(err)
	}
	_, err = st.QueueAddForProfile(ctx, profile, session, "", ids["low"], nil, preflight, testQueueCertification)
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
