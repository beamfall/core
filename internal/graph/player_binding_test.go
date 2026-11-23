package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func authorizeBoundDevice(deviceID store.DeviceID, sessionID store.SessionID, secret string, withPlayerDisplay bool) func(*http.Request) {
	return func(req *http.Request) {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Beamfall-Session", string(sessionID))
		req.Header.Set("X-Beamfall-Device", string(deviceID))
		req.Header.Set("X-Beamfall-Device-Secret", secret)
		if withPlayerDisplay {
			*req = *req.WithContext(auth.WithClientCapabilities(req.Context(), []string{string(capabilities.CapabilityPlayerDisplay)}))
		}
	}
}

func TestPlayerProfileGraphPoliciesAndBinding(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	// Owner creates a player Profile; restricted libraries are never granted.
	data, errs := post(t, h,
		`{"query":"mutation C($name: String!, $kind: String!){ createProfile(name: $name, kind: $kind){ id name kind libraryIDs walkUpEnabled } }","variables":{"name":"Karaoke Room","kind":"player"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("createProfile(player) errors = %#v", errs)
	}
	var created ProfileInfo
	mustUnmarshal(t, data["createProfile"], &created)
	if created.Kind != "player" || created.WalkUpEnabled {
		t.Fatalf("created = %+v, want kind=player walkUpEnabled=false", created)
	}
	for _, libID := range created.LibraryIDs {
		if libID == "library-archive" {
			t.Fatal("createProfile(player) leaked restricted library-archive")
		}
	}

	// Kind is fixed in updateProfile.
	_, errs = post(t, h,
		`{"query":"mutation U($id: String!, $kind: String!){ updateProfile(profileID: $id, kind: $kind){ id } }","variables":{"id":"`+created.ID+`","kind":"adult"}}`,
		authorizeManager)
	if len(errs) == 0 {
		t.Fatal("updateProfile(player -> adult) succeeded")
	}
	_, errs = post(t, h,
		`{"query":"mutation U($id: String!, $kind: String!){ updateProfile(profileID: $id, kind: $kind){ id } }","variables":{"id":"profile-managed","kind":"player"}}`,
		authorizeManager)
	if len(errs) == 0 {
		t.Fatal("updateProfile(managed -> player) succeeded")
	}

	// Picker listing excludes player profiles.
	data, errs = post(t, h, `{"query":"query { profiles { id kind } }"}`, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("profiles errors = %#v", errs)
	}
	var picker []ProfileSummary
	mustUnmarshal(t, data["profiles"], &picker)
	for _, p := range picker {
		if p.ID == created.ID || p.Kind == "player" {
			t.Fatalf("picker included player profile: %+v", p)
		}
	}

	// switchProfile into a player profile refuses with the exact unknown-profile message.
	_, playerSwitchErrs := post(t, h,
		`{"query":"mutation S($id: String!){ switchProfile(profileID: $id){ id } }","variables":{"id":"`+created.ID+`"}}`,
		authorizeViewer)
	_, unknownSwitchErrs := post(t, h,
		`{"query":"mutation S($id: String!){ switchProfile(profileID: $id){ id } }","variables":{"id":"profile-does-not-exist"}}`,
		authorizeViewer)
	if len(playerSwitchErrs) == 0 || len(unknownSwitchErrs) == 0 || playerSwitchErrs[0].Message != unknownSwitchErrs[0].Message {
		t.Fatalf("switchProfile(player) = %#v, switchProfile(unknown) = %#v; want identical message", playerSwitchErrs, unknownSwitchErrs)
	}

	// Enroll a viewer device, then bind it via setDevicePlayerBinding (owner-only).
	if err := st.EnrollDevice(ctx,
		store.Device{ID: "device-display", Name: "Living Room TV", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("display-secret")},
		store.Session{ID: "session-display-1", DeviceID: "device-display", ProfileID: "profile-default", ExpiresAt: time.Now().UTC().Add(time.Hour)},
	); err != nil {
		t.Fatal(err)
	}

	// Non-admin cannot setDevicePlayerBinding, clearDevicePlayerBinding, or setPlayerWalkUp.
	_, errs = post(t, h,
		`{"query":"mutation B($d: String!, $p: String!){ setDevicePlayerBinding(deviceID: $d, profileID: $p){ deviceID } }","variables":{"d":"device-display","p":"`+created.ID+`"}}`,
		authorizeViewer)
	if len(errs) == 0 || errs[0].Extensions.Code != ErrorCodeForbidden {
		t.Fatalf("non-admin setDevicePlayerBinding = %#v, want FORBIDDEN", errs)
	}

	data, errs = post(t, h,
		`{"query":"mutation B($d: String!, $p: String!, $n: String){ setDevicePlayerBinding(deviceID: $d, profileID: $p, displayName: $n){ deviceID playerProfileID playerDisplayName playerBindingGeneration } }","variables":{"d":"device-display","p":"`+created.ID+`","n":"Karaoke TV"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setDevicePlayerBinding errors = %#v", errs)
	}
	var boundInfo DeviceInfo
	mustUnmarshal(t, data["setDevicePlayerBinding"], &boundInfo)
	if boundInfo.PlayerProfileID != created.ID || boundInfo.PlayerDisplayName != "Karaoke TV" || boundInfo.PlayerBindingGeneration != 1 {
		t.Fatalf("boundInfo = %+v", boundInfo)
	}

	// Old session ended immediately by binding.
	endedReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(`{"query":"query { playerBinding { bound } }"}`)))
	authorizeBoundDevice("device-display", "session-display-1", "display-secret", true)(endedReq)
	endedRec := httptest.NewRecorder()
	h.ServeHTTP(endedRec, endedReq)
	var endedBind struct {
		Errors []graphError `json:"errors"`
	}
	_ = json.Unmarshal(endedRec.Body.Bytes(), &endedBind)
	if endedRec.Code != http.StatusUnauthorized || len(endedBind.Errors) == 0 || endedBind.Errors[0].Extensions.Code != ErrorCodeUnauthenticated {
		t.Fatalf("old session after bind = %d %s, want 401 UNAUTHENTICATED", endedRec.Code, endedRec.Body.String())
	}

	// Renew session for the bound device -> issues for the bound player profile.
	renewed, err := st.RenewDeviceSession(ctx, "device-display", "session-display-2", time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if renewed.ProfileID != store.ProfileID(created.ID) {
		t.Fatalf("renewed.ProfileID = %q, want %q", renewed.ProfileID, created.ID)
	}

	// Without player-display capability: only playerBinding allowed (updateRequired=true);
	// browse/search/layout/media/control are refused with FORBIDDEN / UPDATE_REQUIRED.
	withoutCap := authorizeBoundDevice("device-display", renewed.ID, "display-secret", false)
	data, errs = post(t, h, `{"query":"query { playerBinding { bound profileID displayName generation walkUpEnabled updateRequired } }"}`, withoutCap)
	if len(errs) != 0 {
		t.Fatalf("playerBinding without cap errors = %#v", errs)
	}
	var pb PlayerBindingInfo
	mustUnmarshal(t, data["playerBinding"], &pb)
	if !pb.Bound || pb.ProfileID != created.ID || pb.DisplayName != "Karaoke TV" || pb.Generation != 1 || pb.WalkUpEnabled || !pb.UpdateRequired {
		t.Fatalf("playerBinding without cap = %+v", pb)
	}
	for _, q := range []string{
		`{"query":"query { sourceItems(source: { kind: \"recently-added\" }) { id } }"}`,
		`{"query":"query { search(query: \"Alpha\") { id } }"}`,
		`{"query":"query { homeLayout { customized } }"}`,
		`{"query":"query { media(id: \"media-alpha\") { id } }"}`,
		`{"query":"mutation { controlSession(sessionID: \"session-default\", command: \"pause\") { command } }"}`,
	} {
		_, errs = post(t, h, q, withoutCap)
		if len(errs) == 0 || errs[0].Extensions.Code != ErrorCodeForbidden || errs[0].Extensions.Reason != ErrorReasonUpdateRequired {
			t.Fatalf("query %s without player-display = %#v, want FORBIDDEN/UPDATE_REQUIRED", q, errs)
		}
	}

	// With player-display capability and walk-up OFF (default):
	withCap := authorizeBoundDevice("device-display", renewed.ID, "display-secret", true)
	data, errs = post(t, h, `{"query":"query { playerBinding { bound updateRequired walkUpEnabled } }"}`, withCap)
	if len(errs) != 0 {
		t.Fatalf("playerBinding with cap errors = %#v", errs)
	}
	mustUnmarshal(t, data["playerBinding"], &pb)
	if pb.UpdateRequired || pb.WalkUpEnabled {
		t.Fatalf("playerBinding with cap = %+v", pb)
	}

	// Walk-up off refuses browse, search, layout, picker, switchProfile, and uncommanded Media.
	for _, q := range []string{
		`{"query":"query { sourceItems(source: { kind: \"recently-added\" }) { id } }"}`,
		`{"query":"query { search(query: \"Alpha\") { id } }"}`,
		`{"query":"query { homeLayout { customized } }"}`,
		`{"query":"query { navigationManifest { layouts { id } } }"}`,
		`{"query":"query { media(id: \"media-alpha\") { id title } }"}`,
		`{"query":"query { profiles { id } }"}`,
		`{"query":"mutation { switchProfile(profileID: \"profile-default\") { id } }"}`,
	} {
		_, errs = post(t, h, q, withCap)
		if len(errs) == 0 || errs[0].Extensions.Code != ErrorCodeForbidden {
			t.Fatalf("walk-up off query %s = %#v, want FORBIDDEN", q, errs)
		}
	}

	// Command media-alpha via play-media session control event -> media read allowed!
	if err := st.AppendSessionControlEvent(ctx, renewed.ID, store.SessionControlPayload{
		Command: store.SessionCommandPlayMedia, MediaID: "media-alpha", FromDevice: "device-mac", ProfileID: store.ProfileID(created.ID),
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	data, errs = post(t, h, `{"query":"query { media(id: \"media-alpha\") { id title } }"}`, withCap)
	if len(errs) != 0 {
		t.Fatalf("commanded media read errors = %#v", errs)
	}
	var media MediaInfo
	mustUnmarshal(t, data["media"], &media)
	if media.ID != "media-alpha" {
		t.Fatalf("commanded media = %+v", media)
	}

	// Enable walk-up via setPlayerWalkUp -> browse, search, layout allowed; reveal/picker/PIN/settings still refused!
	data, errs = post(t, h,
		`{"query":"mutation W($p: String!, $e: Boolean!){ setPlayerWalkUp(profileID: $p, enabled: $e){ id walkUpEnabled } }","variables":{"p":"`+created.ID+`","e":true}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setPlayerWalkUp errors = %#v", errs)
	}
	var updatedProf ProfileInfo
	mustUnmarshal(t, data["setPlayerWalkUp"], &updatedProf)
	if !updatedProf.WalkUpEnabled {
		t.Fatal("setPlayerWalkUp did not set walkUpEnabled=true")
	}

	data, errs = post(t, h, `{"query":"query { sourceItems(source: { kind: \"recently-added\" }) { id } }"}`, withCap)
	if len(errs) != 0 {
		t.Fatalf("walk-up on sourceItems errors = %#v", errs)
	}
	var items []SourceItem
	mustUnmarshal(t, data["sourceItems"], &items)
	for _, it := range items {
		if it.LibraryID == "library-archive" {
			t.Fatal("walk-up on leaked restricted library-archive")
		}
	}
	if _, errs = post(t, h, `{"query":"query { search(query: \"Alpha\") { id } }"}`, withCap); len(errs) != 0 {
		t.Fatalf("walk-up on search errors = %#v", errs)
	}
	if _, errs = post(t, h, `{"query":"query { homeLayout { customized } }"}`, withCap); len(errs) != 0 {
		t.Fatalf("walk-up on homeLayout errors = %#v", errs)
	}
	for _, forbidden := range []string{
		`{"query":"mutation { reveal(pin: \"1234\") { revealed } }"}`,
		`{"query":"query { profiles { id } }"}`,
		`{"query":"mutation { switchProfile(profileID: \"profile-default\") { id } }"}`,
		`{"query":"mutation { setClientPref(key: \"k\", value: \"v\") { key } }"}`,
		`{"query":"mutation { updateProfile(profileID: \"` + created.ID + `\", subtitleMode: \"off\") { id } }"}`,
	} {
		_, errs = post(t, h, forbidden, withCap)
		if len(errs) == 0 || errs[0].Extensions.Code != ErrorCodeForbidden {
			t.Fatalf("walk-up on %s = %#v, want FORBIDDEN", forbidden, errs)
		}
	}

	// clearDevicePlayerBinding clears binding, increments generation, and ends session.
	data, errs = post(t, h,
		`{"query":"mutation C($d: String!){ clearDevicePlayerBinding(deviceID: $d){ deviceID playerProfileID playerBindingGeneration } }","variables":{"d":"device-display"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("clearDevicePlayerBinding errors = %#v", errs)
	}
	var clearedInfo DeviceInfo
	mustUnmarshal(t, data["clearDevicePlayerBinding"], &clearedInfo)
	if clearedInfo.PlayerProfileID != "" || clearedInfo.PlayerBindingGeneration != 2 {
		t.Fatalf("clearedInfo = %+v", clearedInfo)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(`{"query":"query { playerBinding { bound } }"}`)))
	withCap(req)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var ended struct {
		Errors []graphError `json:"errors"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ended)
	if len(ended.Errors) == 0 || ended.Errors[0].Extensions.Code != ErrorCodeUnauthenticated {
		t.Fatalf("cleared session response = %s", rec.Body.String())
	}
}
