package store

import (
	"context"
	"slices"
	"sync"
	"testing"
)

// TestProfileTrackPreferenceWriteKeepsConcurrentAdminChange pins CORE-1937: a
// Profile's own track preference write updates only the columns it supplies,
// so racing an Admin change of kind, disabled and stream policy, both writes
// survive in either order.
func TestProfileTrackPreferenceWriteKeepsConcurrentAdminChange(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	const id ProfileID = "profile-managed"

	// The deterministic interleaving: the track write merged its row before
	// the Admin change committed, and lands after it.
	stale, err := st.Profile(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	disabled, kind, streams := true, ProfileAdult, 2
	if _, err := st.UpdateProfileSettings(ctx, id, ProfileSettings{Disabled: &disabled, Kind: &kind, MaxConcurrentStreams: &streams}); err != nil {
		t.Fatal(err)
	}
	languages := []string{"ja"}
	stale.AudioLanguages = languages
	if err := st.updateProfileSettingsSQL(ctx, stale, ProfileSettings{AudioLanguages: &languages}); err != nil {
		t.Fatal(err)
	}
	assertBothProfileWrites(t, st, id, languages, true, ProfileAdult, 2)

	// Truly concurrent writers, repeated so a whole-row write would lose one.
	for round := range 20 {
		disabled := round%2 == 0
		languages := []string{"ja", string(rune('a' + round))}
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		wg.Go(func() {
			_, err := st.UpdateProfileSettings(ctx, id, ProfileSettings{Disabled: &disabled})
			errs <- err
		})
		wg.Go(func() {
			_, err := st.UpdateProfileSettings(ctx, id, ProfileSettings{AudioLanguages: &languages})
			errs <- err
		})
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		assertBothProfileWrites(t, st, id, languages, disabled, ProfileAdult, 2)
	}
}

func assertBothProfileWrites(t *testing.T, st *Store, id ProfileID, languages []string, disabled bool, kind ProfileKind, streams int) {
	t.Helper()
	got, err := st.Profile(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.AudioLanguages, languages) || got.Disabled != disabled || got.Kind != kind ||
		got.MaxConcurrentStreams == nil || *got.MaxConcurrentStreams != streams {
		t.Fatalf("profile = %+v; want audio %v, disabled %v, kind %s, %d streams", got, languages, disabled, kind, streams)
	}
}
