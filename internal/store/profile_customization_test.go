package store

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func profileOrder(t *testing.T, st *Store) []ProfileID {
	t.Helper()
	profiles, err := st.Profiles(context.Background())
	if err != nil {
		t.Fatalf("Profiles returned error: %v", err)
	}
	ids := make([]ProfileID, 0, len(profiles))
	for _, profile := range profiles {
		ids = append(ids, profile.ID)
	}
	return ids
}

func TestReorderProfilesPersistsAndAppendsNewProfiles(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	before := profileOrder(t, st)
	if len(before) < 2 {
		t.Fatalf("seed has %d profiles, want at least 2", len(before))
	}

	reversed := slices.Clone(before)
	slices.Reverse(reversed)
	if err := st.ReorderProfiles(ctx, reversed); err != nil {
		t.Fatalf("ReorderProfiles returned error: %v", err)
	}
	if got := profileOrder(t, st); !slices.Equal(got, reversed) {
		t.Fatalf("order after reorder = %v, want %v", got, reversed)
	}

	// Listing only the last Profile moves it first and keeps the rest in order.
	last := reversed[len(reversed)-1]
	if err := st.ReorderProfiles(ctx, []ProfileID{last}); err != nil {
		t.Fatalf("partial ReorderProfiles returned error: %v", err)
	}
	want := append([]ProfileID{last}, reversed[:len(reversed)-1]...)
	if got := profileOrder(t, st); !slices.Equal(got, want) {
		t.Fatalf("order after partial reorder = %v, want %v", got, want)
	}

	// A new Profile goes last, whatever its name.
	created, err := st.CreateProfile(ctx, "Aaron", ProfileAdult, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("CreateProfile returned error: %v", err)
	}
	got := profileOrder(t, st)
	if got[len(got)-1] != created.ID {
		t.Fatalf("order after create = %v, want %s last", got, created.ID)
	}
}

func TestReorderProfilesRejectsUnknownAndDuplicateIDs(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	before := profileOrder(t, st)
	for name, ids := range map[string][]ProfileID{
		"empty":     nil,
		"unknown":   {"profile-nobody"},
		"duplicate": {before[0], before[0]},
		"blank":     {""},
	} {
		var validation ValidationError
		if err := st.ReorderProfiles(ctx, ids); !errors.As(err, &validation) {
			t.Fatalf("%s: ReorderProfiles error = %v, want ValidationError", name, err)
		}
	}
	if got := profileOrder(t, st); !slices.Equal(got, before) {
		t.Fatalf("order after rejected reorders = %v, want unchanged %v", got, before)
	}
}

func TestProfilePictureRoundTripAndClear(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	if _, _, err := st.ProfilePicture(ctx, "profile-default"); !errors.Is(err, ErrNoProfilePicture) {
		t.Fatalf("ProfilePicture before set error = %v, want ErrNoProfilePicture", err)
	}
	image := []byte{0xff, 0xd8, 0xff, 0xd9}
	version, err := st.SetProfilePicture(ctx, "profile-default", image, now)
	if err != nil {
		t.Fatalf("SetProfilePicture returned error: %v", err)
	}
	if version != now.UnixMilli() {
		t.Fatalf("version = %d, want %d", version, now.UnixMilli())
	}
	got, gotVersion, err := st.ProfilePicture(ctx, "profile-default")
	if err != nil || !bytes.Equal(got, image) || gotVersion != version {
		t.Fatalf("ProfilePicture = %x, %d, %v; want %x, %d", got, gotVersion, err, image, version)
	}
	versions, err := st.ProfilePictureVersions(ctx)
	if err != nil || len(versions) != 1 || versions["profile-default"] != version {
		t.Fatalf("ProfilePictureVersions = %v, %v; want only profile-default", versions, err)
	}

	if err := st.ClearProfilePicture(ctx, "profile-default"); err != nil {
		t.Fatalf("ClearProfilePicture returned error: %v", err)
	}
	if _, _, err := st.ProfilePicture(ctx, "profile-default"); !errors.Is(err, ErrNoProfilePicture) {
		t.Fatalf("ProfilePicture after clear error = %v, want ErrNoProfilePicture", err)
	}
	if err := st.ClearProfilePicture(ctx, "profile-default"); err != nil {
		t.Fatalf("clearing an absent picture returned error: %v", err)
	}
}

// A replacement in the same millisecond still gets a new version: the version
// is the picture URL's immutable cache identity.
func TestSetProfilePictureVersionAlwaysAdvances(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	first, err := st.SetProfilePicture(ctx, "profile-default", []byte{1}, now)
	if err != nil {
		t.Fatalf("first SetProfilePicture: %v", err)
	}
	second, err := st.SetProfilePicture(ctx, "profile-default", []byte{2}, now)
	if err != nil {
		t.Fatalf("second SetProfilePicture: %v", err)
	}
	if second <= first {
		t.Fatalf("second version = %d, want > %d", second, first)
	}
	got, gotVersion, err := st.ProfilePicture(ctx, "profile-default")
	if err != nil || !bytes.Equal(got, []byte{2}) || gotVersion != second {
		t.Fatalf("ProfilePicture = %x, %d, %v; want 02, %d", got, gotVersion, err, second)
	}
}

func TestSetProfilePictureRejectsUnknownEmptyAndOversized(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now()
	for name, call := range map[string]func() error{
		"unknown": func() error { _, err := st.SetProfilePicture(ctx, "profile-nobody", []byte{1}, now); return err },
		"empty":   func() error { _, err := st.SetProfilePicture(ctx, "profile-default", nil, now); return err },
		"oversized": func() error {
			_, err := st.SetProfilePicture(ctx, "profile-default", make([]byte, MaxProfilePictureBytes+1), now)
			return err
		},
	} {
		var validation ValidationError
		if err := call(); !errors.As(err, &validation) {
			t.Fatalf("%s: error = %v, want ValidationError", name, err)
		}
	}
}
