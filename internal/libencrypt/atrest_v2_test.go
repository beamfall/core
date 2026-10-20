package libencrypt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- deterministic test inputs -------------------------------------------

func v2TestDEK() []byte {
	dek := make([]byte, dekLen)
	for i := range dek {
		dek[i] = byte(i)
	}
	return dek
}

func v2TestObj() ObjectContext {
	return ObjectContext{
		LibraryID:         "fixture-library",
		ObjectClass:       ObjectClassMediaBytes,
		ImmutableObjectID: "sha256:fixture-object",
	}
}

// v2TestPlain is a deterministic multi-frame plaintext (one full 64 KiB frame
// plus a short final frame).
func v2TestPlain(n int) []byte {
	plain := make([]byte, n)
	for i := range plain {
		plain[i] = byte((i*131 + 89) % 251)
	}
	return plain
}

func encryptV2(t *testing.T, plain []byte, dek []byte, obj ObjectContext) []byte {
	t.Helper()
	var ct bytes.Buffer
	if err := EncryptStreamV2(&ct, bytes.NewReader(plain), dek, obj); err != nil {
		t.Fatalf("EncryptStreamV2: %v", err)
	}
	return ct.Bytes()
}

// decryptV2Both runs both v2 decoders (stream + seekable reader) and demands
// they agree: both fail, or both succeed with identical plaintext.
func decryptV2Both(t *testing.T, ciphertext, dek []byte, obj ObjectContext) ([]byte, error) {
	t.Helper()
	var streamOut bytes.Buffer
	streamErr := DecryptStreamV2(&streamOut, bytes.NewReader(ciphertext), dek, obj)
	reader, readerErr := NewDecryptReaderV2(bytes.NewReader(ciphertext), int64(len(ciphertext)), dek, obj)
	var readerOut []byte
	if readerErr == nil {
		readerOut, readerErr = io.ReadAll(reader)
	}
	if (streamErr == nil) != (readerErr == nil) {
		t.Fatalf("decoder disagreement: stream err=%v, reader err=%v", streamErr, readerErr)
	}
	if streamErr != nil {
		return nil, streamErr
	}
	if !bytes.Equal(streamOut.Bytes(), readerOut) {
		t.Fatal("stream and reader decoders returned different plaintext")
	}
	return streamOut.Bytes(), nil
}

func mustFailV2(t *testing.T, name string, ciphertext, dek []byte, obj ObjectContext) {
	t.Helper()
	if _, err := decryptV2Both(t, ciphertext, dek, obj); err == nil {
		t.Fatalf("%s: decrypt succeeded, want failure before plaintext", name)
	}
}

// ---- byte contract --------------------------------------------------------

// TestV2ObjectContextEncoding pins the exact context encoding:
// enc(v)=u16be(len)||UTF8(v), concatenated LibraryID||ObjectClass||ObjectID.
func TestV2ObjectContextEncoding(t *testing.T) {
	obj := ObjectContext{LibraryID: "lib", ObjectClass: ObjectClassMediaBytes, ImmutableObjectID: "id-1"}
	got, err := obj.encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	want := []byte{0x00, 0x03, 'l', 'i', 'b', 0x00, 0x0b}
	want = append(want, []byte("media-bytes")...)
	want = append(want, 0x00, 0x04, 'i', 'd', '-', '1')
	if !bytes.Equal(got, want) {
		t.Fatalf("context encoding = %x, want %x", got, want)
	}
}

