package libencrypt

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// roundTrip encrypts plaintext under a fresh DEK and decrypts it back, returning
// the ciphertext and the recovered plaintext.
func roundTrip(t *testing.T, plaintext []byte) (ciphertext, recovered []byte) {
	t.Helper()
	dek, err := newDEK()
	if err != nil {
		t.Fatalf("newDEK: %v", err)
	}
	var ct bytes.Buffer
	if err := EncryptStream(&ct, bytes.NewReader(plaintext), dek); err != nil {
		t.Fatalf("EncryptStream: %v", err)
	}
	var pt bytes.Buffer
	if err := DecryptStream(&pt, bytes.NewReader(ct.Bytes()), dek); err != nil {
		t.Fatalf("DecryptStream: %v", err)
	}
	return ct.Bytes(), pt.Bytes()
}

func TestDecryptReaderSeeksAcrossChunkBoundaries(t *testing.T) {
	dek, err := newDEK()
	if err != nil {
		t.Fatal(err)
	}
	plain := make([]byte, atRestChunkSize*3+137)
	if _, err := rand.Read(plain); err != nil {
		t.Fatal(err)
	}
	var ciphertext bytes.Buffer
	if err := EncryptStream(&ciphertext, bytes.NewReader(plain), dek); err != nil {
		t.Fatal(err)
	}
	reader, err := NewDecryptReader(bytes.NewReader(ciphertext.Bytes()), int64(ciphertext.Len()), dek)
	if err != nil {
		t.Fatal(err)
	}
	start := int64(atRestChunkSize - 19)
	if _, err := reader.Seek(start, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, atRestChunkSize+71)
	if _, err := io.ReadFull(reader, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain[start:start+int64(len(got))]) {
		t.Fatal("seeked plaintext differs across chunk boundary")
	}
	end, err := reader.Seek(0, io.SeekEnd)
	if err != nil {
		t.Fatal(err)
	}
	if end != int64(len(plain)) {
		t.Fatalf("plaintext size = %d, want %d", end, len(plain))
	}
}

func TestDecryptReaderLargeFixtureAllocatesOnlyChunkBoundedMemory(t *testing.T) {
	dek, err := newDEK()
	if err != nil {
		t.Fatal(err)
	}
	plain := make([]byte, atRestChunkSize*128+137)
	var ciphertext bytes.Buffer
	if err := EncryptStream(&ciphertext, bytes.NewReader(plain), dek); err != nil {
		t.Fatal(err)
	}

	// Measure only opening and reading the seekable decrypt view. A whole-file
	// decrypt of this fixture would allocate more than 8 MiB; the fixed ceiling
	// permits several chunk-sized AEAD buffers while remaining independent of
	// the encrypted object's size.
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	reader, err := NewDecryptReader(bytes.NewReader(ciphertext.Bytes()), int64(ciphertext.Len()), dek)
	if err != nil {
		t.Fatal(err)
	}
	var got [1]byte
	if _, err := io.ReadFull(reader, got[:]); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(reader)
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	if max := uint64(8 * atRestChunkSize); allocated > max {
		t.Fatalf("opening and reading large fixture allocated %d bytes, want at most %d (8 chunks)", allocated, max)
	}
}

func TestDecryptReaderFailsClosedOnTamperedRequestedChunk(t *testing.T) {
	dek, err := newDEK()
	if err != nil {
		t.Fatal(err)
	}
	plain := bytes.Repeat([]byte("x"), atRestChunkSize*2+1)
	var ciphertext bytes.Buffer
	if err := EncryptStream(&ciphertext, bytes.NewReader(plain), dek); err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), ciphertext.Bytes()...)
	fullFrameSize := 4 + gcmNonceLen + atRestChunkSize + 16
	secondChunk := atRestHeaderLen + fullFrameSize + 4 + gcmNonceLen + 1
	// The offset above lands inside the second chunk ciphertext, leaving the
	// final chunk intact so opening succeeds and the requested read fails.
	tampered[secondChunk] ^= 0x80
	reader, err := NewDecryptReader(bytes.NewReader(tampered), int64(len(tampered)), dek)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Seek(atRestChunkSize, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, ErrUnwrap) {
		t.Fatalf("Read error = %v, want ErrUnwrap", err)
	}
}

