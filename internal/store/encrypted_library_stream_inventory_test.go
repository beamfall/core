package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// TestEncryptedLibraryImportPersistsStreamInventory drives a probed import into
// an Encrypted Library and reads the stream inventory and font attachments back
// from the library plane (CORE-2055). The encrypted_library schema must accept
// the same shared inventory writer the global catalog uses.
func TestEncryptedLibraryImportPersistsStreamInventory(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	want := []MediaFileStream{
		{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10", Level: 153, BitDepth: 10,
			ColorTransfer: "smpte2084", ColorPrimaries: "bt2020", HDR10Plus: true, DoViProfile: 8, DoViBLCompatID: 1},
		{Index: 1, Type: "audio", Codec: "eac3", Language: "eng", Default: true, Channels: 6, ChannelLayout: "5.1",
			SampleRate: 48000, EncoderDelaySamples: 256, EncoderPaddingSamples: 12, DecodedSamples: 2880000},
		{Index: 2, Type: "subtitle", Codec: "ass", Language: "eng", Title: "English (SDH)", Forced: true, HearingImpaired: true,
			VisualImpaired: true, Commentary: true},
	}
	imported := encryptedImport(libraryID, "sha256:encrypted-streams", "/enc/streams.mkv")
	imported.Streams = want
	imported.FontAttachments = []MediaFileFontAttachment{{Filename: "font.ttf", MimeType: "font/ttf"}}
	if _, _, err := st.ImportMediaFile(ctx, imported); err != nil {
		t.Fatalf("ImportMediaFile with streams: %v", err)
	}
	// A re-probe full-replaces the inventory (delete-then-insert, upserted fonts).
	if _, _, err := st.ImportMediaFile(ctx, imported); err != nil {
		t.Fatalf("re-ImportMediaFile with streams: %v", err)
	}

	db := st.encryptedDBs[libraryID]
	got, err := mediaFileStreamsFromPlane(ctx, db.reader, imported.ContentFingerprint)
	if err != nil {
		t.Fatalf("read encrypted stream inventory: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("encrypted stream inventory = %+v, want %+v", got, want)
	}
	var fonts int
	if err := db.reader.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_file_font_attachment WHERE content_fingerprint = ? AND filename = 'font.ttf' AND mime_type = 'font/ttf'`,
		imported.ContentFingerprint).Scan(&fonts); err != nil {
		t.Fatalf("read encrypted font attachments: %v", err)
	}
	if fonts != 1 {
		t.Fatalf("encrypted font attachments = %d, want 1", fonts)
	}
	var catalogStreams int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_file_stream WHERE content_fingerprint = ?`, imported.ContentFingerprint).Scan(&catalogStreams); err != nil {
		t.Fatalf("catalog stream count: %v", err)
	}
	if catalogStreams != 0 {
		t.Fatalf("catalog stream rows = %d, want 0 (inventory stays inside the seal boundary)", catalogStreams)
	}
}

// TestEncryptedLibraryQualityMergeWritesThroughLibraryPlane folds a
// quality-policy candidate into Media of an unsealed Encrypted Library and
// reads the merged Version and stream inventory back from the library plane
// (CORE-2192). Once the Library is Sealed the merge refuses, as import does.
func TestEncryptedLibraryQualityMergeWritesThroughLibraryPlane(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	media, _, err := st.ImportMediaFile(ctx, encryptedImport(libraryID, "sha256:encrypted-720p", "/enc/atlas-720p.mkv"))
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	want := []MediaFileStream{
		{Index: 0, Type: "video", Codec: "hevc", BitDepth: 10},
		{Index: 1, Type: "audio", Codec: "eac3", Language: "eng", Default: true, Channels: 6},
	}
	candidate := QualityCandidate{
		ContentFingerprint: "sha256:encrypted-1080p",
		Path:               "/enc/atlas-1080p.mkv",
		VideoHeight:        1080,
		Streams:            want,
	}
	promoted, err := st.MergeKeepAsVersion(ctx, libraryID, media.ID, 720, candidate, time.Now().UTC())
	if err != nil {
		t.Fatalf("MergeKeepAsVersion into an unsealed Encrypted Library: %v", err)
	}
	if !promoted {
		t.Fatal("candidate should have been promoted (720 below the 1080 cutoff)")
	}

	db := st.encryptedDBs[libraryID]
	var label string
	if err := db.reader.QueryRowContext(ctx,
		`SELECT version_label FROM media_file WHERE content_fingerprint = ? AND media_id = ?`,
		candidate.ContentFingerprint, media.ID).Scan(&label); err != nil {
		t.Fatalf("read merged Version from the library plane: %v", err)
	}
	if label != "" {
		t.Fatalf("merged Version label = %q, want the active default", label)
	}
	got, err := mediaFileStreamsFromPlane(ctx, db.reader, candidate.ContentFingerprint)
	if err != nil {
		t.Fatalf("read encrypted stream inventory: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("encrypted stream inventory = %+v, want %+v", got, want)
	}
	var catalogRows int
	if err := st.catalogR.QueryRowContext(ctx,
		`SELECT (SELECT COUNT(*) FROM media_file WHERE content_fingerprint = ?) + (SELECT COUNT(*) FROM media_file_stream WHERE content_fingerprint = ?)`,
		candidate.ContentFingerprint, candidate.ContentFingerprint).Scan(&catalogRows); err != nil {
		t.Fatalf("catalog row count: %v", err)
	}
	if catalogRows != 0 {
		t.Fatalf("catalog rows for the merged candidate = %d, want 0 (merge stays inside the seal boundary)", catalogRows)
	}

	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	sealed := QualityCandidate{ContentFingerprint: "sha256:encrypted-2160p", Path: "/enc/atlas-2160p.mkv", VideoHeight: 2160}
	if _, err := st.MergeKeepAsVersion(ctx, libraryID, media.ID, 1080, sealed, time.Now().UTC()); !errors.Is(err, ErrEncryptedLibraryDBDisabled) {
		t.Fatalf("MergeKeepAsVersion into a Sealed Library err = %v, want ErrEncryptedLibraryDBDisabled", err)
	}
}
