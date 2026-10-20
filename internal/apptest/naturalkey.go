package apptest

import (
	"encoding/json"
	"testing"
)

// profileSummary is the JSON shape returned by the `profiles` GraphQL field.
type profileSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type profilesData struct {
	Data struct {
		Profiles []profileSummary `json:"profiles"`
	} `json:"data"`
}

// ProfileByName returns the profile whose Name equals name, failing the test
// if none is found. It queries via GraphQL so no direct store access is needed.
// Natural-key variant of a raw profile-ID lookup: keeps tests readable when
// the test author knows the name but not the generated ID.
func (a *Assert) ProfileByName(client *Client, name string) profileSummary {
	a.t.Helper()
	resp := client.GraphQL(`{ profiles { id name kind } }`)
	a.OK(resp)

	var body profilesData
	if err := json.Unmarshal([]byte(resp.Body()), &body); err != nil {
		a.t.Fatalf("apptest: decode profiles response: %v\n%s", err, resp.Body())
	}
	for _, p := range body.Data.Profiles {
		if p.Name == name {
			return p
		}
	}
	a.t.Fatalf("apptest: no profile with name %q in %v", name, body.Data.Profiles)
	return profileSummary{} // unreachable
}

// HasProfileNamed fails unless at least one profile with the given name exists.
func (a *Assert) HasProfileNamed(client *Client, name string) {
	a.t.Helper()
	a.ProfileByName(client, name) // fails loudly if absent
}

// HasProfileKind fails unless the profile named name has the given kind
// (e.g. "adult" or "managed").
func (a *Assert) HasProfileKind(client *Client, name, kind string) {
	a.t.Helper()
	p := a.ProfileByName(client, name)
	if p.Kind != kind {
		a.t.Fatalf("apptest: profile %q kind = %q, want %q", name, p.Kind, kind)
	}
}

// surfaceInfo captures a registered reveal surface.
type surfaceInfo struct {
	ID    string
	Class string
}

// RevealSurfaces fetches the registered reveal surfaces. It uses App.Surfaces()
// directly (no network round-trip needed) and returns them as a slice.
func (h *Harness) RevealSurfaces() []surfaceInfo {
	all := h.App.Surfaces().All()
	out := make([]surfaceInfo, len(all))
	for i, s := range all {
		out[i] = surfaceInfo{ID: s.ID, Class: string(s.Class)}
	}
	return out
}

// HasRevealSurface fails unless a surface with the given ID is registered in
// the app's reveal registry. This is the natural-key assertion: test authors
// refer to a surface by its human-readable resolver ID (e.g.
// "Query.navigationManifest") rather than an opaque internal index.
func (a *Assert) HasRevealSurface(h *Harness, surfaceID string) {
	a.t.Helper()
	for _, s := range h.RevealSurfaces() {
		if s.ID == surfaceID {
			return
		}
	}
	a.t.Fatalf("apptest: no reveal surface with ID %q", surfaceID)
}

// HasRevealSurfaceClass fails unless a surface with the given ID is registered
// with the given class.
func (a *Assert) HasRevealSurfaceClass(h *Harness, surfaceID, class string) {
	a.t.Helper()
	for _, s := range h.RevealSurfaces() {
		if s.ID == surfaceID {
			if s.Class != class {
				a.t.Fatalf("apptest: surface %q class = %q, want %q", surfaceID, s.Class, class)
			}
			return
		}
	}
	a.t.Fatalf("apptest: no reveal surface with ID %q", surfaceID)
}

// SourceItemByTitle finds a source item with the given title in a JSON slice
// (the raw body of a sourceItems response) and returns the item's id. It fails
// the test if no item matches, making source-item assertions title-keyed rather
// than id-keyed.
func SourceItemByTitle(t testing.TB, body string, title string) string {
	t.Helper()
	var wrapper struct {
		Data struct {
			SourceItems []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"sourceItems"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &wrapper); err != nil {
		t.Fatalf("apptest: decode sourceItems body: %v\n%s", err, body)
	}
	for _, item := range wrapper.Data.SourceItems {
		if item.Title == title {
			return item.ID
		}
	}
	t.Fatalf("apptest: no sourceItem with title %q", title)
	return "" // unreachable
}