// TestStreamRoundTripSizes covers the chunk-boundary cases: empty, sub-chunk, an
// exact multiple of the chunk size (the case that needs a zero-length final
// chunk), and a multi-chunk-plus-remainder size.
func TestStreamRoundTripSizes(t *testing.T) {
	sizes := []int{
		0,
		1,
		atRestChunkSize - 1,
		atRestChunkSize,
		atRestChunkSize + 1,
		3*atRestChunkSize + 123,
	}
	for _, size := range sizes {
		plaintext := make([]byte, size)
		if _, err := io.ReadFull(rand.Reader, plaintext); err != nil {
			t.Fatalf("rand: %v", err)
		}
		_, recovered := roundTrip(t, plaintext)
		if !bytes.Equal(recovered, plaintext) {
			t.Fatalf("size %d: round-trip mismatch (got %d bytes)", size, len(recovered))
		}
	}
}

// TestCiphertextIsNotPlaintext confirms the at-rest stream does not contain the
// plaintext anywhere — the core "stolen disk reveals nothing" property at the
// stream level (the file-level twin is TestSealedDBFileHasNoPlaintext).
func TestCiphertextIsNotPlaintext(t *testing.T) {
	plaintext := []byte("GPS 37.7749,-122.4194; album=Family Trip; camera=iPhone 15 Pro")
	ciphertext, recovered := roundTrip(t, plaintext)
	if !bytes.Equal(recovered, plaintext) {
		t.Fatal("round-trip mismatch")
	}
	if bytes.Contains(ciphertext, plaintext) {
		t.Fatal("plaintext found verbatim in ciphertext")
	}
	// Even substrings of meaningful metadata must not survive.
	for _, needle := range [][]byte{[]byte("Family Trip"), []byte("37.7749"), []byte("iPhone")} {
		if bytes.Contains(ciphertext, needle) {
			t.Fatalf("metadata substring %q found in ciphertext", needle)
		}
	}
}

// TestWrongDEKFails confirms a different DEK cannot decrypt, with the generic
// ErrUnwrap (never a content- or key-revealing error).
func TestWrongDEKFails(t *testing.T) {
	dek1, _ := newDEK()
	dek2, _ := newDEK()
	var ct bytes.Buffer
	if err := EncryptStream(&ct, bytes.NewReader([]byte("secret metadata")), dek1); err != nil {
		t.Fatalf("EncryptStream: %v", err)
	}
	err := DecryptStream(io.Discard, bytes.NewReader(ct.Bytes()), dek2)
	if !errors.Is(err, ErrUnwrap) {
		t.Fatalf("wrong DEK: want ErrUnwrap, got %v", err)
	}
}

// TestTamperFails confirms flipping a byte in the body fails the AEAD.
func TestTamperFails(t *testing.T) {
	dek, _ := newDEK()
	var ct bytes.Buffer
	if err := EncryptStream(&ct, bytes.NewReader(bytes.Repeat([]byte("x"), atRestChunkSize+50)), dek); err != nil {
		t.Fatalf("EncryptStream: %v", err)
	}
	b := ct.Bytes()
	// Flip a byte well past the header, inside the first sealed chunk.
	b[atRestHeaderLen+20] ^= 0xFF
	if err := DecryptStream(io.Discard, bytes.NewReader(b), dek); !errors.Is(err, ErrUnwrap) {
		t.Fatalf("tamper: want ErrUnwrap, got %v", err)
	}
}

// TestTruncationFails confirms a body truncated before the EOF-marked final
// chunk is rejected — the property a naive per-chunk AEAD misses.
func TestTruncationFails(t *testing.T) {
	dek, _ := newDEK()
	var ct bytes.Buffer
	// Three chunks so there is a body to cut before the final marker.
	if err := EncryptStream(&ct, bytes.NewReader(make([]byte, 3*atRestChunkSize)), dek); err != nil {
		t.Fatalf("EncryptStream: %v", err)
	}
	b := ct.Bytes()
	// Cut the stream partway through — drops the final-flagged chunk.
	truncated := b[:len(b)/2]
	if err := DecryptStream(io.Discard, bytes.NewReader(truncated), dek); !errors.Is(err, ErrUnwrap) {
		t.Fatalf("truncation: want ErrUnwrap, got %v", err)
	}
}

func TestTrailingCiphertextFails(t *testing.T) {
	dek, _ := newDEK()
	var ct bytes.Buffer
	if err := EncryptStream(&ct, bytes.NewReader([]byte("private bytes")), dek); err != nil {
		t.Fatalf("EncryptStream: %v", err)
	}
	tampered := append(append([]byte(nil), ct.Bytes()...), 0x00)
	if err := DecryptStream(io.Discard, bytes.NewReader(tampered), dek); !errors.Is(err, ErrUnwrap) {
		t.Fatalf("trailing ciphertext: want ErrUnwrap, got %v", err)
	}
}

