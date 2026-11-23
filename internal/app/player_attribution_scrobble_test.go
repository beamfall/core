package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/beamfall/plugin-sdk"
	scrobble "github.com/beamfall/plugin-sdk/scrobble"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

func TestPlayerAttributionScrobbleConsentPerOriginator(t *testing.T) {
	ctx := t.Context()
	st := storetest.Open(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	all := store.LibraryFilter{Revealed: true, AllLibraries: true}

	bundle := testBundle("scrobble-attr-plugin")
	bundle.ScrobbleTarget = &sdk.ScrobbleTargetSpec{Default: scrobble.TargetStandalone}
	registry, err := plugin.NewRegistry([]plugin.Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := st.CreateLibrary(ctx, "Scrobble Lib", false, "scrobble-attr-plugin", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	mediaPath := filepath.Join(t.TempDir(), "movie.mp4")
	if err := os.WriteFile(mediaPath, []byte("movie-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: lib.ID, Path: mediaPath, Title: "Test Film", Container: "mp4", Codec: "h264",
		Class: store.MediaClassVideo, ContentFingerprint: "sha256:attr-scrobble-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaExternalID(ctx, media.ID, "boundary", "101"); err != nil {
		t.Fatal(err)
	}

	doer := newRecordingScrobbleDoer()
	// newBoundaryScrobbleDispatcher links ONLY "profile-default" (Alice); Bob has no link (no consent).
	dispatcher := newBoundaryScrobbleDispatcher(t, st, doer, "https://allowed.example/scrobble", []string{"https://allowed.example"})
	if err := st.SetScrobbleLibraryEligibility(ctx, lib.ID, true); err != nil {
		t.Fatal(err)
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

	authSvc := auth.NewServiceWithClock(st, func() time.Time { return now })
	manifests := manifest.NewService(st, func() time.Time { return now }, nil)
	h := graph.NewHandler(manifests, authSvc, st, registry)
	tokens := streamtoken.NewService([]byte("12345678901234567890123456789012"), func() time.Time { return now })
	tokens.SetRecorder(st)
	pb := playback.NewService(st, tokens, func() time.Time { return now })
	pb.SetAuth(authSvc)
	h.SetWatchedObserver(scrobbleWatchedObserver{
		dispatcher: dispatcher,
		targets:    newScrobbleTargetResolver(st, registry),
	})

	player, err := st.CreateProfile(ctx, "Lounge TV", store.ProfilePlayer, all)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{
			ID: "device-tv-scrob", Name: "Lounge TV", Role: store.DeviceRoleViewer,
			Surface: store.DeviceSurfaceTVOSViewer, SecretHash: store.HashDeviceSecret("tv-sec"),
			PlayerProfileID: player.ID, PlayerDisplayName: "Lounge TV", PlayerBindingGeneration: 1,
		},
		store.Session{ID: "sess-tv-scrob", DeviceID: "device-tv-scrob", ProfileID: player.ID, ExpiresAt: now.Add(24 * time.Hour)},
	); err != nil {
		t.Fatal(err)
	}

	bob, err := st.CreateProfile(ctx, "Bob", store.ProfileAdult, all)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{ID: "device-bob-scrob", Name: "Bob Phone", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("bob-sec")},
		store.Session{ID: "sess-bob-scrob", DeviceID: "device-bob-scrob", ProfileID: bob.ID, ExpiresAt: now.Add(24 * time.Hour)},
	); err != nil {
		t.Fatal(err)
	}

	runReport := func(profileID store.ProfileID, sessID store.SessionID, cmdID, grantID string) {
		t.Helper()
		payload, err := store.NewSessionControlPayload(store.SessionCommandPlayMedia, store.SessionControlArgs{MediaID: media.ID}, "device-ctrl", player.ID, media.ID)
		if err != nil {
			t.Fatal(err)
		}
		var rev int64
		rev, err = st.RendererSessionRevision(ctx, "sess-tv-scrob")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.ControlRendererSessionForPlayer(ctx, "sess-tv-scrob", rev, cmdID, payload, nil, now, store.PlayerControlGuard{
			PlayerProfileID: player.ID, CallerProfileID: profileID, CallerSessionID: sessID, BindingGeneration: 1,
		}); err != nil {
			t.Fatal(err)
		}
		scope, err := authSvc.Scope(ctx, "sess-tv-scrob")
		if err != nil {
			t.Fatal(err)
		}
		info, err := pb.Info(ctx, scope, playback.InfoRequest{
			MediaID: string(media.ID),
			CapabilityProfile: playback.CapabilityProfile{
				Client: "tvos", Containers: []string{"mp4"}, DirectCodecs: []string{"h264"},
			},
		})
		if err != nil || info.GrantID == "" {
			t.Fatalf("pb.Info = %+v, %v", info, err)
		}
		_ = grantID
		body := `{"query":"mutation R($m:String!,$g:String!,$s:Int!){ reportPlayback(mediaID:$m,positionSeconds:120,durationSeconds:120,finished:true,grantID:$g,sequence:$s){ watched } }","variables":{"m":"` + string(media.ID) + `","g":"` + info.GrantID + `","s":0}}`
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(body))
		req.Header.Set("X-Beamfall-Session", "sess-tv-scrob")
		req.Header.Set("X-Beamfall-Device", "device-tv-scrob")
		req.Header.Set("X-Beamfall-Device-Secret", "tv-sec")
		req = req.WithContext(auth.WithClientCapabilities(req.Context(), []string{string(capabilities.CapabilityPlayerDisplay)}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("reportPlayback status = %d: %s", rec.Code, rec.Body.String())
		}
	}

	// 1. Bob (no scrobble link / no consent) originates playback: no scrobble submitted!
	runReport(bob.ID, "sess-bob-scrob", "cmd_bob_no_consent_1", "")
	deadline := time.After(2 * time.Second)
	for len(dispatcher.Skips()) == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for unlinked-profile scrobble skip for Bob")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if got := doer.count(); got != 0 {
		t.Fatalf("unlinked originator Bob triggered %d scrobble calls, want 0", got)
	}

	// 2. profile-default (linked / consented) originates playback: scrobble IS submitted!
	runReport("profile-default", "session-default", "cmd_alice_consent_1", "")
	select {
	case <-doer.done:
	case <-time.After(2 * time.Second):
		t.Fatalf("consented originator did not scrobble; skips=%#v", dispatcher.Skips())
	}
	if got := doer.count(); got != 1 {
		t.Fatalf("consented originator scrobble count = %d, want 1", got)
	}
}
