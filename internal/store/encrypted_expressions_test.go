package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// CORE-0769 / ADR-0045: an Encrypted Library's saved filters, smart
// Collections, saved Stations and Channels, with their names and expressions,
// live in that Library's protected plane, never in catalog.db.

type expressionFixture struct {
	filter  SavedFilter
	smart   SmartCollection
	station SavedStation
	channel Channel
}

func createExpressionFixture(t *testing.T, st *Store, libraryID LibraryID, private string, now time.Time) expressionFixture {
	t.Helper()
	ctx := context.Background()
	var fx expressionFixture
	var err error
	fx.filter, err = st.CreateSavedFilter(ctx, SavedFilter{
		ProfileID: "profile-default", LibraryID: libraryID, Name: "Filter " + private,
		FilterJSON: `{"q":"` + private + `"}`,
	}, now)
	if err != nil {
		t.Fatalf("CreateSavedFilter: %v", err)
	}
	fx.smart, err = st.CreateSmartCollection(ctx, SmartCollection{
		LibraryID: libraryID, Name: "Smart " + private,
		SourceExpr: `{"note":"` + private + `"}`, SortExpr: `{"by":"` + private + `"}`,
		CreatedByProfile: "profile-default",
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("CreateSmartCollection: %v", err)
	}
	fx.station, err = st.CreateSavedStation(ctx, SavedStation{
		ProfileID: "profile-default", LibraryID: libraryID, Name: "Station " + private,
		SeedJSON: `{"seed":"` + private + `"}`, RulesJSON: `{"rules":"` + private + `"}`,
		TuningJSON: `{"tuning":"` + private + `"}`,
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("CreateSavedStation: %v", err)
	}
	fx.channel, err = st.CreateChannel(ctx, Channel{
		LibraryID: libraryID, ProfileID: "profile-default", Name: "Channel " + private,
		SourceExpr: `{"kind":"all-items","seed":"` + private + `"}`,
		Ordering:   ChannelOrderingChronological, Repeat: ChannelRepeatOff,
	}, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	return fx
}

func assertExpressionFixtureReads(t *testing.T, st *Store, fx expressionFixture) {
	t.Helper()
	ctx := context.Background()
	if got, err := st.SavedFilterByID(ctx, fx.filter.ID); err != nil || got.Name != fx.filter.Name || got.FilterJSON != fx.filter.FilterJSON || got.ProfileID != fx.filter.ProfileID {
		t.Fatalf("SavedFilterByID = %+v, %v", got, err)
	}
	if got, err := st.SmartCollectionByID(ctx, fx.smart.ID); err != nil || got.Name != fx.smart.Name || got.SourceExpr != fx.smart.SourceExpr || got.SortExpr != fx.smart.SortExpr {
		t.Fatalf("SmartCollectionByID = %+v, %v", got, err)
	}
	if got, err := st.SavedStationByID(ctx, fx.station.ID); err != nil || got.Name != fx.station.Name || got.SeedJSON != fx.station.SeedJSON || got.RulesJSON != fx.station.RulesJSON || got.TuningJSON != fx.station.TuningJSON {
		t.Fatalf("SavedStationByID = %+v, %v", got, err)
	}
	if got, err := st.ChannelByID(ctx, fx.channel.ID); err != nil || got.Name != fx.channel.Name || got.SourceExpr != fx.channel.SourceExpr || got.Ordering != fx.channel.Ordering || got.ProfileID != fx.channel.ProfileID {
		t.Fatalf("ChannelByID = %+v, %v", got, err)
	}
	libraryID := fx.filter.LibraryID
	if got, err := st.SavedFilters(ctx, "profile-default", libraryID); err != nil || len(got) != 1 || got[0].ID != fx.filter.ID {
		t.Fatalf("SavedFilters = %+v, %v", got, err)
	}
	if got, err := st.SmartCollections(ctx, libraryID); err != nil || len(got) != 1 || got[0].ID != fx.smart.ID {
		t.Fatalf("SmartCollections = %+v, %v", got, err)
	}
	if got, err := st.SavedStations(ctx, "profile-default", libraryID); err != nil || len(got) != 1 || got[0].ID != fx.station.ID {
		t.Fatalf("SavedStations = %+v, %v", got, err)
	}
	if got, err := st.Channels(ctx, "profile-default", libraryID); err != nil || len(got) != 1 || got[0].ID != fx.channel.ID {
		t.Fatalf("Channels = %+v, %v", got, err)
	}
}

func assertExpressionFixtureSealed(t *testing.T, st *Store, fx expressionFixture) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.SavedFilterByID(ctx, fx.filter.ID); !errors.Is(err, ErrSavedFilterNotFound) {
		t.Fatalf("sealed SavedFilterByID err = %v", err)
	}
	if _, err := st.SmartCollectionByID(ctx, fx.smart.ID); !errors.Is(err, ErrSmartCollectionNotFound) {
		t.Fatalf("sealed SmartCollectionByID err = %v", err)
	}
	if _, err := st.SavedStationByID(ctx, fx.station.ID); !errors.Is(err, ErrSavedStationNotFound) {
		t.Fatalf("sealed SavedStationByID err = %v", err)
	}
	if _, err := st.ChannelByID(ctx, fx.channel.ID); !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("sealed ChannelByID err = %v", err)
	}
	libraryID := fx.filter.LibraryID
	if got, err := st.SavedFilters(ctx, "profile-default", libraryID); err != nil || len(got) != 0 {
		t.Fatalf("sealed SavedFilters = %+v, %v", got, err)
	}
	if got, err := st.SmartCollections(ctx, libraryID); err != nil || len(got) != 0 {
		t.Fatalf("sealed SmartCollections = %+v, %v", got, err)
	}
	if got, err := st.SavedStations(ctx, "profile-default", libraryID); err != nil || len(got) != 0 {
		t.Fatalf("sealed SavedStations = %+v, %v", got, err)
	}
	if got, err := st.Channels(ctx, "profile-default", libraryID); err != nil || len(got) != 0 {
		t.Fatalf("sealed Channels = %+v, %v", got, err)
	}
}

func countCatalogExpressionRows(t *testing.T, st *Store, libraryID LibraryID) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, table := range []string{"saved_filter", "smart_collection", "saved_station", "channel"} {
		var n int
		if err := st.catalogR.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table+` WHERE library_id = ?`, libraryID).Scan(&n); err != nil {
			t.Fatalf("count catalog %s: %v", table, err)
		}
		counts[table] = n
	}
	return counts
}

