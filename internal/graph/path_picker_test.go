package graph

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestPathPickerGraphAppStoreSuppressesDownloadRoleAndTerms(t *testing.T) {
	st := storetest.Open(t)
	openManagerAdminSession(t, st)
	handler := newTestHandlerWithStore(st)
	resp := graphPost(t, handler, authorizeManager,
		`{"query":"query($path:String!,$role:String!,$distributionClass:String!){ pathPicker(role:$role, distributionClass:$distributionClass, path:$path) { roles role purposeKey runtime entries { reasonCode } } }","variables":{"path":"/definitely-not-present-web7","role":"libraryRoot","distributionClass":"appStore"}}`)
	var decoded response
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Errors) != 0 {
		t.Fatalf("errors = %#v", decoded.Errors)
	}
	rawBytes, err := json.Marshal(decoded.Data["pathPicker"])
	if err != nil {
		t.Fatalf("marshal picker: %v", err)
	}
	raw := string(rawBytes)
	if strings.Contains(strings.ToLower(raw), "download") {
		t.Fatalf("App Store pathPicker leaked banned term: %s", raw)
	}
	var picker PathPickerInfo
	if err := json.Unmarshal(rawBytes, &picker); err != nil {
		t.Fatalf("decode picker: %v", err)
	}
	for _, role := range picker.Roles {
		if role == store.PathRoleDownloadDestination {
			t.Fatalf("roles = %#v, want downloadDestination suppressed", picker.Roles)
		}
	}
}

func TestPathPickerGraphUsesSessionDistributionClass(t *testing.T) {
	st := storetest.Open(t)
	if err := st.EnrollDevice(t.Context(),
		store.Device{ID: "device-app-manager", Name: "App Manager", Role: store.DeviceRoleManager, DistributionClass: store.DeviceDistributionAppStore, SecretHash: store.HashDeviceSecret("app-secret")},
		store.Session{ID: "session-app-manager", DeviceID: "device-app-manager", ProfileID: "profile-default"},
	); err != nil {
		t.Fatal(err)
	}
	openAdminSession(t, st, "session-app-manager")
	handler := newTestHandlerWithStore(st)
	authorizeAppManager := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-app-manager")
		req.Header.Set("X-Beamfall-Device", "device-app-manager")
		req.Header.Set("X-Beamfall-Device-Secret", "app-secret")
	}
	scopeReq, _ := http.NewRequest(http.MethodPost, "/api/graphql", nil)
	authorizeAppManager(scopeReq)
	scope, err := auth.NewService(st).ScopeFromRequest(t.Context(), scopeReq)
	if err != nil {
		t.Fatalf("ScopeFromRequest returned error: %v", err)
	}
	if scope.DistributionClass != store.DeviceDistributionAppStore {
		t.Fatalf("scope DistributionClass = %q, want appStore", scope.DistributionClass)
	}
	resp := graphPost(t, handler, authorizeAppManager,
		`{"query":"query($path:String!){ pathPicker(role:\"libraryRoot\", path:$path) { roles } }","variables":{"path":"/definitely-not-present-web7"}}`)
	var decoded response
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Errors) != 0 {
		t.Fatalf("errors = %#v", decoded.Errors)
	}
	rawBytes, err := json.Marshal(decoded.Data["pathPicker"])
	if err != nil {
		t.Fatalf("marshal picker: %v", err)
	}
	var picker PathPickerInfo
	if err := json.Unmarshal(rawBytes, &picker); err != nil {
		t.Fatalf("decode picker: %v", err)
	}
	for _, role := range picker.Roles {
		if role == store.PathRoleDownloadDestination {
			t.Fatalf("roles = %#v, want downloadDestination suppressed", picker.Roles)
		}
	}
	widen := graphPost(t, handler, authorizeAppManager,
		`{"query":"query($path:String!,$distributionClass:String!){ pathPicker(role:\"libraryRoot\", distributionClass:$distributionClass, path:$path) { roles } }","variables":{"path":"/definitely-not-present-web7","distributionClass":"direct"}}`)
	if !strings.Contains(string(widen), "cannot widen") {
		t.Fatalf("widen response = %s, want rejection", widen)
	}
}

