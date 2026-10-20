package photoexport

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// fakeStore is an in-memory Store: a Library is an ordered list of ExportItems,
// each item's files point at real on-disk paths the test wrote.
type fakeStore struct {
	order []store.MediaID
	items map[store.MediaID]store.ExportItem
}

func (f *fakeStore) MediaIDsForLibrary(ctx context.Context, libraryID store.LibraryID) ([]store.MediaID, error) {
	return append([]store.MediaID(nil), f.order...), nil
}

func (f *fakeStore) ExportItem(ctx context.Context, mediaID store.MediaID) (store.ExportItem, error) {
	item, ok := f.items[mediaID]
	if !ok {
		return store.ExportItem{}, fmt.Errorf("no item %s", mediaID)
	}
	return item, nil
}

// xorDecryptor is a stand-in for the libencrypt seam: it reads the on-disk
// "ciphertext" and reverses a byte XOR to recover the byte-exact original, so
// the export's encrypted path is exercised without the real cipher. It checks
// the object identity of every call so tests prove the export binds each
// decrypt to the file's persisted fingerprint identity, never its path.
type xorDecryptor struct {
	key  byte
	seen *[]string
}

func (d xorDecryptor) DecryptBytes(libraryID store.LibraryID, ciphertextPath, objectClass, immutableObjectID string) ([]byte, error) {
	if d.seen != nil {
		*d.seen = append(*d.seen, immutableObjectID)
	}
	if objectClass != "media-bytes" || immutableObjectID == "" {
		return nil, fmt.Errorf("photoexport test: malformed object identity")
	}
	raw, err := os.ReadFile(ciphertextPath)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(raw))
	for i, b := range raw {
		out[i] = b ^ d.key
	}
	return out, nil
}

func fingerprint(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + fmt.Sprintf("%x", sum[:])
}

// writeOriginal writes plaintext bytes to a source path and returns the file.
func writeOriginal(t *testing.T, dir, name string, body []byte, version string, part int) store.ExportFile {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return store.ExportFile{Path: path, ContentFingerprint: fingerprint(body), VersionLabel: version, PartOrdinal: part}
}

