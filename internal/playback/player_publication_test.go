package playback

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

func TestPlayerPlaybackPublicationRechecksCommand(t *testing.T) {
	for _, name := range []string{"schedule closed", "library removed", "image stopped"} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			st := storetest.Open(t)
			now := time.Now()
			now = time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
			clock := func() time.Time { return now }
			authService := auth.NewServiceWithClock(st, clock)
			tokens := streamtoken.NewService([]byte("player-publication-test"), clock)
			svc := NewService(st, tokens, clock)
			svc.SetAuth(authService)
			player, err := st.CreateProfile(ctx, "Display", store.ProfilePlayer, store.LibraryFilter{AllLibraries: true})
			if err != nil {
				t.Fatal(err)
			}
			const display, device = "session-display", "device-display"
			if err := st.EnrollDevice(ctx, store.Device{
				ID: device, Name: "Display", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("display-test"),
				PlayerProfileID: player.ID, PlayerDisplayName: "Display", PlayerBindingGeneration: 1,
			}, store.Session{ID: display, DeviceID: device, ProfileID: player.ID, ExpiresAt: now.Add(24 * time.Hour)}); err != nil {
				t.Fatal(err)
			}
			const originator = "profile-managed"
			otherLibrary, err := st.CreateLibrary(ctx, "Other library", false, "", "profile-default")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.SetPlayerControllers(ctx, player.ID, false, []store.ProfileID{originator}, nil); err != nil {
				t.Fatal(err)
			}
			if err := st.SetProfileAccessWindows(ctx, originator, []store.ProfileAccessWindow{
				{Weekday: int(now.Weekday()), StartMinute: 720, EndMinute: 721},
			}); err != nil {
				t.Fatal(err)
			}
			var mediaID store.MediaID
			if name == "image stopped" {
				src := filepath.Join(t.TempDir(), "still.jpg")
				seedScratchFile(t, src)
				media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
					LibraryID: "library-local", Path: src, Title: "Still", Class: store.MediaClassImage,
					Container: "jpeg", ContentFingerprint: "publication-still",
				})
				if err != nil {
					t.Fatal(err)
				}
				mediaID = media.ID
			} else {
				mediaID, _ = importDrainMedia(t, st, "library-local", store.MediaClassVideo, 120)
			}
			guard := store.PlayerControlGuard{
				PlayerProfileID: player.ID, CallerProfileID: originator, CallerSessionID: "session-managed", BindingGeneration: 1,
			}
			command := func(kind string, id string, revision int64, target store.MediaID) {
				t.Helper()
				payload, err := store.NewSessionControlPayload(kind, store.SessionControlArgs{MediaID: target}, "device-tablet", player.ID, target)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := st.ControlRendererSessionForPlayer(ctx, display, revision, id, payload, nil, now, guard); err != nil {
					t.Fatal(err)
				}
			}
			command(store.SessionCommandPlayMedia, "cmd_initial_playback", 0, mediaID)
			scope, err := authService.Scope(ctx, display)
			if err != nil {
				t.Fatal(err)
			}
			req := InfoRequest{MediaID: string(mediaID), CapabilityProfile: CapabilityProfile{
				Client: "test", Containers: []string{"mp4"}, DirectCodecs: []string{"h264", "aac"},
			}}
			before, err := svc.Info(ctx, scope, req)
			if err != nil || before.Decision == DecisionUnavailable || before.GrantID == "" || len(before.Streams) == 0 {
				t.Fatalf("initial decision = %+v, %v", before, err)
			}
			if name == "image stopped" {
				signed, err := tokens.UnverifiedGrant(before.Streams[0].Token, streamtoken.TargetMediaDirect)
				if err != nil || signed.GrantID != before.GrantID {
					t.Fatalf("image token grant ID = %q, %v, want %q", signed.GrantID, err, before.GrantID)
				}
				report, err := st.RecordPlayerPlaybackReport(ctx, display, device, player.ID, 1, store.PlaybackProgress{
					MediaID: mediaID, GrantID: before.GrantID,
				}, true, store.ResumePolicy{Class: store.MediaClassImage}, now)
				if err != nil || report.Accepted || report.Ended {
					t.Fatalf("image report = %+v, %v, want no playback report", report, err)
				}
			}

			injected := false
			svc.decisionLookup = &testDecisionLookup{decisionLookup: st, onRestricted: func(store.LibraryID) {
				if injected {
					return
				}
				injected = true
				switch name {
				case "schedule closed":
					now = now.Add(time.Minute)
				case "library removed":
					if _, err := st.SetProfileLibraries(ctx, originator, []store.LibraryID{otherLibrary.ID}); err != nil {
						t.Fatal(err)
					}
				case "image stopped":
					command(store.SessionCommandStop, "cmd_stop_during_info", 1, "")
				}
			}}
			records := 0
			tokens.SetRecorder(drainRecorder(func(store.MediaID) { records++ }))
			info, err := svc.Info(ctx, scope, req)
			if !injected {
				t.Fatal("decision never reached the negotiation hook")
			}
			if err != nil || info.Decision != DecisionUnavailable || info.GrantID != "" || len(info.Streams) != 0 || records != 0 {
				t.Fatalf("decision after %s = %+v, err %v, recorded %d tokens", name, info, err, records)
			}
			if name != "image stopped" {
				if prior, found, err := st.ActivePlaybackGrantForSession(ctx, display, mediaID, now); err != nil || !found || prior.GrantID != before.GrantID {
					t.Fatalf("denied grant changed the prior grant: %q, %t, %v", prior.GrantID, found, err)
				}
				if _, pending, err := st.ActivePlayerCommandedAttribution(ctx, display, mediaID, now); err != nil || pending {
					t.Fatalf("denied command still pending = %t, %v", pending, err)
				}
			}
		})
	}
}
