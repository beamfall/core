package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestApplyLocalExtractionRollsBackAtomicallyOnMidTxFailure(t *testing.T) {
	ctx := context.Background()
	dir := migratedDataDir(t)
	st, err := Open(ctx, dir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	lib, err := st.CreateLibraryWithRoots(ctx, "Photos", false, "", "profile-default", []string{root}, "")
	if err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	const fp = "sha256:cliprollback1"
	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:           lib.ID,
		Path:                filepath.Join(root, "clip.mov"),
		ContentFingerprint:  fp,
		Title:               "clip",
		Class:               MediaClassVideo,
		DurationSecondsHint: 8,
		Container:           "mov",
		Codec:               "hevc",
	})
	if err != nil {
		_ = st.Close()
		t.Fatal(err)
	}

	injectedErr := errors.New("simulated crash between field and entity writes")
	st.testLocalExtractAfterFieldsHook = func() error { return injectedErr }

	captured := time.Date(2025, 9, 10, 12, 0, 0, 0, time.UTC)
	input := LocalExtractionInput{
		LibraryID:          lib.ID,
		MediaID:            media.ID,
		ContentFingerprint: fp,
		FieldBatches: [][]MediaField{
			{{Name: "capture-date", Type: EntityFieldDate, Date: captured}},
		},
		Entities: []LocalExtractionEntity{
			{
				Entity: Entity{
					ID:        "entity-album-vacation",
					LibraryID: lib.ID,
					Kind:      "album",
					Title:     "Vacation",
				},
				Relation: EntityRelation{
					FromEntityID: "entity-album-vacation",
					Name:         "contains",
					ToMediaID:    media.ID,
				},
			},
		},
		MarkComplete: true,
		AttemptField: "capture-date",
	}
	if err := st.ApplyLocalExtraction(ctx, input); !errors.Is(err, injectedErr) {
		_ = st.Close()
		t.Fatalf("ApplyLocalExtraction err = %v, want injectedErr", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	fields, err := reopened.MediaFields(ctx, media.ID, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 0 {
		t.Fatalf("fields after rollback = %+v, want empty", fields)
	}
	entities, err := reopened.EntitiesForMedia(ctx, media.ID, "", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 0 {
		t.Fatalf("entities after rollback = %+v, want empty", entities)
	}
	if prov, err := reopened.MediaFieldProvenance(ctx, media.ID, "capture-date"); err != nil || prov != "" {
		t.Fatalf("capture-date provenance after rollback = (%q, %v), want empty", prov, err)
	}
	missing, err := reopened.LibraryFilePathsWithoutLocalExtraction(ctx, lib.ID, MediaClassVideo)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := missing[filepath.Join(root, "clip.mov")]; !ok {
		t.Fatalf("backfill set %v missing rolled-back clip", missing)
	}

	// Retry without fault persists everything atomically and removes from backfill.
	if err := reopened.ApplyLocalExtraction(ctx, input); err != nil {
		t.Fatalf("retry ApplyLocalExtraction: %v", err)
	}
	missing, err = reopened.LibraryFilePathsWithoutLocalExtraction(ctx, lib.ID, MediaClassVideo)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("backfill set after clean retry = %v, want empty", missing)
	}
	fields, err = reopened.MediaFields(ctx, media.ID, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || !fields[0].Date.Equal(captured) {
		t.Fatalf("fields after retry = %+v, want %v", fields, captured)
	}
	entities, err = reopened.EntitiesForMedia(ctx, media.ID, "", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 1 || entities[0].Entity.ID != "entity-album-vacation" {
		t.Fatalf("entities after retry = %+v, want Vacation entity", entities)
	}
}

func TestReconcileStandaloneLibraryMediaSplitsPartsAndVersionsPreservingLinks(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	root := t.TempDir()
	lib, err := st.CreateLibraryWithRoots(ctx, "Camera", false, "", "profile-default", []string{root}, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	// Seed a two-part Media row and a two-version Media row.
	pt1Path := filepath.Join(root, "roll-pt1.mov")
	pt2Path := filepath.Join(root, "roll-pt2.mov")
	partMedia, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib.ID, Path: pt1Path, ContentFingerprint: "sha256:part1",
		Title: "Roll", Class: MediaClassVideo, DurationSecondsHint: 25, Container: "mov", Codec: "hevc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO media_file(id, content_fingerprint, media_id, path, version_label, part_ordinal, last_seen_at_ms)
		VALUES ('sha256:part2', 'sha256:part2', ?, ?, '', 1, ?)
	`, partMedia.ID, pt2Path, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}

	v1Path := filepath.Join(root, "trip-1080p.mov")
	v2Path := filepath.Join(root, "trip-720p.mov")
	verMedia, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib.ID, Path: v1Path, ContentFingerprint: "sha256:ver1",
		Title: "Trip", Class: MediaClassVideo, DurationSecondsHint: 14, Container: "mov", Codec: "hevc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		UPDATE media_file SET version_label = '1080p' WHERE media_id = ?
	`, verMedia.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO media_file(id, content_fingerprint, media_id, path, version_label, part_ordinal, last_seen_at_ms)
		VALUES ('sha256:ver2', 'sha256:ver2', ?, ?, '720p', 0, ?)
	`, verMedia.ID, v2Path, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}

	// Attach curated collection, tag, entity relation, and certification to both grouped items.
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE library SET plugin_id = 'photos' WHERE id = ?`, lib.ID); err != nil {
		t.Fatal(err)
	}
	col, err := st.CreateCollection(ctx, Collection{LibraryID: lib.ID, Name: "Favorites"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddCollectionMedia(ctx, col.ID, []MediaID{partMedia.ID, verMedia.ID}, LibraryFilter{AllLibraries: true}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO tag(id, name, created_at_ms, updated_at_ms)
		VALUES ('tag-holiday', 'Holiday', ?, ?)
	`, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntity(ctx, Entity{
		ID: "ent-trip", LibraryID: lib.ID, Kind: "album", Title: "Summer",
	}); err != nil {
		t.Fatal(err)
	}
	for _, mid := range []MediaID{partMedia.ID, verMedia.ID} {
		if _, err := st.catalogW.ExecContext(ctx, `
			INSERT INTO media_tag(media_id, tag_id) VALUES (?, 'tag-holiday')
		`, mid); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertEntityRelation(ctx, EntityRelation{
			FromEntityID: "ent-trip", Name: "contains", ToMediaID: mid,
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertMediaFieldsWithSource(ctx, mid, []MediaField{{Name: certificationField, Type: EntityFieldText, Text: "high"}}, MutationSourceOperator); err != nil {
			t.Fatal(err)
		}
	}

	// Also seed one live file paired with a missing legacy SHA-256 Version reservation.
	missLivePath := filepath.Join(root, "offline-live.mov")
	missOfflinePath := filepath.Join(root, "offline-4k.mov")
	missMedia, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib.ID, Path: missLivePath, ContentFingerprint: "sha256:misslive",
		Title: "OfflineGroup", Class: MediaClassVideo, DurationSecondsHint: 10, Container: "mov", Codec: "hevc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO media_file(id, content_fingerprint, media_id, path, version_label, part_ordinal, size_bytes, missing_at_ms, last_seen_at_ms)
		VALUES ('sha256:missoffline', 'sha256:missoffline', ?, ?, '4K', 0, 512, ?, ?)
	`, missMedia.ID, missOfflinePath, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}

	if err := st.ReconcileStandaloneLibraryMedia(ctx, lib.ID); err != nil {
		t.Fatalf("ReconcileStandaloneLibraryMedia: %v", err)
	}

	files, err := st.MediaFilesForLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 5 {
		t.Fatalf("live files count = %d, want 5", len(files))
	}
	var totalFiles, distinctMedia int
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT f.media_id)
		FROM media_file f JOIN media m ON m.id = f.media_id
		WHERE m.library_id = ? AND m.trashed_at_ms IS NULL
	`, lib.ID).Scan(&totalFiles, &distinctMedia); err != nil {
		t.Fatal(err)
	}
	if totalFiles != 6 || distinctMedia != 6 {
		t.Fatalf("total/distinct media_file rows = %d/%d, want 6/6", totalFiles, distinctMedia)
	}
	seenMedia := map[MediaID]bool{}
	allowUnratedFilter := LibraryFilter{
		AllLibraries: true,
		PlaybackGate: true,
		Certification: []CertificationConstraint{
			{PluginID: "photos", Allowed: []string{"low"}, AllowUnrated: true},
		},
	}
	allowHighFilter := LibraryFilter{
		AllLibraries: true,
		PlaybackGate: true,
		Certification: []CertificationConstraint{
			{PluginID: "photos", Allowed: []string{"high"}},
		},
	}
	for _, f := range files {
		if seenMedia[f.MediaID] {
			t.Fatalf("file %s still shares media_id %s", f.Path, f.MediaID)
		}
		seenMedia[f.MediaID] = true
		if f.Path == missLivePath || f.Path == missOfflinePath {
			continue
		}

		var versionLabel string
		var partOrdinal, durationSeconds int
		if err := st.catalogR.QueryRowContext(ctx, `
			SELECT f.version_label, f.part_ordinal, m.duration_seconds
			FROM media_file f JOIN media m ON m.id = f.media_id
			WHERE f.media_id = ?
		`, f.MediaID).Scan(&versionLabel, &partOrdinal, &durationSeconds); err != nil {
			t.Fatal(err)
		}
		if versionLabel != "" || partOrdinal != 0 || durationSeconds != 0 {
			t.Fatalf("split file %s has label=%q part=%d dur=%d, want '', 0, 0", f.Path, versionLabel, partOrdinal, durationSeconds)
		}

		var colCount, tagCount, relCount int
		_ = st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM collection_media WHERE media_id = ? AND collection_id = ?`, f.MediaID, col.ID).Scan(&colCount)
		_ = st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_tag WHERE media_id = ? AND tag_id = 'tag-holiday'`, f.MediaID).Scan(&tagCount)
		_ = st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM entity_relation WHERE to_media_id = ? AND from_entity_id = 'ent-trip'`, f.MediaID).Scan(&relCount)
		if colCount != 1 || tagCount != 1 || relCount != 1 {
			t.Fatalf("split media %s (%s) links col=%d tag=%d rel=%d, want 1/1/1", f.MediaID, f.Path, colCount, tagCount, relCount)
		}
		// Certification "high" must stay denied under AllowUnrated ceiling ("low") and allowed under "high".
		if _, err := st.Media(ctx, f.MediaID, allowUnratedFilter); err == nil {
			t.Fatalf("split media %s (%s) bypassed certification ceiling as unrated", f.MediaID, f.Path)
		}
		if _, err := st.Media(ctx, f.MediaID, allowHighFilter); err != nil {
			t.Fatalf("split media %s (%s) denied under high certification: %v", f.MediaID, f.Path, err)
		}
	}

	// Missing offline legacy Version must retain its reservation for reconnect.
	reserved, err := st.MissingLegacyVersionFingerprints(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reserved[missOfflinePath]) != 1 || reserved[missOfflinePath][0].ContentFingerprint != "sha256:missoffline" {
		t.Fatalf("missing legacy reservation lost after split: %+v", reserved)
	}

	backfill, err := st.LibraryFilePathsWithoutLocalExtraction(ctx, lib.ID, MediaClassVideo)
	if err != nil {
		t.Fatal(err)
	}
	if len(backfill) != 5 {
		t.Fatalf("backfill count = %d (%v), want all 5 live standalone clips", len(backfill), backfill)
	}
}

func TestReconcileStandaloneLibraryMediaAllOfflineCuratedAndScanFieldRules(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	root := t.TempDir()
	lib, err := st.CreateLibraryWithRoots(ctx, "Camera", false, "", "profile-default", []string{root}, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	// Parent Media for checking Extra media_relation preservation across split.
	parentMedia, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib.ID, Path: filepath.Join(root, "parent.mov"), ContentFingerprint: "sha256:parent",
		Title: "Parent Feature", Class: MediaClassVideo, DurationSecondsHint: 120, Container: "mov", Codec: "hevc",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Two-part Extra with curated manual title, operator-owned place, scan-derived
	// camera-model, and an artwork lock without artwork.
	pt1 := filepath.Join(root, "extra-pt1.mov")
	pt2 := filepath.Join(root, "extra-pt2.mov")
	extraMedia, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib.ID, Path: pt1, ContentFingerprint: "sha256:ext1",
		Title: "Extra One", Class: MediaClassVideo, DurationSecondsHint: 15, Container: "mov", Codec: "hevc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		UPDATE media SET extra = 1, title = 'Behind the Scenes', title_identified = 1 WHERE id = ?
	`, extraMedia.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO media_file(id, content_fingerprint, media_id, path, version_label, part_ordinal, last_seen_at_ms)
		VALUES ('sha256:ext2', 'sha256:ext2', ?, ?, '', 1, ?)
	`, extraMedia.ID, pt2, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO media_relation(from_media_id, relation, to_media_id)
		VALUES (?, 'extra-of', ?)
	`, extraMedia.ID, parentMedia.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMediaFieldsWithSource(ctx, extraMedia.ID, []MediaField{
		{Name: "title", Type: EntityFieldText, Text: "Behind the Scenes"},
		{Name: "place", Type: EntityFieldText, Text: "Reykjavik"},
	}, MutationSourceOperator); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMediaFieldsWithSource(ctx, extraMedia.ID, []MediaField{
		{Name: "camera-model", Type: EntityFieldText, Text: "PocketCam 1"},
		{Name: certificationField, Type: EntityFieldText, Text: "high"},
	}, MutationSourceScan); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO media_field_provenance(media_id, field, source) VALUES (?, ?, 'manual')
	`, extraMedia.ID, ArtworkLockField("poster")); err != nil {
		t.Fatal(err)
	}

	// All-offline legacy grouped Versions (both files missing) must still split so
	// simultaneous reconnect does not re-attach them under one Media ID.
	off1 := filepath.Join(root, "offline-1080.mov")
	off2 := filepath.Join(root, "offline-720.mov")
	offMedia, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib.ID, Path: off1, ContentFingerprint: "sha256:off1",
		Title: "Offline Pair", Class: MediaClassVideo, DurationSecondsHint: 12, Container: "mov", Codec: "hevc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		UPDATE media_file SET version_label = '1080p', size_bytes = 100, missing_at_ms = ? WHERE media_id = ?
	`, now.UnixMilli(), offMedia.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO media_file(id, content_fingerprint, media_id, path, version_label, part_ordinal, size_bytes, missing_at_ms, last_seen_at_ms)
		VALUES ('sha256:off2', 'sha256:off2', ?, ?, '720p', 0, 100, ?, ?)
	`, offMedia.ID, off2, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}

	if err := st.ReconcileStandaloneLibraryMedia(ctx, lib.ID); err != nil {
		t.Fatalf("ReconcileStandaloneLibraryMedia: %v", err)
	}

	var splitExtraID MediaID
	if err := st.catalogR.QueryRowContext(ctx, `SELECT media_id FROM media_file WHERE path = ?`, pt2).Scan(&splitExtraID); err != nil {
		t.Fatal(err)
	}
	if splitExtraID == extraMedia.ID {
		t.Fatalf("pt2 stayed on extraMedia %s", extraMedia.ID)
	}
	var splitTitle string
	var splitExtraFlag int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT title, extra FROM media WHERE id = ?`, splitExtraID).Scan(&splitTitle, &splitExtraFlag); err != nil {
		t.Fatal(err)
	}
	if splitTitle != "Behind the Scenes" || splitExtraFlag != 1 {
		t.Fatalf("split extra title/flag = (%q, %d), want ('Behind the Scenes', 1)", splitTitle, splitExtraFlag)
	}
	var relCount int
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM media_relation WHERE from_media_id = ? AND relation = 'extra-of' AND to_media_id = ?
	`, splitExtraID, parentMedia.ID).Scan(&relCount); err != nil || relCount != 1 {
		t.Fatalf("split extra-of relation count = %d (%v), want 1", relCount, err)
	}
	fields, err := st.MediaFields(ctx, splitExtraID, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	hasPlace, hasCamera := false, false
	for _, f := range fields {
		switch f.Name {
		case "place":
			hasPlace = f.Text == "Reykjavik"
		case "camera-model":
			hasCamera = true
		}
	}
	if !hasPlace || hasCamera {
		t.Fatalf("split fields = %+v, want operator place kept and scan camera-model dropped", fields)
	}
	var artLockCount int
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM media_field_provenance WHERE media_id = ? AND field LIKE 'artwork%'
	`, splitExtraID).Scan(&artLockCount); err != nil || artLockCount != 0 {
		t.Fatalf("artwork lock count on split sibling = %d (%v), want 0", artLockCount, err)
	}

	var offMedia1, offMedia2 MediaID
	if err := st.catalogR.QueryRowContext(ctx, `SELECT media_id FROM media_file WHERE path = ?`, off1).Scan(&offMedia1); err != nil {
		t.Fatal(err)
	}
	if err := st.catalogR.QueryRowContext(ctx, `SELECT media_id FROM media_file WHERE path = ?`, off2).Scan(&offMedia2); err != nil {
		t.Fatal(err)
	}
	if offMedia1 == offMedia2 {
		t.Fatalf("all-offline legacy Versions still share MediaID %s after reconcile", offMedia1)
	}
	reserved, err := st.MissingLegacyVersionFingerprints(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reserved[off1]) != 1 || len(reserved[off2]) != 1 {
		t.Fatalf("offline legacy reservations after split = %+v, want both retained", reserved)
	}

	// Curated title must survive a subsequent reimport scan (title_identified preserved).
	if _, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib.ID, Path: pt2, ContentFingerprint: "sha256:ext2",
		Title: "extra-pt2", Class: MediaClassVideo, DurationSecondsHint: 15, Container: "mov", Codec: "hevc",
	}); err != nil {
		t.Fatal(err)
	}
	reimportedTitle := ""
	if err := st.catalogR.QueryRowContext(ctx, `SELECT title FROM media WHERE id = ?`, splitExtraID).Scan(&reimportedTitle); err != nil {
		t.Fatal(err)
	}
	if reimportedTitle != "Behind the Scenes" {
		t.Fatalf("split curated title overwritten on reimport: got %q, want 'Behind the Scenes'", reimportedTitle)
	}

	// Scan-owned certification must remain enforced on split siblings.
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE library SET plugin_id = 'photos' WHERE id = ?`, lib.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Media(ctx, splitExtraID, LibraryFilter{
		AllLibraries:  true,
		PlaybackGate:  true,
		Certification: []CertificationConstraint{{PluginID: "photos", Allowed: []string{"low"}, AllowUnrated: true}},
	}); err == nil {
		t.Fatalf("scan-owned certification allowed under unrated/low ceiling for split sibling %s", splitExtraID)
	}
	var splitCert string
	if err := st.catalogR.QueryRowContext(ctx, `SELECT value FROM media_field_text WHERE media_id = ? AND field = ?`, splitExtraID, certificationField).Scan(&splitCert); err != nil || splitCert != "high" {
		t.Fatalf("scan-owned certification lost on split sibling: %q (%v), want 'high'", splitCert, err)
	}

	// Returning offline legacy Parts (part_ordinal 0 and 1, version_label '') keep split Media IDs.
	partOff1 := filepath.Join(root, "offline-pt1.mov")
	partOff2 := filepath.Join(root, "offline-pt2.mov")
	offPartMedia, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib.ID, Path: partOff1, ContentFingerprint: "sha256:p1",
		Title: "Offline Parts", Class: MediaClassVideo, DurationSecondsHint: 10, Container: "mov", Codec: "hevc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		UPDATE media_file SET missing_at_ms = ? WHERE media_id = ?
	`, now.UnixMilli(), offPartMedia.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO media_file(id, content_fingerprint, media_id, path, version_label, part_ordinal, size_bytes, missing_at_ms, last_seen_at_ms)
		VALUES ('sha256:p2', 'sha256:p2', ?, ?, '', 1, 200, ?, ?)
	`, offPartMedia.ID, partOff2, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := st.ReconcileStandaloneLibraryMedia(ctx, lib.ID); err != nil {
		t.Fatalf("ReconcileStandaloneLibraryMedia parts: %v", err)
	}
	var p1SplitID, p2SplitID MediaID
	if err := st.catalogR.QueryRowContext(ctx, `SELECT media_id FROM media_file WHERE path = ?`, partOff1).Scan(&p1SplitID); err != nil {
		t.Fatal(err)
	}
	if err := st.catalogR.QueryRowContext(ctx, `SELECT media_id FROM media_file WHERE path = ?`, partOff2).Scan(&p2SplitID); err != nil {
		t.Fatal(err)
	}
	if p1SplitID == p2SplitID {
		t.Fatalf("offline parts did not split: both %s", p1SplitID)
	}
	partRes, err := st.MissingLegacyVersionFingerprints(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(partRes[partOff1]) != 1 || len(partRes[partOff2]) != 1 {
		t.Fatalf("missing legacy part reservations = %+v, want both %s and %s", partRes, partOff1, partOff2)
	}
	re1, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib.ID, Path: partOff1, ContentFingerprint: "sha256:p1",
		Title: "offline-pt1", Class: MediaClassVideo, DurationSecondsHint: 10, Container: "mov", Codec: "hevc",
	})
	if err != nil {
		t.Fatal(err)
	}
	re2, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib.ID, Path: partOff2, ContentFingerprint: "sha256:p2",
		Title: "offline-pt2", Class: MediaClassVideo, DurationSecondsHint: 10, Container: "mov", Codec: "hevc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if re1.ID != p1SplitID || re2.ID != p2SplitID {
		t.Fatalf("returning legacy parts changed Media IDs: got (%s, %s), want (%s, %s)", re1.ID, re2.ID, p1SplitID, p2SplitID)
	}
}