func TestRecoveryKeyRejectsNonCanonicalPaddingBits(t *testing.T) {
	canonical := normalizeRecoveryInput(encodeRecoveryKey(make([]byte, dekLen)))
	bodyLen := base32EncodedLen(dekLen)
	mutated := []byte(canonical)
	mutated[bodyLen-1] = '1'
	if _, err := ParseRecoveryKey(string(mutated)); !errors.Is(err, ErrUnwrap) {
		t.Fatalf("non-canonical recovery key padding: want ErrUnwrap, got %v", err)
	}
}

// TestReorderFails confirms swapping two sealed chunks fails, because the chunk
// index is bound into the AEAD additional data.
func TestReorderFails(t *testing.T) {
	dek, _ := newDEK()
	var ct bytes.Buffer
	if err := EncryptStream(&ct, bytes.NewReader(make([]byte, 2*atRestChunkSize)), dek); err != nil {
		t.Fatalf("EncryptStream: %v", err)
	}
	// Parse the two full-size sealed frames and swap them. Each frame is
	// len(4) || nonce || ciphertext+tag; full chunks are identical sealed length.
	b := ct.Bytes()
	body := b[atRestHeaderLen:]
	frameLen := 4 + gcmNonceLen + atRestChunkSize + 16 // 16 = GCM tag
	if len(body) < 2*frameLen {
		t.Fatalf("unexpected body layout: %d bytes", len(body))
	}
	swapped := make([]byte, len(b))
	copy(swapped, b[:atRestHeaderLen])
	off := atRestHeaderLen
	copy(swapped[off:], body[frameLen:2*frameLen]) // chunk 1 first
	copy(swapped[off+frameLen:], body[:frameLen])  // chunk 0 second
	copy(swapped[off+2*frameLen:], body[2*frameLen:])
	if err := DecryptStream(io.Discard, bytes.NewReader(swapped), dek); !errors.Is(err, ErrUnwrap) {
		t.Fatalf("reorder: want ErrUnwrap, got %v", err)
	}
}

// TestBadHeaderFails confirms a foreign/garbled header is ErrCorrupt, not a
// confusing AEAD error.
func TestBadHeaderFails(t *testing.T) {
	dek, _ := newDEK()
	for _, b := range [][]byte{
		{},
		[]byte("not a beamfall stream"),
		append([]byte("BFE2"), make([]byte, 5)...), // wrong magic
	} {
		if err := DecryptStream(io.Discard, bytes.NewReader(b), dek); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("bad header %q: want ErrCorrupt, got %v", b, err)
		}
	}
}

// TestEncryptDecryptFileRoundTrip exercises the file-level helpers used for the
// metadata-DB lifecycle and bytes/derivatives: encrypt a file to ciphertext,
// confirm the ciphertext file holds no plaintext, decrypt it back byte-exact.
func TestEncryptDecryptFileRoundTrip(t *testing.T) {
	m, _ := newTestManager()
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	dir := t.TempDir()
	plainPath := filepath.Join(dir, "metadata.db")
	ctPath := filepath.Join(dir, "metadata.db.enc")
	outPath := filepath.Join(dir, "metadata.out.db")

	payload := bytes.Repeat([]byte("EXIF:lat=37.77;album=Trip;"), 5000) // ~130 KiB, multi-chunk
	if err := os.WriteFile(plainPath, payload, 0o600); err != nil {
		t.Fatalf("write plaintext: %v", err)
	}
	if err := m.EncryptFile(testLib, plainPath, ctPath, testObj); err != nil {
		t.Fatalf("EncryptFile: %v", err)
	}
	ct, err := os.ReadFile(ctPath)
	if err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}
	if bytes.Contains(ct, []byte("album=Trip")) || bytes.Contains(ct, []byte("EXIF")) {
		t.Fatal("plaintext metadata found in ciphertext file at rest")
	}
	if err := m.DecryptFile(testLib, ctPath, outPath, testObj); err != nil {
		t.Fatalf("DecryptFile: %v", err)
	}
	out, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !bytes.Equal(out, payload) {
		t.Fatal("decrypted file not byte-exact")
	}
	// The working copy must be 0600 (invariant #5 — protected like every artifact).
	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("decrypted working copy perm = %o, want 600", perm)
	}
}

