package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/store"
)

func TestServerPlayerDisplayCapabilityAndWalkUpEndToEnd(t *testing.T) {
	ctx := context.Background()
	srv, st := newTestServerWithStore(t)

	player, err := st.CreateProfile(ctx, "Lounge TV", store.ProfilePlayer, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{
			ID: "device-tv-bound", Name: "Lounge Apple TV", Role: store.DeviceRoleViewer,
			Surface: store.DeviceSurfaceTVOSViewer, SecretHash: store.HashDeviceSecret("tv-bound-secret"),
			PlayerProfileID: player.ID, PlayerDisplayName: "Lounge TV", PlayerBindingGeneration: 1,
		},
		store.Session{
			ID: "session-tv-bound", DeviceID: "device-tv-bound", ProfileID: player.ID,
			ExpiresAt: time.Now().UTC().Add(time.Hour),
		},
	); err != nil {
		t.Fatal(err)
	}

	catalog := compatTestCatalog()
	catalog.EnrollmentBindings = append(catalog.EnrollmentBindings, capabilities.EnrollmentBuildBinding{
		EnrollmentID: "session:session-tv-bound",
		Surface:      capabilities.SurfaceTVOSViewer,
		BuildID:      compatTestBuildID("tvos-viewer"),
	})
	policy := srv.compat.snapshot()
	policy.Builds = catalog
	srv.SetCompatPolicy(policy, srv.buildSHA, 0)

	authTV := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-tv-bound")
		req.Header.Set("X-Beamfall-Device", "device-tv-bound")
		req.Header.Set("X-Beamfall-Device-Secret", "tv-bound-secret")
	}

	// 1. Handshake WITHOUT player-display -> compatible, but playerBinding reports updateRequired=true
	// and browse/manifest/playback are refused.
	noCapContract := compatTestContract("tvos-viewer")
	resp := compatHandshakeRecord(t, srv, noCapContract, authTV)
	if resp.Decision != "compatible" {
		t.Fatalf("handshake without player-display = %+v", resp)
	}

	pbReq := newJSONRequest(http.MethodPost, "/api/graphql", strings.NewReader(`{"query":"query { playerBinding { bound displayName generation updateRequired } }"}`))
	authTV(pbReq)
	pbRec := httptest.NewRecorder()
	srv.ServeHTTP(pbRec, pbReq)
	if pbRec.Code != http.StatusOK {
		t.Fatalf("playerBinding status = %d: %s", pbRec.Code, pbRec.Body.String())
	}
	var pbBody struct {
		Data struct {
			PlayerBinding struct {
				Bound          bool   `json:"bound"`
				DisplayName    string `json:"displayName"`
				Generation     int    `json:"generation"`
				UpdateRequired bool   `json:"updateRequired"`
			} `json:"playerBinding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(pbRec.Body.Bytes(), &pbBody); err != nil {
		t.Fatal(err)
	}
	if !pbBody.Data.PlayerBinding.Bound || pbBody.Data.PlayerBinding.DisplayName != "Lounge TV" || pbBody.Data.PlayerBinding.Generation != 1 || !pbBody.Data.PlayerBinding.UpdateRequired {
		t.Fatalf("playerBinding without cap = %+v", pbBody.Data.PlayerBinding)
	}

	manifestReq := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	authTV(manifestReq)
	manifestRec := httptest.NewRecorder()
	srv.ServeHTTP(manifestRec, manifestReq)
	if manifestRec.Code != http.StatusForbidden {
		t.Fatalf("manifest without player-display = %d, want 403", manifestRec.Code)
	}

	// 2. Handshake WITH player-display -> updateRequired=false; walk-up off allows only commanded media.
	withCapContract := compatTestContract("tvos-viewer")
	withCapContract.RequestedCapabilities = []string{string(capabilities.CapabilityPlayerDisplay)}
	resp = compatHandshakeRecord(t, srv, withCapContract, authTV)
	if resp.Decision != "compatible" {
		t.Fatalf("handshake with player-display = %+v", resp)
	}

	pbReq2 := newJSONRequest(http.MethodPost, "/api/graphql", strings.NewReader(`{"query":"query { playerBinding { updateRequired } }"}`))
	authTV(pbReq2)
	pbRec2 := httptest.NewRecorder()
	srv.ServeHTTP(pbRec2, pbReq2)
	if err := json.Unmarshal(pbRec2.Body.Bytes(), &pbBody); err != nil {
		t.Fatal(err)
	}
	if pbBody.Data.PlayerBinding.UpdateRequired {
		t.Fatal("playerBinding with player-display reported updateRequired=true")
	}

	// Walk-up off refuses /api/manifest and uncommanded /api/playback-info.
	manifestRec = httptest.NewRecorder()
	srv.ServeHTTP(manifestRec, manifestReq)
	if manifestRec.Code != http.StatusForbidden {
		t.Fatalf("walk-up off manifest = %d, want 403", manifestRec.Code)
	}
	playBody := `{"mediaID":"media-alpha","capabilityProfile":{"client":"tvos","containers":["mp4"],"directCodecs":["h264"]}}`
	playReq := newJSONRequest(http.MethodPost, "/api/playback-info", strings.NewReader(playBody))
	authTV(playReq)
	playRec := httptest.NewRecorder()
	srv.ServeHTTP(playRec, playReq)
	if playRec.Code != http.StatusForbidden {
		t.Fatalf("walk-up off uncommanded playback-info = %d, want 403", playRec.Code)
	}

	// Command media-alpha -> /api/playback-info succeeds.
	if err := st.AppendSessionControlEvent(ctx, "session-tv-bound", store.SessionControlPayload{
		Command: store.SessionCommandPlayMedia, MediaID: "media-alpha", FromDevice: "device-mac", ProfileID: player.ID,
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	playReq2 := newJSONRequest(http.MethodPost, "/api/playback-info", strings.NewReader(playBody))
	authTV(playReq2)
	playRec2 := httptest.NewRecorder()
	srv.ServeHTTP(playRec2, playReq2)
	if playRec2.Code != http.StatusOK {
		t.Fatalf("walk-up off commanded playback-info = %d: %s", playRec2.Code, playRec2.Body.String())
	}
}

// ADR-0240: a Device bound as a player display acts as a viewer even when its
// stored role is manager, on REST and GraphQL alike.
func TestServerPlayerBoundManagerDeviceResolvesAsViewer(t *testing.T) {
	ctx := context.Background()
	srv, st := newTestServerWithStore(t)

	player, err := st.CreateProfile(ctx, "Den TV", store.ProfilePlayer, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{
			ID: "device-mgr-bound", Name: "Den Mac", Role: store.DeviceRoleManager,
			SecretHash:      store.HashDeviceSecret("mgr-bound-secret"),
			PlayerProfileID: player.ID, PlayerDisplayName: "Den TV", PlayerBindingGeneration: 1,
		},
		store.Session{
			ID: "session-mgr-bound", DeviceID: "device-mgr-bound", ProfileID: player.ID,
			ExpiresAt: time.Now().UTC().Add(time.Hour),
		},
	); err != nil {
		t.Fatal(err)
	}
	authBound := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-mgr-bound")
		req.Header.Set("X-Beamfall-Device", "device-mgr-bound")
		req.Header.Set("X-Beamfall-Device-Secret", "mgr-bound-secret")
	}

	scope, err := srv.auth.ScopeFromRequest(ctx, func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		authBound(r)
		return r
	}())
	if err != nil {
		t.Fatal(err)
	}
	if scope.DeviceRole != store.DeviceRoleViewer || scope.Admin {
		t.Fatalf("bound scope role=%q admin=%v, want viewer and no admin", scope.DeviceRole, scope.Admin)
	}

	exportReq := httptest.NewRequest(http.MethodGet, subscriptionExportPath, nil)
	authBound(exportReq)
	exportRec := httptest.NewRecorder()
	srv.ServeHTTP(exportRec, exportReq)
	if exportRec.Code != http.StatusForbidden {
		t.Fatalf("bound subscription export = %d, want 403: %s", exportRec.Code, exportRec.Body.String())
	}

	gqlReq := newJSONRequest(http.MethodPost, "/api/graphql", strings.NewReader(`{"query":"mutation { trashMedia(id: \"media-alpha\") { id } }"}`))
	authBound(gqlReq)
	gqlRec := httptest.NewRecorder()
	srv.ServeHTTP(gqlRec, gqlReq)
	if !strings.Contains(gqlRec.Body.String(), "errors") {
		t.Fatalf("bound trashMedia was not refused: %s", gqlRec.Body.String())
	}

	stored, err := st.Device(ctx, "device-mgr-bound")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Role != store.DeviceRoleManager {
		t.Fatalf("stored role = %q, want manager kept for unbinding", stored.Role)
	}
}
