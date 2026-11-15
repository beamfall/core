package app

import (
	"context"
	"testing"
	"time"

	sdk "github.com/beamfall/plugin-sdk"
	scrobble "github.com/beamfall/plugin-sdk/scrobble"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const (
	scrobbleTargetFixturePlugin = "scrobble-target-fixture"
	scrobbleTargetFixtureField  = "shape"
	scrobbleTargetSkipReason    = "scrobble target hint is absent or unknown"
)

// TestScrobbleWatchedObserverResolvesTargetHint drives a watched transition
// through the composition-root adapter into a real dispatcher and asserts the
// TargetHint comes from the Library plugin's declared ScrobbleTargetSpec:
// resolved hints are submitted, every miss is skipped (fail closed).
func TestScrobbleWatchedObserverResolvesTargetHint(t *testing.T) {
	mapped := &sdk.ScrobbleTargetSpec{
		MediaField: scrobbleTargetFixtureField,
		Mappings: []sdk.ScrobbleTargetMapping{
			{Value: "single", Hint: scrobble.TargetStandalone},
			{Value: "part", Hint: scrobble.TargetIndexed},
		},
	}
	tests := []struct {
		name       string
		spec       *sdk.ScrobbleTargetSpec
		fieldValue string // "" leaves the Media field unset
		wantHint   scrobble.TargetHint
	}{
		{name: "default hint", spec: &sdk.ScrobbleTargetSpec{Default: scrobble.TargetStandalone}, wantHint: scrobble.TargetStandalone},
		{name: "mapped field standalone", spec: mapped, fieldValue: "single", wantHint: scrobble.TargetStandalone},
		{name: "mapped field indexed", spec: mapped, fieldValue: "part", wantHint: scrobble.TargetIndexed},
		{name: "unmapped field value", spec: mapped, fieldValue: "other"},
		{name: "absent field value", spec: mapped},
		{name: "plugin without spec", spec: nil, fieldValue: "single"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			st := storetest.Open(t)
			bundle := testBundle(scrobbleTargetFixturePlugin)
			bundle.MediaFields = []plugin.FieldSpec{{Name: scrobbleTargetFixtureField, Type: "text"}}
			bundle.ScrobbleTarget = tt.spec
			registry, err := plugin.NewRegistry([]plugin.Bundle{bundle})
			if err != nil {
				t.Fatalf("NewRegistry: %v", err)
			}
			library, err := st.CreateLibrary(ctx, "Fixture Library", false, scrobbleTargetFixturePlugin, "profile-default")
			if err != nil {
				t.Fatalf("CreateLibrary: %v", err)
			}
			media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
				LibraryID: library.ID, Path: "/fixture/item.mkv", Title: "Fixture Item", Container: "mkv", Codec: "h264",
				Class: store.MediaClassVideo, ContentFingerprint: "sha256:scrobble-target-fixture",
			})
			if err != nil {
				t.Fatalf("ImportMediaFile: %v", err)
			}
			if tt.fieldValue != "" {
				if err := st.SetMediaFields(ctx, media.ID, []store.MediaField{
					{Name: scrobbleTargetFixtureField, Type: store.EntityFieldText, Text: tt.fieldValue},
				}); err != nil {
					t.Fatalf("SetMediaFields: %v", err)
				}
			}

			doer := newRecordingScrobbleDoer()
			dispatcher := newBoundaryScrobbleDispatcher(t, st, doer, "https://allowed.example/scrobble", []string{"https://allowed.example"})
			if err := st.SetScrobbleLibraryEligibility(ctx, library.ID, true); err != nil {
				t.Fatalf("SetScrobbleLibraryEligibility: %v", err)
			}
			runCtx, cancel := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() {
				dispatcher.Run(runCtx)
				close(done)
			}()
			defer func() {
				cancel()
				<-done
			}()

			resolver := newScrobbleTargetResolver(st, registry)
			if got := resolver.resolve(ctx, library.ID, media.ID); got != tt.wantHint {
				t.Fatalf("resolved hint = %q, want %q", got, tt.wantHint)
			}
			scrobbleWatchedObserver{dispatcher: dispatcher, targets: resolver}.Watched(graph.WatchedTransition{
				ProfileID:   "profile-default",
				MediaID:     media.ID,
				LibraryID:   library.ID,
				Class:       store.MediaClassVideo,
				Title:       "Fixture Item",
				ExternalIDs: []store.MediaExternalID{{Scheme: "boundary", Value: "42"}},
				WatchedAt:   time.Unix(1_700_000_000, 0).UTC(),
			})

			if tt.wantHint != "" {
				select {
				case <-doer.done:
				case <-time.After(2 * time.Second):
					t.Fatalf("watched event was not submitted; skips = %#v", dispatcher.Skips())
				}
				for _, skip := range dispatcher.Skips() {
					if skip.Reason == scrobbleTargetSkipReason {
						t.Fatalf("resolved event recorded target-hint skip: %#v", skip)
					}
				}
				return
			}
			deadline := time.After(2 * time.Second)
			for len(dispatcher.Skips()) == 0 {
				select {
				case <-deadline:
					t.Fatal("timed out waiting for target-hint skip")
				case <-time.After(10 * time.Millisecond):
				}
			}
			if got := dispatcher.Skips()[0]; got.Reason != scrobbleTargetSkipReason {
				t.Fatalf("skip = %#v, want reason %q", got, scrobbleTargetSkipReason)
			}
			if got := doer.count(); got != 0 {
				t.Fatalf("base Doer calls = %d, want 0", got)
			}
		})
	}
}