// TestSealedLibraryCannotDecrypt confirms the at-rest file helpers refuse to run
// for a Sealed library — the DEK is not in memory, so EncryptFile/DecryptFile
// return ErrSealed and touch nothing.
func TestSealedLibraryCannotDecrypt(t *testing.T) {
	m, _ := newTestManager()
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	dir := t.TempDir()
	plainPath := filepath.Join(dir, "x.db")
	ctPath := filepath.Join(dir, "x.db.enc")
	if err := os.WriteFile(plainPath, []byte("rows"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := m.EncryptFile(testLib, plainPath, ctPath, testObj); err != nil {
		t.Fatalf("EncryptFile (unsealed): %v", err)
	}
	m.Seal(testLib)
	if err := m.DecryptFile(testLib, ctPath, filepath.Join(dir, "out.db"), testObj); !errors.Is(err, ErrSealed) {
		t.Fatalf("decrypt while sealed: want ErrSealed, got %v", err)
	}
	if err := m.EncryptFile(testLib, plainPath, filepath.Join(dir, "y.enc"), testObj); !errors.Is(err, ErrSealed) {
		t.Fatalf("encrypt while sealed: want ErrSealed, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.db")); !os.IsNotExist(err) {
		t.Fatal("a working copy was written despite Sealed")
	}
}

// TestSealDBFileCycleAndNoPlaintextAtRest is the headline ENC-3 test: the
// encrypt → seal (file is ciphertext, working copy gone, no plaintext on disk) →
// unseal → decrypt round-trip, plus the negative assertion that the sealed DB
// file contains no plaintext metadata and the DEK never persists to the secure
// store.
func TestSealDBFileCycleAndNoPlaintextAtRest(t *testing.T) {
	m, store := newTestManager()
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	dir := t.TempDir()
	ctPath := filepath.Join(dir, "lib.metadata.enc")
	workingPath := filepath.Join(dir, "lib.metadata.working.db")

	// Distinctive plaintext that must never survive at rest.
	secret := []byte("Location: Reykjavik 64.1466,-21.9426 | Album: Iceland 2025 | caption: northern lights")
	if err := os.WriteFile(workingPath, secret, 0o600); err != nil {
		t.Fatalf("write working DB: %v", err)
	}

	// Seal: re-encrypt the working copy and wipe it.
	if err := m.SealDBFile(testLib, workingPath, ctPath, testObj); err != nil {
		t.Fatalf("SealDBFile: %v", err)
	}
	if _, err := os.Stat(workingPath); !os.IsNotExist(err) {
		t.Fatal("working copy still present after SealDBFile — plaintext left on disk")
	}
	ct, err := os.ReadFile(ctPath)
	if err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}
	if bytes.Contains(ct, secret) {
		t.Fatal("full plaintext found in sealed DB file")
	}
	for _, needle := range [][]byte{[]byte("Reykjavik"), []byte("Iceland"), []byte("northern lights"), []byte("64.1466")} {
		if bytes.Contains(ct, needle) {
			t.Fatalf("plaintext metadata %q found in sealed DB file", needle)
		}
	}

	// Drop the DEK entirely (Seal) — the disk now holds only ciphertext and the
	// secure store holds only wrapped blobs. Nothing should decrypt.
	m.Seal(testLib)
	if err := m.UnsealDBFile(testLib, ctPath, filepath.Join(dir, "leak.db"), testObj); !errors.Is(err, ErrSealed) {
		t.Fatalf("UnsealDBFile while Sealed: want ErrSealed, got %v", err)
	}

	// The DEK must never have hit the secure store (invariant #5). The plaintext
	// DEK is the in-memory key; only wrapped blobs + KDF params are persisted.
	dek := mustDEKForTest(t, m, store)
	_ = dek

	// Re-unseal via the KEK and round-trip the metadata back byte-exact.
	if err := m.AutoUnseal(testLib); err != nil {
		t.Fatalf("AutoUnseal: %v", err)
	}
	if err := m.UnsealDBFile(testLib, ctPath, workingPath, testObj); err != nil {
		t.Fatalf("UnsealDBFile: %v", err)
	}
	got, err := os.ReadFile(workingPath)
	if err != nil {
		t.Fatalf("read recovered working DB: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Fatal("recovered metadata not byte-exact after unseal")
	}
}

func TestCrashResidueImageHarness(t *testing.T) {
	if mode := os.Getenv("BEAMFALL_LIBENCRYPT_CRASH_CHILD"); mode != "" {
		crashResidueChild(t, mode)
		return
	}

	cases := []struct {
		name         string
		allowedPlain map[string]bool
	}{
		{name: "write", allowedPlain: map[string]bool{"work/lib.metadata.working.db": true}},
		{name: "post-seal"},
		{name: "unseal", allowedPlain: map[string]bool{"work/lib.metadata.working.db": true}},
		{name: "rewrap"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestCrashResidueImageHarness$")
			cmd.Env = append(os.Environ(),
				"BEAMFALL_LIBENCRYPT_CRASH_CHILD="+tc.name,
				"BEAMFALL_LIBENCRYPT_CRASH_DIR="+dir,
			)
			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("child exit = %v, want killed subprocess", err)
			}
			assertFileTreeLacksBytes(t, dir, "plaintext", crashResiduePlaintext(), tc.allowedPlain)
		})
	}
}