// TestV2ObjectContextValidation: every field nonempty, valid UTF-8, ≤4096
// bytes; ObjectClass is a closed set.
func TestV2ObjectContextValidation(t *testing.T) {
	valid := v2TestObj()
	cases := map[string]ObjectContext{
		"empty library":  {LibraryID: "", ObjectClass: valid.ObjectClass, ImmutableObjectID: valid.ImmutableObjectID},
		"empty id":       {LibraryID: valid.LibraryID, ObjectClass: valid.ObjectClass, ImmutableObjectID: ""},
		"unknown class":  {LibraryID: valid.LibraryID, ObjectClass: "not-a-class", ImmutableObjectID: valid.ImmutableObjectID},
		"empty class":    {LibraryID: valid.LibraryID, ObjectClass: "", ImmutableObjectID: valid.ImmutableObjectID},
		"oversize field": {LibraryID: strings.Repeat("a", objectContextFieldMax+1), ObjectClass: valid.ObjectClass, ImmutableObjectID: valid.ImmutableObjectID},
		"invalid utf8":   {LibraryID: string([]byte{0xff, 0xfe}), ObjectClass: valid.ObjectClass, ImmutableObjectID: valid.ImmutableObjectID},
	}
	for name, obj := range cases {
		if _, err := obj.encode(); !errors.Is(err, ErrObjectContext) {
			t.Fatalf("%s: err = %v, want ErrObjectContext", name, err)
		}
	}
	max := ObjectContext{LibraryID: strings.Repeat("a", objectContextFieldMax), ObjectClass: valid.ObjectClass, ImmutableObjectID: valid.ImmutableObjectID}
	if _, err := max.encode(); err != nil {
		t.Fatalf("4096-byte field must be accepted: %v", err)
	}
	// Every v2 entry point rejects an invalid context before doing anything.
	if err := EncryptStreamV2(io.Discard, strings.NewReader("x"), v2TestDEK(), ObjectContext{}); !errors.Is(err, ErrObjectContext) {
		t.Fatalf("EncryptStreamV2 empty context: %v", err)
	}
	if err := DecryptStreamV2(io.Discard, strings.NewReader("x"), v2TestDEK(), ObjectContext{}); !errors.Is(err, ErrObjectContext) {
		t.Fatalf("DecryptStreamV2 empty context: %v", err)
	}
	if _, err := NewDecryptReaderV2(bytes.NewReader(nil), 0, v2TestDEK(), ObjectContext{}); !errors.Is(err, ErrObjectContext) {
		t.Fatalf("NewDecryptReaderV2 empty context: %v", err)
	}
}

// TestV2HeaderNonceAADBytes pins the exact header / nonce / AAD byte layout.
func TestV2HeaderNonceAADBytes(t *testing.T) {
	salt := bytes.Repeat([]byte{0xAB}, atRestV2SaltLen)
	header := v2Header(salt)
	if len(header) != atRestV2HeaderLen || atRestV2HeaderLen != 41 {
		t.Fatalf("header length = %d, want 41", len(header))
	}
	wantPrefix := append([]byte("BFE2"), 0x02, 0x00, 0x01, 0x00, 0x00)
	if !bytes.Equal(header[:9], wantPrefix) {
		t.Fatalf("header prefix = %x, want %x (BFE2||0x02||u32be(65536))", header[:9], wantPrefix)
	}
	if !bytes.Equal(header[9:], salt) {
		t.Fatal("header salt bytes mismatch")
	}
	nonce := v2Nonce(0xDEADBEEF)
	if !bytes.Equal(nonce, []byte{0, 0, 0, 0, 0, 0, 0, 0, 0xDE, 0xAD, 0xBE, 0xEF}) {
		t.Fatalf("nonce = %x, want 8 zero bytes || u32be(index)", nonce)
	}
	context := []byte{0x01, 0x02}
	prefix := append(append([]byte{}, header...), context...)
	aad := v2AAD(prefix, 7, true)
	want := append(append([]byte{}, prefix...), 0x00, 0x00, 0x00, 0x07, 0x01)
	if !bytes.Equal(aad, want) {
		t.Fatalf("final AAD = %x, want raw-header||context||u32be(index)||0x01", aad)
	}
	if aadNonFinal := v2AAD(prefix, 7, false); aadNonFinal[len(aadNonFinal)-1] != 0x00 {
		t.Fatal("non-final AAD must end in 0x00")
	}
}

// ---- round trips ----------------------------------------------------------

func TestV2RoundTripAndSeek(t *testing.T) {
	dek := v2TestDEK()
	obj := v2TestObj()
	for _, n := range []int{0, 1, atRestV2ChunkSize - 1, atRestV2ChunkSize, atRestV2ChunkSize + 1, 3*atRestV2ChunkSize + 17} {
		plain := v2TestPlain(n)
		ciphertext := encryptV2(t, plain, dek, obj)
		got, err := decryptV2Both(t, ciphertext, dek, obj)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if !bytes.Equal(got, plain) {
			t.Fatalf("n=%d: round trip not byte-exact", n)
		}
		// Seek into the middle and read a range.
		if n > 10 {
			reader, err := NewDecryptReaderV2(bytes.NewReader(ciphertext), int64(len(ciphertext)), dek, obj)
			if err != nil {
				t.Fatalf("reader: %v", err)
			}
			if _, err := reader.Seek(int64(n/2), io.SeekStart); err != nil {
				t.Fatalf("seek: %v", err)
			}
			buf := make([]byte, 10)
			if _, err := io.ReadFull(reader, buf); err != nil {
				t.Fatalf("range read: %v", err)
			}
			if !bytes.Equal(buf, plain[n/2:n/2+10]) {
				t.Fatalf("n=%d: range read mismatch", n)
			}
		}
	}
}

