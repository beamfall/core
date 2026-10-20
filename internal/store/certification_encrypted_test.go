package store

import (
	"context"
	"sort"
	"testing"
)

func TestPlaneCertificationFilterAndLockedAnnotation(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	const libraryID = LibraryID("library-local")
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE library SET plugin_id = 'photos' WHERE id = ?`, libraryID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}

	ids := map[string]MediaID{}
	for _, cert := range []string{"low", "high", "", "granted-high"} {
		in := encryptedImport(libraryID, "sha256:certification-encrypted-"+cert, "/encrypted/certification-"+cert+".mkv")
		in.Title = "Certification " + cert
		media, _, err := st.ImportMediaFile(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		ids[cert] = media.ID
		if cert == "" {
			continue
		}
		value := cert
		if cert == "granted-high" {
			value = "high"
		}
		tx, err := st.encryptedDBs[libraryID].writer.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := insertMediaFields(ctx, tx, media.ID, []MediaField{{Name: certificationField, Type: EntityFieldText, Text: value}}); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	constraint := CertificationConstraint{PluginID: "photos", Allowed: []string{"low"}, Locked: true}
	assertPlaneCertificationIDs(t, st, libraryID, ids, []CertificationConstraint{constraint}, false, nil, "", "granted-high", "high", "low")
	assertPlaneCertificationIDs(t, st, libraryID, ids, []CertificationConstraint{constraint}, true, nil, "low")
	assertPlaneCertificationIDs(t, st, libraryID, ids, []CertificationConstraint{{PluginID: "photos", Allowed: []string{"low"}, AllowUnrated: true}}, true, nil, "", "low")
	assertPlaneCertificationIDs(t, st, libraryID, ids, []CertificationConstraint{{PluginID: "foreign", Allowed: []string{"low"}}}, true, nil, "", "granted-high", "high", "low")
	assertPlaneCertificationIDs(t, st, libraryID, ids, []CertificationConstraint{constraint}, true, []MediaID{ids["granted-high"]}, "granted-high", "low")

	hide := LibraryFilter{AllLibraries: true, Certification: []CertificationConstraint{{PluginID: "photos", Allowed: []string{"low"}}}}
	lockedBrowse := LibraryFilter{AllLibraries: true, Certification: []CertificationConstraint{constraint}}
	lockedPlayback := lockedBrowse
	lockedPlayback.PlaybackGate = true
	allowUnrated := LibraryFilter{AllLibraries: true, PlaybackGate: true, Certification: []CertificationConstraint{{PluginID: "photos", Allowed: []string{"low"}, AllowUnrated: true}}}
	granted := lockedPlayback
	granted.GrantedMediaIDs = []MediaID{ids["granted-high"]}
	for _, test := range []struct {
		name   string
		id     MediaID
		filter LibraryFilter
		want   bool
	}{
		{"hide allowed", ids["low"], hide, true},
		{"hide blocked", ids["high"], hide, false},
		{"hide unrated", ids[""], hide, false},
		{"locked browse", ids["high"], lockedBrowse, true},
		{"locked playback", ids["high"], lockedPlayback, false},
		{"allowed unrated", ids[""], allowUnrated, true},
		{"granted playback", ids["granted-high"], granted, true},
	} {
		t.Run("public reads "+test.name, func(t *testing.T) {
			assertEncryptedCertificationRead(t, st, test.id, test.filter, test.want)
		})
	}
	batchIDs := []MediaID{ids["low"], ids["high"], ids[""], ids["granted-high"], "media-encrypted-unknown"}
	for _, surface := range []struct {
		name string
		read func(context.Context, []MediaID, LibraryFilter) (map[MediaID]Media, error)
	}{
		{"MediaByIDs", st.MediaByIDs},
		{"VisibleMediaBatch", st.VisibleMediaBatch},
	} {
		for _, test := range []struct {
			name   string
			filter LibraryFilter
			want   []MediaID
		}{
			{"hide", hide, []MediaID{ids["low"]}},
			{"locked playback", lockedPlayback, []MediaID{ids["low"]}},
			{"granted playback", granted, []MediaID{ids["low"], ids["granted-high"]}},
		} {
			t.Run(surface.name+" "+test.name, func(t *testing.T) {
				media, err := surface.read(ctx, batchIDs, test.filter)
				if err != nil {
					t.Fatal(err)
				}
				got := make([]MediaID, 0, len(media))
				for id := range media {
					got = append(got, id)
				}
				assertMediaIDSet(t, got, test.want...)
			})
		}
		hidden, err := surface.read(ctx, []MediaID{ids["high"]}, hide)
		if err != nil {
			t.Fatal(err)
		}
		unknown, err := surface.read(ctx, []MediaID{"media-encrypted-unknown"}, hide)
		if err != nil {
			t.Fatal(err)
		}
		if len(hidden) != 0 || len(unknown) != 0 {
			t.Fatalf("%s hidden = %v, unknown = %v; want indistinguishable empty results", surface.name, hidden, unknown)
		}
	}
	denied := assertEncryptedCertificationRead(t, st, ids["high"], hide, false)
	unknown := assertEncryptedCertificationRead(t, st, "media-encrypted-unknown", hide, false)
	for i := range denied {
		if denied[i] != unknown[i] {
			t.Fatalf("hidden error %T(%v), unknown error %T(%v)", denied[i], denied[i], unknown[i], unknown[i])
		}
	}

	entity := Entity{ID: "entity-encrypted-certification", LibraryID: libraryID, Kind: "widget", Title: "Encrypted certification"}
	if err := st.UpsertEntity(ctx, entity); err != nil {
		t.Fatal(err)
	}
	for _, id := range []MediaID{ids["low"], ids["high"], ids["granted-high"]} {
		if _, err := st.encryptedDBs[libraryID].writer.ExecContext(ctx, `
			INSERT INTO entity_relation(from_entity_id, relation, to_media_id)
			VALUES (?, 'linked', ?)
		`, entity.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []MediaID{ids["high"], ids["granted-high"]} {
		if err := st.LinkMediaRelation(ctx, id, MediaRelationEditionOf, ids["low"]); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name   string
		filter LibraryFilter
		want   []MediaID
	}{
		{"hide", hide, []MediaID{ids["low"]}},
		{"locked browse", lockedBrowse, []MediaID{ids["low"], ids["high"], ids["granted-high"]}},
		{"locked playback", lockedPlayback, []MediaID{ids["low"]}},
		{"granted playback", granted, []MediaID{ids["low"], ids["granted-high"]}},
	} {
		t.Run("EntityRelationRows "+test.name, func(t *testing.T) {
			rows, err := st.EntityRelationRows(ctx, entity.ID, "linked", test.filter)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]MediaID, 0, len(rows))
			for _, row := range rows {
				if row.ToMedia != nil {
					got = append(got, row.ToMedia.ID)
				}
			}
			assertMediaIDSet(t, got, test.want...)
		})
	}
	for _, test := range []struct {
		name   string
		filter LibraryFilter
		want   []MediaID
	}{
		{"hide", hide, nil},
		{"locked browse", lockedBrowse, []MediaID{ids["high"], ids["granted-high"]}},
		{"locked playback", lockedPlayback, nil},
		{"granted playback", granted, []MediaID{ids["granted-high"]}},
	} {
		t.Run("MediaRelationsOf "+test.name, func(t *testing.T) {
			media, err := st.MediaRelationsOf(ctx, ids["low"], MediaRelationEditionOf, test.filter)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]MediaID, len(media))
			for i := range media {
				got[i] = media[i].ID
			}
			assertMediaIDSet(t, got, test.want...)
		})
	}

	items := make([]SourceItem, 0, len(ids))
	for cert, id := range ids {
		items = append(items, SourceItem{ID: id, Kind: "media", Title: cert})
	}
	if err := st.annotateLockedLocked(ctx, []CertificationConstraint{constraint}, items, []MediaID{ids["granted-high"]}); err != nil {
		t.Fatal(err)
	}
	locked := lockedSet(items)
	if !locked[ids["high"]] || !locked[ids[""]] || locked[ids["low"]] || locked[ids["granted-high"]] {
		t.Fatalf("locked IDs = %v", locked)
	}
}

func assertEncryptedCertificationRead(t *testing.T, st *Store, id MediaID, filter LibraryFilter, want bool) [2]error {
	t.Helper()
	_, mediaErr := st.Media(context.Background(), id, filter)
	files, filesErr := st.MediaFilesForMedia(context.Background(), id, filter)
	if want {
		if mediaErr != nil || filesErr != nil {
			t.Fatalf("Media error = %v, MediaFilesForMedia error = %v", mediaErr, filesErr)
		}
		if len(files) != 1 {
			t.Fatalf("MediaFilesForMedia returned %d files, want 1", len(files))
		}
	} else if mediaErr == nil || filesErr == nil {
		t.Fatalf("Media error = %v, MediaFilesForMedia error = %v; want both denied", mediaErr, filesErr)
	}
	return [2]error{mediaErr, filesErr}
}

func assertMediaIDSet(t *testing.T, got []MediaID, want ...MediaID) {
	t.Helper()
	gotSet := make(map[MediaID]bool, len(got))
	for _, id := range got {
		gotSet[id] = true
	}
	if len(gotSet) != len(want) {
		t.Fatalf("media IDs = %v, want %v", got, want)
	}
	for _, id := range want {
		if !gotSet[id] {
			t.Fatalf("media IDs = %v, want %v", got, want)
		}
	}
}

func assertPlaneCertificationIDs(t *testing.T, st *Store, libraryID LibraryID, ids map[string]MediaID, constraints []CertificationConstraint, playbackGate bool, granted []MediaID, want ...string) {
	t.Helper()
	clause, certArgs := planeCertificationFilterSQL("photos", constraints, playbackGate, granted)
	args := []any{libraryID, ids["low"], ids["high"], ids[""], ids["granted-high"]}
	args = append(args, certArgs...)
	// #nosec G202 -- clause is generated by planeCertificationFilterSQL from closed SQL fragments.
	rows, err := st.encryptedDBs[libraryID].reader.QueryContext(context.Background(), `
		SELECT m.id FROM media m
		WHERE m.library_id = ? AND m.id IN (?, ?, ?, ?)`+clause, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []MediaID
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	wantIDs := make([]MediaID, len(want))
	for i, cert := range want {
		wantIDs[i] = ids[cert]
	}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	sort.Slice(wantIDs, func(i, j int) bool { return wantIDs[i] < wantIDs[j] })
	if len(got) != len(wantIDs) {
		t.Fatalf("IDs = %v, want %v", got, wantIDs)
	}
	for i := range got {
		if got[i] != wantIDs[i] {
			t.Fatalf("IDs = %v, want %v", got, wantIDs)
		}
	}
}
