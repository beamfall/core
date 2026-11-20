package graph

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestCrossLibrarySourceItemProvenance(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	const title = "Provenance Needle"
	var libraries []store.LibraryID
	var items []store.MediaID
	for i, name := range []string{"Permitted provenance", "Excluded provenance", "Restricted provenance"} {
		lib, err := st.CreateLibrary(ctx, name, i == 2, "", "profile-default")
		if err != nil {
			t.Fatal(err)
		}
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: lib.ID, Title: title, Path: "/" + string(lib.ID) + ".mp4",
			Container: "mp4", Codec: "h264",
			ContentFingerprint: "sha256:" + string(lib.ID),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertPlaybackProgress(ctx, store.PlaybackProgress{
			ProfileID: "profile-default", MediaID: media.ID, PositionSeconds: 45, DurationSeconds: 120,
		}, false, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
			t.Fatal(err)
		}
		libraries = append(libraries, lib.ID)
		items = append(items, media.ID)
	}

	handler := newTestHandlerWithStore(st)
	for _, surface := range []struct {
		name, query string
	}{
		{"Recently Added", `sourceItems(kind: "recently-added", home: true, limit: 3) { id title libraryID }`},
		{"Continue Watching", `sourceItems(kind: "continue-watching", home: true) { id title libraryID }`},
		{"Search", `search(query: "Provenance Needle") { id title libraryID }`},
		{"Enriched Search", `searchResult(query: "Provenance Needle", mode: keyword) { items { id title libraryID } }`},
	} {
		t.Run(surface.name, func(t *testing.T) {
			for _, session := range []struct {
				name  string
				auth  func(*http.Request)
				count int
			}{
				{"revealed", authorizeRevealed, 3},
				{"unrevealed", authorizeViewer, 2},
			} {
				t.Run(session.name, func(t *testing.T) {
					data, err := json.Marshal(map[string]string{"query": "query { " + surface.query + " }"})
					if err != nil {
						t.Fatal(err)
					}
					req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(string(data)))
					session.auth(req)
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, req)
					type row struct {
						ID        store.MediaID
						Title     string
						LibraryID store.LibraryID
					}
					var reply struct {
						Data struct {
							SourceItems  []row
							Search       []row
							SearchResult struct{ Items []row }
						}
						Errors []graphError
					}
					if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
						t.Fatal(err)
					}
					if rec.Code != http.StatusOK || len(reply.Errors) != 0 {
						t.Fatalf("query failed: %d %s", rec.Code, rec.Body.String())
					}
					if session.count == 2 && (strings.Contains(rec.Body.String(), string(items[2])) ||
						strings.Contains(rec.Body.String(), string(libraries[2]))) {
						t.Fatalf("restricted row or provenance leaked: %s", rec.Body.String())
					}

					want := make(map[store.MediaID]store.LibraryID, session.count)
					for i := range session.count {
						want[items[i]] = libraries[i]
					}
					permitted := map[store.LibraryID]bool{libraries[0]: true}
					got := make(map[store.MediaID]store.LibraryID)
					var survivors []store.MediaID
					for _, rows := range [][]row{reply.Data.SourceItems, reply.Data.Search, reply.Data.SearchResult.Items} {
						for _, item := range rows {
							if item.Title != title && surface.name == "Recently Added" {
								continue // this source also includes the store's development seed
							}
							if _, duplicate := got[item.ID]; duplicate {
								t.Fatalf("duplicate item %q", item.ID)
							}
							got[item.ID] = item.LibraryID
							if permitted[item.LibraryID] {
								survivors = append(survivors, item.ID)
							}
						}
					}
					if !maps.Equal(got, want) {
						t.Fatalf("item provenance = %v, want %v", got, want)
					}
					if len(survivors) != 1 || survivors[0] != items[0] {
						t.Fatalf("permitted items = %v, want [%s]", survivors, items[0])
					}
				})
			}
		})
	}
}