func assertNoCatalogExpressionRows(t *testing.T, st *Store, libraryID LibraryID) {
	t.Helper()
	for table, n := range countCatalogExpressionRows(t, st, libraryID) {
		if n != 0 {
			t.Fatalf("catalog.%s holds %d rows for Encrypted Library %s", table, n, libraryID)
		}
	}
}

func TestEncryptedLibraryExpressionsRoundTripAndSealedClose(t *testing.T) {
	ctx := context.Background()
	const title = "Private Film Expr 0769"
	st, media := seededEncryptedMedia(t, title)
	closed := false
	defer func() {
		if !closed {
			_ = st.Close()
		}
	}()
	libraryID := media.LibraryID
	private := string(media.ID) + " " + title
	now := time.UnixMilli(1_800_000_000_000).UTC()
	fx := createExpressionFixture(t, st, libraryID, private, now)
	assertExpressionFixtureReads(t, st, fx)

	// Update and tune while unsealed.
	updatedSmart, err := st.UpdateSmartCollection(ctx, SmartCollection{ID: fx.smart.ID, Name: fx.smart.Name + " Edited", SourceExpr: `{"note":"edited ` + private + `"}`})
	if err != nil || updatedSmart.SortExpr != fx.smart.SortExpr {
		t.Fatalf("UpdateSmartCollection = %+v, %v", updatedSmart, err)
	}
	fx.smart = updatedSmart
	updatedChannel := fx.channel
	updatedChannel.Name += " Edited"
	updatedChannel.Repeat = ChannelRepeatAll
	if fx.channel, err = st.UpdateChannel(ctx, updatedChannel); err != nil || fx.channel.Repeat != ChannelRepeatAll {
		t.Fatalf("UpdateChannel = %+v, %v", fx.channel, err)
	}
	assertExpressionFixtureReads(t, st, fx)
	all := LibraryFilter{AllLibraries: true}
	window, err := st.TuneChannel(ctx, "profile-default", fx.channel.ID, true, all)
	if err != nil || len(window) == 0 {
		t.Fatalf("TuneChannel = %+v, %v", window, err)
	}

	// Direct export and My Data read the protected plane.
	catalog, err := st.ExportCatalog(ctx, all)
	if err != nil {
		t.Fatalf("ExportCatalog: %v", err)
	}
	if len(catalog.Collections) != 1 || catalog.Collections[0].ID != fx.smart.ID || catalog.Collections[0].SourceExpr != fx.smart.SourceExpr {
		t.Fatalf("ExportCatalog collections = %+v", catalog.Collections)
	}
	mine, err := st.ExportMyEngagement(ctx, "profile-default", all)
	if err != nil {
		t.Fatalf("ExportMyEngagement: %v", err)
	}
	if len(mine.SavedFilters) != 1 || mine.SavedFilters[0].FilterJSON != fx.filter.FilterJSON {
		t.Fatalf("My Data saved filters = %+v", mine.SavedFilters)
	}
	if len(mine.Stations) != 1 || mine.Stations[0].SeedJSON != fx.station.SeedJSON || mine.Stations[0].TuningJSON != fx.station.TuningJSON {
		t.Fatalf("My Data stations = %+v", mine.Stations)
	}
	if len(mine.Collections) != 1 || mine.Collections[0].SourceExpr != fx.smart.SourceExpr {
		t.Fatalf("My Data collections = %+v", mine.Collections)
	}
	assertNoCatalogExpressionRows(t, st, libraryID)

	// Sealed reads fail closed and writes refuse.
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	assertExpressionFixtureSealed(t, st, fx)
	if _, err := st.TuneChannel(ctx, "profile-default", fx.channel.ID, true, all); !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("sealed TuneChannel err = %v", err)
	}
	if _, err := st.CreateSavedFilter(ctx, SavedFilter{ProfileID: "profile-default", LibraryID: libraryID, Name: "sealed"}, now); !errors.Is(err, ErrEncryptedLibraryDBDisabled) {
		t.Fatalf("sealed CreateSavedFilter err = %v", err)
	}
	if _, err := st.CreateSmartCollection(ctx, SmartCollection{LibraryID: libraryID, Name: "sealed"}, now); !errors.Is(err, ErrEncryptedLibraryDBDisabled) {
		t.Fatalf("sealed CreateSmartCollection err = %v", err)
	}
	if _, err := st.CreateSavedStation(ctx, SavedStation{ProfileID: "profile-default", LibraryID: libraryID, Name: "sealed"}, now); !errors.Is(err, ErrEncryptedLibraryDBDisabled) {
		t.Fatalf("sealed CreateSavedStation err = %v", err)
	}
	if _, err := st.CreateChannel(ctx, Channel{LibraryID: libraryID, Name: "sealed"}, now); !errors.Is(err, ErrEncryptedLibraryDBDisabled) {
		t.Fatalf("sealed CreateChannel err = %v", err)
	}
	if mine, err := st.ExportMyEngagement(ctx, "profile-default", all); err != nil || len(mine.SavedFilters)+len(mine.Stations)+len(mine.Collections) != 0 {
		t.Fatalf("sealed My Data = %+v, %v", mine, err)
	}

	catalogPath := st.catalogPath
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	closed = true
	assertCatalogFilesLackText(t, catalogPath, private, string(media.ID), title,
		fx.filter.Name, fx.smart.Name, fx.station.Name, fx.channel.Name)

	// A restart without unseal still fails closed; unsealing restores the originals.
	reopened, err := Open(ctx, filepath.Dir(filepath.Dir(catalogPath)), Options{EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	assertExpressionFixtureSealed(t, reopened, fx)
	if err := reopened.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("re-unseal: %v", err)
	}
	assertExpressionFixtureReads(t, reopened, fx)
}