func TestAddPathPickerRootGraphAudits(t *testing.T) {
	st := storetest.Open(t)
	openManagerAdminSession(t, st)
	handler := newTestHandlerWithStore(st)
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	resp := graphPost(t, handler, authorizeManager,
		`{"query":"mutation($path:String!){ addPathPickerRoot(path:$path){ path entryClass } }","variables":{"path":"`+root+`"}}`)
	var decoded response
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Errors) != 0 {
		t.Fatalf("errors = %#v", decoded.Errors)
	}
	var added PathPickerRootInfo
	rawBytes, err := json.Marshal(decoded.Data["addPathPickerRoot"])
	if err != nil {
		t.Fatalf("marshal root: %v", err)
	}
	if err := json.Unmarshal(rawBytes, &added); err != nil {
		t.Fatalf("decode root: %v", err)
	}
	if added.Path != canonicalRoot || added.EntryClass != "ordinary" {
		t.Fatalf("root = %#v, want %q ordinary", added, canonicalRoot)
	}
	entries, err := st.ActivityEntries(t.Context(), store.ActivityQuery{Filter: store.LibraryFilter{Revealed: true, AllLibraries: true}, Limit: 5})
	if err != nil {
		t.Fatalf("ActivityEntries: %v", err)
	}
	if len(entries) == 0 || entries[0].Action != store.ActivityPathPickerRootAdded || entries[0].TargetID != canonicalRoot {
		t.Fatalf("latest activity = %#v, want path picker root audit", entries)
	}
}

func TestPathPickerGraphRejectsBlocklistedBrowse(t *testing.T) {
	st := storetest.Open(t)
	openManagerAdminSession(t, st)
	handler := newTestHandlerWithStore(st)
	resp := graphPost(t, handler, authorizeManager,
		`{"query":"query($path:String!){ pathPicker(role:\"libraryRoot\", path:$path){ entries { path selectable reasonCode sampleNames } } }","variables":{"path":"/etc"}}`)
	var decoded response
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Errors) != 0 {
		t.Fatalf("errors = %#v", decoded.Errors)
	}
	var picker PathPickerInfo
	rawBytes, err := json.Marshal(decoded.Data["pathPicker"])
	if err != nil {
		t.Fatalf("marshal picker: %v", err)
	}
	if err := json.Unmarshal(rawBytes, &picker); err != nil {
		t.Fatalf("decode picker: %v", err)
	}
	if len(picker.Entries) != 1 || picker.Entries[0].Selectable || picker.Entries[0].ReasonCode != "blocklisted" || len(picker.Entries[0].SampleNames) != 0 {
		t.Fatalf("entries = %#v, want blocklisted without samples", picker.Entries)
	}
}

func TestPathPickerGraphTypedAndBrowsedRootMatch(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.Context(), t.TempDir(), store.Options{SeedDevelopmentData: true, AllowedRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	openManagerAdminSession(t, st)
	handler := newTestHandlerWithStore(st)
	resp := graphPost(t, handler, authorizeManager,
		`{"query":"query($path:String!){ pathPicker(role:\"libraryRoot\", path:$path){ entries { path children { path selectable } } } }","variables":{"path":"`+root+`"}}`)
	var decoded response
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Errors) != 0 {
		t.Fatalf("errors = %#v", decoded.Errors)
	}
	var picker PathPickerInfo
	rawBytes, err := json.Marshal(decoded.Data["pathPicker"])
	if err != nil {
		t.Fatalf("marshal picker: %v", err)
	}
	if err := json.Unmarshal(rawBytes, &picker); err != nil {
		t.Fatalf("decode picker: %v", err)
	}
	if len(picker.Entries) != 1 || picker.Entries[0].Path != canonicalRoot || len(picker.Entries[0].Children) != 1 || picker.Entries[0].Children[0].Path != filepath.Join(canonicalRoot, "child") {
		t.Fatalf("picker = %#v", picker)
	}
}