// ---- the tamper matrix ----------------------------------------------------

func TestV2WrongContextFails(t *testing.T) {
	dek := v2TestDEK()
	obj := v2TestObj()
	ciphertext := encryptV2(t, v2TestPlain(100000), dek, obj)
	for name, wrong := range map[string]ObjectContext{
		"library":  {LibraryID: "other-library", ObjectClass: obj.ObjectClass, ImmutableObjectID: obj.ImmutableObjectID},
		"class":    {LibraryID: obj.LibraryID, ObjectClass: ObjectClassGeneratedAsset, ImmutableObjectID: obj.ImmutableObjectID},
		"objectID": {LibraryID: obj.LibraryID, ObjectClass: obj.ObjectClass, ImmutableObjectID: "sha256:other-object"},
	} {
		mustFailV2(t, "wrong "+name, ciphertext, dek, wrong)
	}
	if _, err := decryptV2Both(t, ciphertext, dek, obj); err != nil {
		t.Fatalf("control: correct context must decrypt: %v", err)
	}
}

// TestV2CrossObjectSpliceFails: two objects under the SAME DEK; a frame from
// object B spliced into object A at the SAME index must fail (the object key
// is salt+context derived, so B's frame never authenticates inside A).
func TestV2CrossObjectSpliceFails(t *testing.T) {
	dek := v2TestDEK()
	objA := v2TestObj()
	objB := ObjectContext{LibraryID: objA.LibraryID, ObjectClass: objA.ObjectClass, ImmutableObjectID: "sha256:object-b"}
	plain := v2TestPlain(2*atRestV2ChunkSize + 100)
	ctA := encryptV2(t, plain, dek, objA)
	ctB := encryptV2(t, plain, dek, objB)

	const fullFrame = atRestV2FrameOverhead + atRestV2ChunkSize
	for _, index := range []int{0, 1} {
		spliced := append([]byte{}, ctA...)
		offset := atRestV2HeaderLen + index*fullFrame
		copy(spliced[offset:offset+fullFrame], ctB[offset:offset+fullFrame])
		mustFailV2(t, fmt.Sprintf("cross-object splice at index %d", index), spliced, dek, objA)
	}
	// Header splice: A's frames under B's header (and vice versa) must fail —
	// the raw header is bound into every frame's AAD.
	headerSwap := append(append([]byte{}, ctB[:atRestV2HeaderLen]...), ctA[atRestV2HeaderLen:]...)
	mustFailV2(t, "header splice", headerSwap, dek, objA)
	mustFailV2(t, "header splice under B", headerSwap, dek, objB)
}

// TestV2WholeObjectSwapFails: presenting object B's complete ciphertext under
// object A's identity (a file swapped on disk) fails.
func TestV2WholeObjectSwapFails(t *testing.T) {
	dek := v2TestDEK()
	objA := v2TestObj()
	objB := ObjectContext{LibraryID: objA.LibraryID, ObjectClass: objA.ObjectClass, ImmutableObjectID: "sha256:object-b"}
	ctB := encryptV2(t, v2TestPlain(50000), dek, objB)
	mustFailV2(t, "whole-object swap", ctB, dek, objA)
}

func TestV2HeaderEditFails(t *testing.T) {
	dek := v2TestDEK()
	obj := v2TestObj()
	ciphertext := encryptV2(t, v2TestPlain(100000), dek, obj)
	for _, offset := range []int{0, 3, 4, 5, 8, 9, 40} { // magic, version, chunk size, salt
		edited := append([]byte{}, ciphertext...)
		edited[offset] ^= 0x01
		mustFailV2(t, fmt.Sprintf("header edit at byte %d", offset), edited, dek, obj)
	}
}

func TestV2ReorderDuplicateTruncationTrailingFail(t *testing.T) {
	dek := v2TestDEK()
	obj := v2TestObj()
	plain := v2TestPlain(2*atRestV2ChunkSize + 100) // frames 0,1 full + final short
	ciphertext := encryptV2(t, plain, dek, obj)
	const fullFrame = atRestV2FrameOverhead + atRestV2ChunkSize
	frame0 := ciphertext[atRestV2HeaderLen : atRestV2HeaderLen+fullFrame]
	frame1 := ciphertext[atRestV2HeaderLen+fullFrame : atRestV2HeaderLen+2*fullFrame]
	finalFrame := ciphertext[atRestV2HeaderLen+2*fullFrame:]

	build := func(frames ...[]byte) []byte {
		out := append([]byte{}, ciphertext[:atRestV2HeaderLen]...)
		for _, f := range frames {
			out = append(out, f...)
		}
		return out
	}
	mustFailV2(t, "reorder", build(frame1, frame0, finalFrame), dek, obj)
	mustFailV2(t, "duplicate", build(frame0, frame0, frame1, finalFrame), dek, obj)
	mustFailV2(t, "truncate final frame", build(frame0, frame1), dek, obj)
	mustFailV2(t, "truncate mid-frame", build(frame0, frame1, finalFrame[:len(finalFrame)-1]), dek, obj)
	mustFailV2(t, "drop interior frame", build(frame0, finalFrame), dek, obj)
	mustFailV2(t, "trailing bytes", append(build(frame0, frame1, finalFrame), 0x00), dek, obj)
}