func TestPlaintextLibraryExpressionsStayInCatalog(t *testing.T) {
	ctx := context.Background()
	st, encrypted := seededEncryptedMedia(t, "Expr Encrypted Neighbor")
	defer st.Close()
	plainID := createPlainLibraryForCollection(t, st)
	importPlainMediaForCollection(t, st, plainID, "Public Expr Film")
	fx := createExpressionFixture(t, st, plainID, "public expression", time.UnixMilli(1_800_000_000_000).UTC())
	for table, n := range countCatalogExpressionRows(t, st, plainID) {
		if n != 1 {
			t.Fatalf("catalog.%s rows for plaintext Library = %d, want 1", table, n)
		}
	}
	if err := st.SealEncryptedLibraryDB(ctx, encrypted.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	assertExpressionFixtureReads(t, st, fx)
}

// seedLegacyCatalogExpressions writes the rows a pre-CORE-0769 server left in
// catalog.db for an Encrypted Library.
func seedLegacyCatalogExpressions(t *testing.T, st *Store, libraryID LibraryID, private string) {
	t.Helper()
	ctx := context.Background()
	conn, err := st.catalogW.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()
	stmts := []struct {
		query string
		args  []any
	}{
		{`PRAGMA foreign_keys = OFF`, nil},
		{`INSERT INTO saved_filter (id, profile_id, library_id, name, filter_json, pinned, created_at_ms)
		  VALUES ('saved-filter-legacy-b', 'profile-default', ?, ?, ?, 1, 20)`, []any{libraryID, "Legacy Filter B " + private, `{"q":"` + private + `"}`}},
		{`INSERT INTO saved_filter (id, profile_id, library_id, name, filter_json, pinned, created_at_ms)
		  VALUES ('saved-filter-legacy-a', 'profile-default', ?, 'Legacy Filter A', '{}', 0, 10)`, []any{libraryID}},
		{`INSERT INTO smart_collection (id, library_id, name, source_expr, created_at_ms, created_by_profile_id, sort_expr)
		  VALUES ('smart-legacy', ?, ?, ?, 30, 'profile-default', ?)`, []any{libraryID, "Legacy Smart " + private, `{"note":"` + private + `"}`, `{"by":"` + private + `"}`}},
		{`INSERT INTO saved_station (id, profile_id, library_id, name, seed_json, rules_json, tuning_json, created_at_ms)
		  VALUES ('station-legacy', 'profile-default', ?, ?, ?, ?, ?, 40)`, []any{libraryID, "Legacy Station " + private, `{"seed":"` + private + `"}`, `{"r":1}`, `{"tuning":"` + private + `"}`}},
		{`INSERT INTO channel (id, library_id, profile_id, name, source_expr, ordering_policy, repeat_policy, created_at_ms)
		  VALUES ('channel-legacy', ?, NULL, ?, ?, 'chronological', 'all', 50)`, []any{libraryID, "Legacy Channel " + private, `{"kind":"all-items","seed":"` + private + `"}`}},
		{`PRAGMA foreign_keys = ON`, nil},
	}
	for _, stmt := range stmts {
		if _, err := conn.ExecContext(ctx, stmt.query, stmt.args...); err != nil {
			t.Fatalf("seed legacy rows %q: %v", stmt.query, err)
		}
	}
}

func TestEncryptedLibraryUnsealEvacuatesLegacyPlaintextExpressions(t *testing.T) {
	ctx := context.Background()
	const title = "Legacy Expr Film 0769"
	st, media := seededEncryptedMedia(t, title)
	closed := false
	defer func() {
		if !closed {
			_ = st.Close()
		}
	}()
	libraryID := media.LibraryID
	private := string(media.ID) + " " + title

	// The first unseal of a fresh plane cleared the pending key; re-arm it as a
	// plane migrated from a pre-CORE-0769 server would hold it.
	if _, err := st.encryptedDBs[libraryID].writer.ExecContext(ctx, `
		INSERT OR REPLACE INTO encrypted_library_metadata (key, value) VALUES ('legacy_expression_evacuation', 'pending')`); err != nil {
		t.Fatalf("arm pending key: %v", err)
	}
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	seedLegacyCatalogExpressions(t, st, libraryID, private)

	// Legacy plaintext rows of a sealed Encrypted Library are not served.
	if _, err := st.SmartCollectionByID(ctx, "smart-legacy"); !errors.Is(err, ErrSmartCollectionNotFound) {
		t.Fatalf("sealed legacy SmartCollectionByID err = %v", err)
	}

	// A failed catalog delete on the last table unwinds the whole unseal.
	if _, err := st.catalogW.ExecContext(ctx, `
		CREATE TRIGGER test_block_station_delete BEFORE DELETE ON saved_station
		BEGIN SELECT RAISE(ABORT, 'blocked by test'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err == nil {
		t.Fatal("unseal with blocked evacuation succeeded")
	}
	want := map[string]int{"saved_filter": 2, "smart_collection": 1, "saved_station": 1, "channel": 1}
	for table, n := range countCatalogExpressionRows(t, st, libraryID) {
		if n != want[table] {
			t.Fatalf("catalog.%s after failed evacuation = %d, want %d", table, n, want[table])
		}
	}
	if db := st.encryptedDBs[libraryID]; db != nil && db.state == "unsealed-registered" {
		t.Fatal("failed evacuation left the Library unsealed")
	}
	if _, err := st.catalogW.ExecContext(ctx, `DROP TRIGGER test_block_station_delete`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}

	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	assertNoCatalogExpressionRows(t, st, libraryID)
	set, err := encryptedLibraryMetadataKeySet(ctx, st.encryptedDBs[libraryID].reader, "legacy_expression_evacuation")
	if err != nil || set {
		t.Fatalf("pending key after evacuation = %v, %v", set, err)
	}

	// IDs, ownership, ordering and behavior survive the move.
	filters, err := st.SavedFilters(ctx, "profile-default", libraryID)
	if err != nil || len(filters) != 2 {
		t.Fatalf("evacuated SavedFilters = %+v, %v", filters, err)
	}
	byID := map[SavedFilterID]SavedFilter{filters[0].ID: filters[0], filters[1].ID: filters[1]}
	if !byID["saved-filter-legacy-b"].Pinned || byID["saved-filter-legacy-a"].Pinned || byID["saved-filter-legacy-b"].CreatedAt.UnixMilli() != 20 {
		t.Fatalf("evacuated filters lost fields: %+v", filters)
	}
	smart, err := st.SmartCollectionByID(ctx, "smart-legacy")
	if err != nil || !strings.Contains(smart.SourceExpr, private) || !strings.Contains(smart.SortExpr, private) || smart.CreatedAt.UnixMilli() != 30 {
		t.Fatalf("evacuated SmartCollection = %+v, %v", smart, err)
	}
	var createdBy string
	if err := st.encryptedDBs[libraryID].reader.QueryRowContext(ctx, `SELECT created_by_profile_id FROM smart_collection WHERE id = 'smart-legacy'`).Scan(&createdBy); err != nil || createdBy != "profile-default" {
		t.Fatalf("evacuated smart_collection owner = %q, %v", createdBy, err)
	}
	station, err := st.SavedStationByID(ctx, "station-legacy")
	if err != nil || station.ProfileID != "profile-default" || !strings.Contains(station.TuningJSON, private) || station.CreatedAt.UnixMilli() != 40 {
		t.Fatalf("evacuated SavedStation = %+v, %v", station, err)
	}
	channel, err := st.ChannelByID(ctx, "channel-legacy")
	if err != nil || channel.ProfileID != "" || channel.Ordering != ChannelOrderingChronological || channel.Repeat != ChannelRepeatAll {
		t.Fatalf("evacuated Channel = %+v, %v", channel, err)
	}
	if window, err := st.TuneChannel(ctx, "profile-default", channel.ID, true, LibraryFilter{AllLibraries: true}); err != nil || len(window) == 0 {
		t.Fatalf("evacuated TuneChannel = %+v, %v", window, err)
	}

	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	catalogPath := st.catalogPath
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	closed = true
	assertCatalogFilesLackText(t, catalogPath, private, "Legacy Filter A")
}