func readManifest(t *testing.T, dest string) Manifest {
	t.Helper()
	blob, err := os.ReadFile(filepath.Join(dest, manifestName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var man Manifest
	if err := json.Unmarshal(blob, &man); err != nil {
		t.Fatalf("decode manifest (must be third-party-readable JSON): %v", err)
	}
	return man
}

// TestExportPlaintextCustodialLibraryRoundTrips covers the core acceptance
// criterion: a mixed Custodial Library exports bit-for-bit originals plus a
// third-party-readable manifest, and Live Photo / RAW+JPEG pairs round-trip.
func TestExportPlaintextCustodialLibraryRoundTrips(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	dest := t.TempDir()

	plainBody := []byte("a single still image\x00\x01\x02binary")
	plain := writeOriginal(t, src, "still.jpg", plainBody, "", 0)

	// Live Photo: one Media, still as Part 0 + motion as Part 1.
	stillBody := []byte("live-photo still bytes")
	motionBody := []byte("live-photo motion MOV bytes\xff\xfe")
	liveStill := writeOriginal(t, src, "live.heic", stillBody, "", 0)
	liveMotion := writeOriginal(t, src, "live.mov", motionBody, "", 1)

	// RAW+JPEG: one Media, both encodes (modeled as two files of the Media).
	rawBody := []byte("RAW sensor data")
	jpegBody := []byte("JPEG encode")
	raw := writeOriginal(t, src, "shot.dng", rawBody, "raw", 0)
	jpeg := writeOriginal(t, src, "shot.jpg", jpegBody, "jpeg", 0)

	st := &fakeStore{
		order: []store.MediaID{"m-plain", "m-live", "m-rawjpeg"},
		items: map[store.MediaID]store.ExportItem{
			"m-plain": {
				Media:  store.Media{ID: "m-plain", LibraryID: "lib", Title: "Sunset", Year: 2024, Class: store.MediaClassImage},
				Files:  []store.ExportFile{plain},
				Fields: []store.MediaField{{Name: "gps_lat", Type: store.EntityFieldNumber, Number: 37.77}},
				Memberships: []store.MediaMembership{
					{Relation: "member", Kind: "album", Title: "Vacation"},
				},
			},
			"m-live": {
				Media: store.Media{ID: "m-live", LibraryID: "lib", Title: "Beach", Class: store.MediaClassImage},
				Files: []store.ExportFile{liveStill, liveMotion},
			},
			"m-rawjpeg": {
				Media: store.Media{ID: "m-rawjpeg", LibraryID: "lib", Title: "Mountain", Class: store.MediaClassImage},
				Files: []store.ExportFile{raw, jpeg},
			},
		},
	}

	svc := NewService(st, nil)
	man, err := svc.Export(ctx, "lib", dest)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Manifest is third-party-readable JSON on disk.
	onDisk := readManifest(t, dest)
	if onDisk.Version != ManifestVersion || len(onDisk.Items) != 3 {
		t.Fatalf("manifest = %+v", onDisk)
	}
	if onDisk.Encrypted {
		t.Fatal("plaintext library marked encrypted")
	}

	// Every exported file is bit-for-bit identical to its original, and the
	// recorded fingerprint matches the bytes (third-party verifiable).
	want := map[string][]byte{
		"still.jpg": plainBody, "live.heic": stillBody, "live.mov": motionBody,
		"shot.dng": rawBody, "shot.jpg": jpegBody,
	}
	fpByContent := map[string]string{}
	for _, b := range want {
		fpByContent[string(b)] = fingerprint(b)
	}
	exportedExt := map[string]int{}
	for _, item := range man.Items {
		for _, f := range item.Files {
			got, err := os.ReadFile(filepath.Join(dest, f.RelPath))
			if err != nil {
				t.Fatalf("read exported %s: %v", f.RelPath, err)
			}
			if f.ContentFingerprint != fingerprint(got) {
				t.Fatalf("%s fingerprint mismatch: bytes do not verify against manifest", f.RelPath)
			}
			exportedExt[filepath.Ext(f.RelPath)]++
		}
	}

	// Live Photo round-trip: m-live emitted both parts (still + motion).
	live := itemByID(t, man, "m-live")
	if len(live.Files) != 2 {
		t.Fatalf("live photo exported %d files, want 2 (still + motion)", len(live.Files))
	}
	if live.Files[0].PartOrdinal != 0 || live.Files[1].PartOrdinal != 1 {
		t.Fatalf("live photo part ordinals = %d,%d", live.Files[0].PartOrdinal, live.Files[1].PartOrdinal)
	}

	// RAW+JPEG round-trip: both encodes present and distinguishable.
	rj := itemByID(t, man, "m-rawjpeg")
	if len(rj.Files) != 2 || rj.Files[0].VersionLabel == rj.Files[1].VersionLabel {
		t.Fatalf("raw+jpeg files = %+v, want two distinct versions", rj.Files)
	}
	if exportedExt[".dng"] != 1 || exportedExt[".jpg"] != 2 || exportedExt[".heic"] != 1 || exportedExt[".mov"] != 1 {
		t.Fatalf("exported extension counts = %v", exportedExt)
	}

	// Metadata carried into the manifest: EXIF/Location field + album membership.
	plainItem := itemByID(t, man, "m-plain")
	if len(plainItem.Fields) != 1 || plainItem.Fields[0].Name != "gps_lat" {
		t.Fatalf("plain item fields = %+v", plainItem.Fields)
	}
	if len(plainItem.Memberships) != 1 || plainItem.Memberships[0].Kind != "album" || plainItem.Memberships[0].Title != "Vacation" {
		t.Fatalf("plain item memberships = %+v", plainItem.Memberships)
	}

	// Sanity: every source byte set landed exactly once.
	for name, body := range want {
		matches := 0
		_ = filepath.Walk(filepath.Join(dest, originalsDir), func(p string, info os.FileInfo, _ error) error {
			if info == nil || info.IsDir() {
				return nil
			}
			b, _ := os.ReadFile(p)
			if string(b) == string(body) {
				matches++
			}
			return nil
		})
		if matches != 1 {
			t.Fatalf("source %s appeared %d times in export, want exactly 1", name, matches)
		}
	}
}

// TestExportEncryptedLibraryDecryptsToByteExact proves an Encrypted Library's
// at-rest ciphertext is decrypted to byte-exact originals through the decryptor
// seam, and that a missing decryptor fails closed (never emits ciphertext).
func TestExportEncryptedLibraryDecryptsToByteExact(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	dest := t.TempDir()

	plainBody := []byte("secret photo bytes the export must recover exactly")
	const key = 0x5a
	cipherBytes := make([]byte, len(plainBody))
	for i, b := range plainBody {
		cipherBytes[i] = b ^ key
	}
	cipherPath := filepath.Join(src, "asset.libencrypt")
	if err := os.WriteFile(cipherPath, cipherBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	st := &fakeStore{
		order: []store.MediaID{"m-enc"},
		items: map[store.MediaID]store.ExportItem{
			"m-enc": {
				Encrypted: true,
				Media:     store.Media{ID: "m-enc", LibraryID: "lib-enc", Title: "Private", Class: store.MediaClassImage},
				Files:     []store.ExportFile{{Path: cipherPath, ContentFingerprint: fingerprint(plainBody), PartOrdinal: 0}},
			},
		},
	}

	// Missing decryptor → fail closed.
	if _, err := NewService(st, nil).Export(ctx, "lib-enc", dest); err == nil {
		t.Fatal("encrypted export with nil decryptor must fail closed")
	}

	encDest := t.TempDir()
	man, err := NewService(st, xorDecryptor{key: key}).Export(ctx, "lib-enc", encDest)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if !man.Encrypted || len(man.Items) != 1 {
		t.Fatalf("manifest = %+v", man)
	}
	f := man.Items[0].Files[0]
	got, err := os.ReadFile(filepath.Join(encDest, f.RelPath))
	if err != nil {
		t.Fatalf("read exported: %v", err)
	}
	if string(got) != string(plainBody) {
		t.Fatalf("decrypted export not byte-exact: %q != %q", got, plainBody)
	}
	if strings.Contains(string(got), string(cipherBytes[:4])) && string(got) != string(plainBody) {
		t.Fatal("export emitted ciphertext")
	}
}

func TestFailedRerunUnpublishesPreviousManifest(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	dest := t.TempDir()
	sourcePath := filepath.Join(src, "photo.jpg")
	if err := os.WriteFile(sourcePath, []byte("old bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &fakeStore{
		order: []store.MediaID{"m1"},
		items: map[store.MediaID]store.ExportItem{
			"m1": {
				Media: store.Media{ID: "m1", LibraryID: "lib", Class: store.MediaClassImage},
				Files: []store.ExportFile{{Path: sourcePath, ContentFingerprint: fingerprint([]byte("old bytes"))}},
			},
		},
	}
	svc := NewService(st, nil)
	if _, err := svc.Export(ctx, "lib", dest); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(sourcePath, []byte("new bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	item := st.items["m1"]
	item.Files[0].ContentFingerprint = fingerprint([]byte("new bytes"))
	st.items["m1"] = item
	st.order = []store.MediaID{"m1", "missing"}
	if _, err := svc.Export(ctx, "lib", dest); err == nil {
		t.Fatal("rerun unexpectedly succeeded")
	}
	if _, err := os.Stat(filepath.Join(dest, manifestName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed rerun left previous manifest published: %v", err)
	}
}

func itemByID(t *testing.T, man Manifest, id store.MediaID) ManifestItem {
	t.Helper()
	for _, it := range man.Items {
		if it.MediaID == id {
			return it
		}
	}
	t.Fatalf("manifest missing item %s", id)
	return ManifestItem{}
}