// TestV2IndexOverflowRejected: geometry implying a frame index past u32 (and a
// plaintext past 2^48−1) is rejected at open, before any frame is read.
func TestV2IndexOverflowRejected(t *testing.T) {
	dek := v2TestDEK()
	obj := v2TestObj()
	ciphertext := encryptV2(t, v2TestPlain(100), dek, obj)
	const fullFrame = int64(atRestV2FrameOverhead + atRestV2ChunkSize)
	overflowSize := int64(atRestV2HeaderLen) + (int64(atRestV2MaxFrameIndex)+1)*fullFrame + int64(atRestV2FrameOverhead)
	if _, err := NewDecryptReaderV2(bytes.NewReader(ciphertext), overflowSize, dek, obj); !errors.Is(err, ErrUnwrap) {
		t.Fatalf("index overflow open: err = %v, want ErrUnwrap", err)
	}
}

// ---- manager-level format-generation behavior -----------------------------

// forceV1Generation rewrites the library's committed key bundle to the legacy
// v1 at-rest format, simulating a library provisioned before v2 existed.
func forceV1Generation(t *testing.T, m *Manager, libraryID string) {
	t.Helper()
	bundle, err := m.loadBundle(libraryID)
	if err != nil {
		t.Fatalf("loadBundle: %v", err)
	}
	bundle.AtRestFormat = atRestFormatV1
	if err := m.storeBundle(libraryID, bundle); err != nil {
		t.Fatalf("storeBundle: %v", err)
	}
}