func TestReconcileStandaloneLibraryMediaSplitEventsMarkersEncryptedAndSlotNormalize(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	root := t.TempDir()
	pubLib, err := st.CreateLibraryWithRoots(ctx, "Standalone", false, "", "profile-default", []string{root}, "")
	if err != nil {
		t.Fatal(err)
	}
	p1Path := filepath.Join(root, "part1.mov")
	p2Path := filepath.Join(root, "part2.mov")
	pubMedia, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: pubLib.ID, Path: p1Path, ContentFingerprint: "sha256:part1",
		Title: "Combined", Class: MediaClassVideo, DurationSecondsHint: 20, Container: "mov", Codec: "hevc",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO media_file(id, content_fingerprint, media_id, path, version_label, part_ordinal, size_bytes, missing_at_ms, last_seen_at_ms)
		VALUES ('sha256:part2', 'sha256:part2', ?, ?, '', 1, 200, ?, ?)
	`, pubMedia.ID, p2Path, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	filter := LibraryFilter{AllLibraries: true}
	end := 12.0
	for _, method := range []string{"part-boundary", "chapter-import"} {
		if err := st.ReplaceDetectedMarkers(ctx, pubMedia.ID, method, []Marker{{Title: method, StartSeconds: 0, EndSeconds: &end}}, filter, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.CreateMarker(ctx, Marker{MediaID: pubMedia.ID, Title: "Manual", StartSeconds: 5}, filter, now); err != nil {
		t.Fatal(err)
	}

	eventsBefore := len(mediaOutboxEvents(t, st))
	if err := st.ReconcileStandaloneLibraryMedia(ctx, pubLib.ID); err != nil {
		t.Fatalf("ReconcileStandaloneLibraryMedia public: %v", err)
	}
	var splitP2ID MediaID
	if err := st.catalogR.QueryRowContext(ctx, `SELECT media_id FROM media_file WHERE path = ?`, p2Path).Scan(&splitP2ID); err != nil {
		t.Fatal(err)
	}
	eventsAfter := mediaOutboxEvents(t, st)[eventsBefore:]
	if got := countMediaEvents(eventsAfter, MediaEventCreated, splitP2ID); got != 1 {
		t.Fatalf("split sibling media.created count = %d, want 1", got)
	}
	markers, err := st.MarkersForMedia(ctx, pubMedia.ID, filter)
	if err != nil || len(markers) != 1 || markers[0].Title != "Manual" {
		t.Fatalf("surviving clip markers after split = %+v, err=%v; want only manual marker", markers, err)
	}
	_, reFile, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: pubLib.ID, Path: p2Path, ContentFingerprint: "sha256:part2",
		Title: "part2", Class: MediaClassVideo, DurationSecondsHint: 10, Container: "mov", Codec: "hevc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reFile.MediaID != splitP2ID || reFile.PartOrdinal != 0 || reFile.VersionLabel != "" {
		t.Fatalf("reconnected split Part 2 = %+v, want MediaID=%s ordinal=0 label=''", reFile, splitP2ID)
	}
	firstFP, err := firstVersionFingerprintSQL(ctx, st.catalogR, splitP2ID)
	if err != nil || firstFP != "sha256:part2" {
		t.Fatalf("firstVersionFingerprintSQL(%s) = (%q, %v), want sha256:part2", splitP2ID, firstFP, err)
	}

	// Encrypted offline sample1: group keeps both split encrypted Media IDs without emitting outbox events.
	encRoot := t.TempDir()
	encLib, err := st.CreateLibraryWithRoots(ctx, "Private", false, "", "profile-default", []string{encRoot}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryBacking(ctx, encLib.ID, false, true); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, encLib.ID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	ep1 := filepath.Join(encRoot, "enc-pt1.mov")
	ep2 := filepath.Join(encRoot, "enc-pt2.mov")
	encMedia, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: encLib.ID, Path: ep1, ContentFingerprint: "sample1:ep1",
		Title: "Enc Group", Class: MediaClassVideo, DurationSecondsHint: 15, Container: "mov", Codec: "hevc",
	})
	if err != nil {
		t.Fatal(err)
	}
	st.mu.RLock()
	encPlane, err := st.catalogPlaneForLibraryLocked(ctx, encLib.ID)
	st.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encPlane.writer.ExecContext(ctx, `UPDATE media_file SET missing_at_ms = ? WHERE media_id = ?`, now.UnixMilli(), encMedia.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := encPlane.writer.ExecContext(ctx, `
		INSERT INTO media_file(id, content_fingerprint, media_id, path, version_label, part_ordinal, size_bytes, missing_at_ms, last_seen_at_ms)
		VALUES ('sample1:ep2', 'sample1:ep2', ?, ?, '', 1, 200, ?, ?)
	`, encMedia.ID, ep2, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	eventsBeforeEnc := len(mediaOutboxEvents(t, st))
	if err := st.ReconcileStandaloneLibraryMedia(ctx, encLib.ID); err != nil {
		t.Fatalf("ReconcileStandaloneLibraryMedia encrypted: %v", err)
	}
	if got := len(mediaOutboxEvents(t, st)) - eventsBeforeEnc; got != 0 {
		t.Fatalf("encrypted split emitted %d outbox events, want 0", got)
	}
	var e1ID, e2ID MediaID
	if err := encPlane.reader.QueryRowContext(ctx, `SELECT media_id FROM media_file WHERE path = ?`, ep1).Scan(&e1ID); err != nil {
		t.Fatal(err)
	}
	if err := encPlane.reader.QueryRowContext(ctx, `SELECT media_id FROM media_file WHERE path = ?`, ep2).Scan(&e2ID); err != nil {
		t.Fatal(err)
	}
	if e1ID == e2ID {
		t.Fatalf("encrypted offline parts did not split")
	}
	reEnc1, reEncF1, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: encLib.ID, Path: ep1, ContentFingerprint: "sample1:ep1",
		Title: "enc-pt1", Class: MediaClassVideo, DurationSecondsHint: 8, Container: "mov", Codec: "hevc",
	})
	if err != nil {
		t.Fatal(err)
	}
	reEnc2, reEncF2, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: encLib.ID, Path: ep2, ContentFingerprint: "sample1:ep2",
		Title: "enc-pt2", Class: MediaClassVideo, DurationSecondsHint: 7, Container: "mov", Codec: "hevc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reEnc1.ID != e1ID || reEnc2.ID != e2ID || reEncF1.PartOrdinal != 0 || reEncF2.PartOrdinal != 0 {
		t.Fatalf("reconnected encrypted split parts = (%s/%d, %s/%d), want (%s/0, %s/0)", reEnc1.ID, reEncF1.PartOrdinal, reEnc2.ID, reEncF2.PartOrdinal, e1ID, e2ID)
	}

	// Marking the reconnected encrypted file missing a second time and reimporting
	// identical sample1: bytes at the same path must still preserve e1ID.
	if err := st.MarkMediaFileMissing(ctx, encLib.ID, ep1); err != nil {
		t.Fatal(err)
	}
	reEnc1Again, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: encLib.ID, Path: ep1, ContentFingerprint: "sample1:ep1",
		Title: "enc-pt1", Class: MediaClassVideo, DurationSecondsHint: 8, Container: "mov", Codec: "hevc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reEnc1Again.ID != e1ID {
		t.Fatalf("second encrypted sample1: reconnect changed Media ID = %s, want %s", reEnc1Again.ID, e1ID)
	}
	// ReplaceDetectedMarkers on an unlocked encrypted media must no-op cleanly without FK 787.
	if err := st.ReplaceDetectedMarkers(ctx, e1ID, "chapter-import", []Marker{{Title: "Intro", StartSeconds: 0, EndSeconds: &end}}, filter, now); err != nil {
		t.Fatalf("ReplaceDetectedMarkers on encrypted media = %v, want nil", err)
	}
	// Legacy split sha256:part2 must retain its reservation across a second disconnect/reconnect.
	if err := st.MarkMediaFileMissing(ctx, pubLib.ID, p2Path); err != nil {
		t.Fatal(err)
	}
	reFileAgain, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: pubLib.ID, Path: p2Path, ContentFingerprint: "sha256:part2",
		Title: "part2", Class: MediaClassVideo, DurationSecondsHint: 10, Container: "mov", Codec: "hevc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reFileAgain.ID != splitP2ID {
		t.Fatalf("second legacy sha256 reconnect changed Media ID = %s, want %s", reFileAgain.ID, splitP2ID)
	}
}