func crashResidueChild(t *testing.T, mode string) {
	t.Helper()
	dir := os.Getenv("BEAMFALL_LIBENCRYPT_CRASH_DIR")
	if dir == "" {
		t.Fatal("missing crash dir")
	}
	workDir := filepath.Join(dir, "work")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	secrets := diskSecretStore{dir: filepath.Join(dir, "secrets")}
	m := NewManagerWithParams(secrets, TestArgon2Params)
	const passphrase = "operator crash harness passphrase"
	workingPath := filepath.Join(workDir, "lib.metadata.working.db")
	ctPath := filepath.Join(workDir, "lib.metadata.enc")
	payload := crashResiduePlaintext()

	switch mode {
	case "write":
		if err := os.WriteFile(workingPath, payload, 0o600); err != nil {
			t.Fatal(err)
		}
	case "post-seal":
		provisionAndSealWorkingDB(t, m, passphrase, workingPath, ctPath, payload)
	case "unseal":
		provisionAndSealWorkingDB(t, m, passphrase, workingPath, ctPath, payload)
		m.Seal(testLib)
		restarted := NewManagerWithParams(secrets, TestArgon2Params)
		if err := restarted.PassphraseUnseal(testLib, passphrase); err != nil {
			t.Fatal(err)
		}
		if err := restarted.UnsealDBFile(testLib, ctPath, workingPath, testObj); err != nil {
			t.Fatal(err)
		}
	case "rewrap":
		if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
			t.Fatal(err)
		}
		plainPath := filepath.Join(workDir, "original.bin")
		if err := os.WriteFile(plainPath, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := m.EncryptFile(testLib, plainPath, ctPath, testObj); err != nil {
			t.Fatal(err)
		}
		if err := wipeFile(plainPath); err != nil {
			t.Fatal(err)
		}
		_, _ = m.Rewrap(testLib, UnsealModeAuto, "", func(c RewrapCipher) error {
			if err := c.RewrapFile(ctPath, testObj); err != nil {
				return err
			}
			killSelf()
			return nil
		})
		t.Fatal("rewrap child survived kill")
	default:
		t.Fatalf("unknown crash mode %q", mode)
	}
	killSelf()
}

func provisionAndSealWorkingDB(t *testing.T, m *Manager, passphrase, workingPath, ctPath string, payload []byte) {
	t.Helper()
	if _, err := m.Provision(testLib, UnsealModePassphrase, passphrase); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workingPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.SealDBFile(testLib, workingPath, ctPath, testObj); err != nil {
		t.Fatal(err)
	}
}

func crashResiduePlaintext() []byte {
	return bytes.Repeat([]byte("CRASH-RESIDUE-PLAINTEXT-GPS=64.1466,-21.9426;ALBUM=ZERO-KEY;"), 256)
}

func killSelf() {
	p, err := os.FindProcess(os.Getpid())
	if err == nil {
		_ = p.Kill()
	}
	os.Exit(2)
}

// mustDEKForTest fetches the in-memory DEK and asserts it never appears in any
// persisted secret (invariant #5). It is the at-rest twin of ENC-2's
// TestNoPlaintextKeyMaterialPersisted, run inside the lifecycle test so the
// assertion holds across an encrypt/seal cycle.
func mustDEKForTest(t *testing.T, m *Manager, store *memStore) []byte {
	t.Helper()
	// Briefly unseal to read the DEK, then re-seal so the caller controls state.
	if err := m.AutoUnseal(testLib); err != nil {
		t.Fatalf("AutoUnseal for DEK read: %v", err)
	}
	dek, err := m.DEK(testLib)
	if err != nil {
		t.Fatalf("DEK: %v", err)
	}
	cp := append([]byte(nil), dek...)
	m.Seal(testLib)
	assertSecretAbsent(t, store, cp)
	return cp
}