// TestManagerRenameWithUnchangedContextSucceeds: a rename is invisible to the
// cipher — identity is the persisted context, never the path.
func TestManagerRenameWithUnchangedContextSucceeds(t *testing.T) {
	m, _ := newTestManager()
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	dir := t.TempDir()
	plainPath := filepath.Join(dir, "plain.bin")
	oldPath := filepath.Join(dir, "object-at-old-name.enc")
	if err := os.WriteFile(plainPath, []byte("rename-stable payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.EncryptFile(testLib, plainPath, oldPath, testObj); err != nil {
		t.Fatalf("EncryptFile: %v", err)
	}
	newPath := filepath.Join(dir, "renamed-object.enc")
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	reader, err := m.OpenDecryptReader(testLib, newPath, testObj)
	if err != nil {
		t.Fatalf("OpenDecryptReader after rename: %v", err)
	}
	got, err := io.ReadAll(reader)
	if err != nil || string(got) != "rename-stable payload" {
		t.Fatalf("read after rename = %q, %v", got, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	// Same bytes under a DIFFERENT identity still fail — the rename test's dual.
	other := MediaObjectContext(testLib, "sha256:not-this-object")
	if _, err := m.OpenDecryptReader(testLib, newPath, other); err == nil {
		t.Fatal("swapped identity must fail even though the bytes are genuine")
	}
}

// TestCommittedV2GenerationRejectsRestoredV1: once the key generation commits
// to v2, restored v1 ciphertext under the same DEK is permanently rejected.
func TestCommittedV2GenerationRejectsRestoredV1(t *testing.T) {
	m, _ := newTestManager()
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	dek, err := m.DEK(testLib)
	if err != nil {
		t.Fatalf("DEK: %v", err)
	}
	dir := t.TempDir()
	v1Path := filepath.Join(dir, "restored-v1.enc")
	var v1 bytes.Buffer
	if err := EncryptStream(&v1, strings.NewReader("legacy v1 bytes"), dek); err != nil {
		t.Fatalf("EncryptStream: %v", err)
	}
	if err := os.WriteFile(v1Path, v1.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.OpenDecryptReader(testLib, v1Path, testObj); err == nil {
		t.Fatal("v1 replay into a v2 generation must fail (reader)")
	}
	if err := m.DecryptFile(testLib, v1Path, filepath.Join(dir, "out.bin"), testObj); err == nil {
		t.Fatal("v1 replay into a v2 generation must fail (file)")
	}
	if _, err := os.Stat(filepath.Join(dir, "out.bin")); !os.IsNotExist(err) {
		t.Fatal("plaintext output written despite rejected v1 replay")
	}
}

// TestLegacyV1GenerationSeekAndNoPartialUpgrade: persisted v1 seek/range stays
// readable ONLY while the key generation is v1, and a v1 generation keeps
// writing v1 (no same-DEK partial upgrade — v2 arrives only through Rewrap).
func TestLegacyV1GenerationSeekAndNoPartialUpgrade(t *testing.T) {
	m, _ := newTestManager()
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	forceV1Generation(t, m, testLib)
	dek, err := m.DEK(testLib)
	if err != nil {
		t.Fatalf("DEK: %v", err)
	}
	dir := t.TempDir()
	v1Path := filepath.Join(dir, "legacy.enc")
	payload := v2TestPlain(3*atRestChunkSize + 33)
	var v1 bytes.Buffer
	if err := EncryptStream(&v1, bytes.NewReader(payload), dek); err != nil {
		t.Fatalf("EncryptStream: %v", err)
	}
	if err := os.WriteFile(v1Path, v1.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	// Seek/range read works in the legacy generation.
	reader, err := m.OpenDecryptReader(testLib, v1Path, testObj)
	if err != nil {
		t.Fatalf("legacy OpenDecryptReader: %v", err)
	}
	if _, err := reader.Seek(int64(len(payload)/2), io.SeekStart); err != nil {
		t.Fatalf("seek: %v", err)
	}
	buf := make([]byte, 16)
	if _, err := io.ReadFull(reader, buf); err != nil {
		t.Fatalf("range read: %v", err)
	}
	if !bytes.Equal(buf, payload[len(payload)/2:len(payload)/2+16]) {
		t.Fatal("legacy range read mismatch")
	}
	_ = reader.Close()

	// A v1-generation write stays v1 — same-DEK v2 writes are forbidden until
	// the all-object Rewrap commits a fresh v2-only DEK.
	plainPath := filepath.Join(dir, "new.plain")
	newPath := filepath.Join(dir, "new.enc")
	if err := os.WriteFile(plainPath, []byte("written in v1 generation"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.EncryptFile(testLib, plainPath, newPath, testObj); err != nil {
		t.Fatalf("EncryptFile: %v", err)
	}
	magic := make([]byte, 4)
	f, err := os.Open(newPath) //nolint:gosec // test-owned temp path
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(f, magic); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if string(magic) != atRestMagic {
		t.Fatalf("v1-generation write produced magic %q, want %q (no same-DEK partial upgrade)", magic, atRestMagic)
	}

	// After the generation commits to v2 (as Rewrap does), the same persisted
	// v1 object is no longer readable — legacy seek/range dies with the
	// legacy generation.
	bundle, err := m.loadBundle(testLib)
	if err != nil {
		t.Fatal(err)
	}
	bundle.AtRestFormat = atRestFormatV2
	if err := m.storeBundle(testLib, bundle); err != nil {
		t.Fatal(err)
	}
	if _, err := m.OpenDecryptReader(testLib, v1Path, testObj); err == nil {
		t.Fatal("persisted v1 must be unreadable once the generation commits to v2")
	}
}

// TestNewProvisionCommitsV2 confirms fresh provisions are v2-only generations
// writing context-bound BFE2 objects.
func TestNewProvisionCommitsV2(t *testing.T) {
	m, _ := newTestManager()
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	format, err := m.atRestFormat(testLib)
	if err != nil || format != atRestFormatV2 {
		t.Fatalf("new provision format = %d, %v; want v2", format, err)
	}
	dir := t.TempDir()
	plainPath := filepath.Join(dir, "p.bin")
	ctPath := filepath.Join(dir, "c.enc")
	if err := os.WriteFile(plainPath, []byte("fresh"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.EncryptFile(testLib, plainPath, ctPath, testObj); err != nil {
		t.Fatalf("EncryptFile: %v", err)
	}
	ct, err := os.ReadFile(ctPath) //nolint:gosec // test-owned temp path
	if err != nil {
		t.Fatal(err)
	}
	if string(ct[:4]) != atRestV2Magic || ct[4] != atRestV2Version {
		t.Fatalf("fresh provision wrote magic %x, want BFE2 v2", ct[:5])
	}
	if binary.BigEndian.Uint32(ct[5:9]) != atRestV2ChunkSize {
		t.Fatal("fresh provision header chunk size != 65536")
	}
}
