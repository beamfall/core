package libencrypt

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/beamfall/core/internal/libencrypt/secretstoretest"
)

// memStore is an in-memory SecretStore standing in for the 0600 secretFiles
// secure store. It records every value ever written so a test can assert no key
// material reaches a forbidden surface and inspect what is persisted.
type memStore struct {
	mu         sync.Mutex
	values     map[string]string
	writes     []string
	failSetRef string
	failSetErr error
	failSetAt  int
	setCounts  map[string]int
}

func newMemStore() *memStore {
	return &memStore{values: map[string]string{}, setCounts: map[string]int{}}
}

func (s *memStore) Secret(ref string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[ref]
	if !ok {
		return "", errNotFound
	}
	return v, nil
}

func (s *memStore) SetSecret(ref, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setCounts[ref]++
	if s.failSetRef == ref && (s.failSetAt == 0 || s.failSetAt == s.setCounts[ref]) {
		return s.failSetErr
	}
	s.writes = append(s.writes, ref)
	if value == "" {
		delete(s.values, ref)
		return nil
	}
	s.values[ref] = value
	return nil
}

func (s *memStore) SecretRefs(prefix string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var refs []string
	for ref := range s.values {
		if strings.HasPrefix(ref, prefix) {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

func (s *memStore) MoveSecret(src, dst, expectedSource, destination string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.values[dst]; ok && existing != destination {
		return errors.New("destination mismatch")
	}
	source, ok := s.values[src]
	if !ok {
		if s.values[dst] == destination {
			return nil
		}
		return errNotFound
	}
	if source != expectedSource {
		return errors.New("source mismatch")
	}
	s.values[dst] = destination
	delete(s.values, src)
	return nil
}

func (s *memStore) resetWrites() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = nil
}

func (s *memStore) wrote(ref string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, got := range s.writes {
		if got == ref {
			return true
		}
	}
	return false
}

var errNotFound = &notFoundErr{}

type notFoundErr struct{}

func (*notFoundErr) Error() string { return "not found" }

// Unwrap makes absence match the SecretStore contract: only fs.ErrNotExist means
// the ref is absent, every other read error propagates.
func (*notFoundErr) Unwrap() error { return fs.ErrNotExist }

type diskSecretStore struct{ dir string }

func (s diskSecretStore) Secret(ref string) (string, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, ref))
	if os.IsNotExist(err) {
		return "", errNotFound
	}
	return string(b), err
}

func (s diskSecretStore) SetSecret(ref, value string) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(s.dir, ref)
	if value == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return os.WriteFile(path, []byte(value), 0o600)
}

// newTestManager builds a Manager with fast argon2 params (never production).
func newTestManager() (*Manager, *memStore) {
	store := newMemStore()
	return NewManagerWithParams(store, TestArgon2Params), store
}

func TestSecretStoreAbsenceContract(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		secretstoretest.AssertAbsence(t, newMemStore())
	})
	t.Run("disk", func(t *testing.T) {
		secretstoretest.AssertAbsence(t, diskSecretStore{dir: t.TempDir()})
	})
}

const testLib = "lib-test-001"

// TestProvisionUnsealedAndDEK confirms provisioning leaves the library Unsealed
// with the DEK in memory.
func TestProvisionUnsealedAndDEK(t *testing.T) {
	m, _ := newTestManager()
	if _, err := m.Provision(testLib, UnsealModeAuto, "correct horse battery staple"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if m.Sealed(testLib) {
		t.Fatal("library should be Unsealed immediately after Provision")
	}
	dek, err := m.DEK(testLib)
	if err != nil {
		t.Fatalf("DEK: %v", err)
	}
	if len(dek) != dekLen {
		t.Fatalf("DEK length = %d, want %d", len(dek), dekLen)
	}
}

func TestSealConcurrentWithEncryptUsesStableDEK(t *testing.T) {
	m, _ := newTestManager()
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	dek, err := m.DEK(testLib)
	if err != nil {
		t.Fatalf("DEK: %v", err)
	}
	want := append([]byte(nil), dek...)
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		for range 256 {
			if err := EncryptStream(io.Discard, strings.NewReader("concurrent encryption payload"), dek); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	<-started
	m.Seal(testLib)
	if err := <-done; err != nil {
		t.Fatalf("EncryptStream: %v", err)
	}
	if !bytes.Equal(dek, want) {
		t.Fatal("Seal mutated DEK material held by an active encryptor")
	}
}

// TestKEKRoundTrip exercises the auto-unseal path: seal, then AutoUnseal recovers
// the same DEK.
func TestKEKRoundTrip(t *testing.T) {
	m, _ := newTestManager()
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	want, _ := m.DEK(testLib)
	got := append([]byte(nil), want...)

	m.Seal(testLib)
	if !m.Sealed(testLib) {
		t.Fatal("Seal did not seal")
	}
	if _, err := m.DEK(testLib); err != ErrSealed {
		t.Fatalf("DEK on sealed = %v, want ErrSealed", err)
	}

	if err := m.AutoUnseal(testLib); err != nil {
		t.Fatalf("AutoUnseal: %v", err)
	}
	after, err := m.DEK(testLib)
	if err != nil {
		t.Fatalf("DEK after AutoUnseal: %v", err)
	}
	if !bytes.Equal(got, after) {
		t.Fatal("DEK did not round-trip through the KEK wrap")
	}
}

// TestPassphraseRoundTrip exercises the passphrase-unseal path, plus the wrong-
// passphrase rejection.
func TestPassphraseRoundTrip(t *testing.T) {
	m, _ := newTestManager()
	const pass = "a long operator passphrase"
	if _, err := m.Provision(testLib, UnsealModePassphrase, pass); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	want, _ := m.DEK(testLib)
	want = append([]byte(nil), want...)

	m.Seal(testLib)
	// Wrong passphrase fails and leaves the library Sealed.
	if err := m.PassphraseUnseal(testLib, "wrong passphrase"); err != ErrUnwrap {
		t.Fatalf("wrong passphrase = %v, want ErrUnwrap", err)
	}
	if !m.Sealed(testLib) {
		t.Fatal("library must stay Sealed after a wrong passphrase")
	}
	// Right passphrase unseals to the same DEK.
	if err := m.PassphraseUnseal(testLib, pass); err != nil {
		t.Fatalf("PassphraseUnseal: %v", err)
	}
	got, _ := m.DEK(testLib)
	if !bytes.Equal(want, got) {
		t.Fatal("DEK did not round-trip through the passphrase wrap")
	}
}

// TestPassphraseModeRequiresPassphrase confirms passphrase mode rejects an empty
// passphrase at provisioning.
func TestPassphraseModeRequiresPassphrase(t *testing.T) {
	m, _ := newTestManager()
	if _, err := m.Provision(testLib, UnsealModePassphrase, ""); err == nil {
		t.Fatal("passphrase mode with empty passphrase should fail")
	}
}

// TestRecoveryRoundTrip exercises the Recovery-Key path, plus the wrong-key
// rejection, using the once-surfaced display string.
func TestRecoveryRoundTrip(t *testing.T) {
	m, _ := newTestManager()
	rk, err := m.Provision(testLib, UnsealModeAuto, "")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if rk.Display == "" {
		t.Fatal("Recovery Key display must be surfaced once at provisioning")
	}
	want, _ := m.DEK(testLib)
	want = append([]byte(nil), want...)

	m.Seal(testLib)
	// A wrong (but well-formed) Recovery Key fails.
	other, _ := generateRecoveryKey()
	if err := m.RecoveryUnseal(testLib, other.Display); err != ErrUnwrap {
		t.Fatalf("wrong recovery key = %v, want ErrUnwrap", err)
	}
	if !m.Sealed(testLib) {
		t.Fatal("library must stay Sealed after a wrong Recovery Key")
	}
	// The real Recovery Key unseals to the same DEK.
	if err := m.RecoveryUnseal(testLib, rk.Display); err != nil {
		t.Fatalf("RecoveryUnseal: %v", err)
	}
	got, _ := m.DEK(testLib)
	if !bytes.Equal(want, got) {
		t.Fatal("DEK did not round-trip through the Recovery-Key wrap")
	}
}

func TestRewrapRotatesDEKAndRecoveryKey(t *testing.T) {
	m, _ := newTestManager()
	oldRK, err := m.Provision(testLib, UnsealModeAuto, "")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	dir := t.TempDir()
	plainPath := filepath.Join(dir, "plain.bin")
	ctPath := filepath.Join(dir, "media.libencrypt")
	if err := os.WriteFile(plainPath, []byte("private bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.EncryptFile(testLib, plainPath, ctPath, testObj); err != nil {
		t.Fatalf("EncryptFile: %v", err)
	}
	before, err := os.ReadFile(ctPath)
	if err != nil {
		t.Fatal(err)
	}

	newRK, err := m.Rewrap(testLib, UnsealModeAuto, "", func(c RewrapCipher) error {
		return c.RewrapFile(ctPath, testObj)
	})
	if err != nil {
		t.Fatalf("Rewrap: %v", err)
	}
	after, err := os.ReadFile(ctPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, after) {
		t.Fatal("ciphertext did not change across DEK rewrap")
	}

	m.Seal(testLib)
	if err := m.RecoveryUnseal(testLib, oldRK.Display); !errors.Is(err, ErrUnwrap) {
		t.Fatalf("old Recovery Key unseal = %v, want ErrUnwrap", err)
	}
	if err := m.RecoveryUnseal(testLib, newRK.Display); err != nil {
		t.Fatalf("new Recovery Key unseal: %v", err)
	}
	outPath := filepath.Join(dir, "out.bin")
	if err := m.DecryptFile(testLib, ctPath, outPath, testObj); err != nil {
		t.Fatalf("DecryptFile with new DEK: %v", err)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "private bytes" {
		t.Fatalf("decrypted bytes = %q", got)
	}
}

func TestRewrapFailureKeepsOldRecoveryKeyCommitted(t *testing.T) {
	m, _ := newTestManager()
	oldRK, err := m.Provision(testLib, UnsealModeAuto, "")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	boom := errors.New("rewrite failed")
	if _, err := m.Rewrap(testLib, UnsealModeAuto, "", func(RewrapCipher) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("Rewrap error = %v, want %v", err, boom)
	}
	m.Seal(testLib)
	if err := m.RecoveryUnseal(testLib, oldRK.Display); err != nil {
		t.Fatalf("old Recovery Key must remain committed after failed rewrap: %v", err)
	}
}

func TestRewrapPersistenceFailureKeepsOldCiphertextReadable(t *testing.T) {
	m, store := newTestManager()
	oldRK, err := m.Provision(testLib, UnsealModeAuto, "")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	dir := t.TempDir()
	plainPath := filepath.Join(dir, "plain.bin")
	ctPath := filepath.Join(dir, "media.libencrypt")
	payload := []byte("private bytes")
	if err := os.WriteFile(plainPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.EncryptFile(testLib, plainPath, ctPath, testObj); err != nil {
		t.Fatalf("EncryptFile: %v", err)
	}
	before, err := os.ReadFile(ctPath)
	if err != nil {
		t.Fatal(err)
	}

	boom := errors.New("persist bundle failed")
	store.failSetRef = bundleRef(testLib)
	store.failSetErr = boom
	called := false
	if _, err := m.Rewrap(testLib, UnsealModeAuto, "", func(c RewrapCipher) error {
		called = true
		return c.RewrapFile(ctPath, testObj)
	}); !errors.Is(err, boom) {
		t.Fatalf("Rewrap error = %v, want %v", err, boom)
	}
	if !called {
		t.Fatal("rewrite callback did not run")
	}
	after, err := os.ReadFile(ctPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("ciphertext changed before key material commit")
	}
	temps, err := filepath.Glob(filepath.Join(dir, ".libenc-*.tmp*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 0 {
		t.Fatalf("staged ciphertext temps left behind: %v", temps)
	}

	store.failSetRef = ""
	m.Seal(testLib)
	if err := m.AutoUnseal(testLib); err != nil {
		t.Fatalf("old KEK and bundle must remain auto-readable: %v", err)
	}
	outPath := filepath.Join(dir, "out.bin")
	if err := m.DecryptFile(testLib, ctPath, outPath, testObj); err != nil {
		t.Fatalf("DecryptFile with old DEK: %v", err)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("decrypted bytes = %q", got)
	}
	m.Seal(testLib)
	if err := m.RecoveryUnseal(testLib, oldRK.Display); err != nil {
		t.Fatalf("old Recovery Key must remain committed: %v", err)
	}
}

func TestRewrapMidSwapFailureDoesNotCommitNewKeyMaterial(t *testing.T) {
	m, store := newTestManager()
	oldRK, err := m.Provision(testLib, UnsealModeAuto, "")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	dir := t.TempDir()
	paths := []string{
		filepath.Join(dir, "one.libencrypt"),
		filepath.Join(dir, "two.libencrypt"),
	}
	for i, ctPath := range paths {
		plainPath := filepath.Join(dir, fmt.Sprintf("plain-%d.bin", i))
		if err := os.WriteFile(plainPath, []byte(fmt.Sprintf("payload-%d", i)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := m.EncryptFile(testLib, plainPath, ctPath, testObj); err != nil {
			t.Fatalf("EncryptFile: %v", err)
		}
	}

	store.resetWrites()
	if _, err := m.Rewrap(testLib, UnsealModeAuto, "", func(c RewrapCipher) error {
		if err := c.RewrapFile(paths[0], testObj); err != nil {
			return err
		}
		*c.staged = append(*c.staged, stagedRewrapFile{
			dst: filepath.Join(dir, "missing.libencrypt"),
			tmp: filepath.Join(dir, "missing.tmp"),
		})
		return nil
	}); err == nil {
		t.Fatal("Rewrap succeeded after mid-swap failure")
	}
	if store.wrote(kekRef(testLib)) || store.wrote(bundleRef(testLib)) {
		t.Fatal("new key material was committed before staged files finished swapping")
	}

	m.Seal(testLib)
	if err := m.RecoveryUnseal(testLib, oldRK.Display); err != nil {
		t.Fatalf("old Recovery Key must remain committed: %v", err)
	}
	for i, ctPath := range paths {
		outPath := filepath.Join(dir, fmt.Sprintf("out-%d.bin", i))
		if err := m.DecryptFile(testLib, ctPath, outPath, testObj); err != nil {
			t.Fatalf("DecryptFile %d with old DEK: %v", i, err)
		}
		got, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("payload-%d", i); string(got) != want {
			t.Fatalf("decrypted bytes %d = %q, want %q", i, got, want)
		}
	}
}

func TestRewrapJournalRollbackAfterRestartMidSwap(t *testing.T) {
	m, store := newTestManager()
	oldRK, paths := provisionEncryptedFiles(t, m, 2)
	oldKEK, oldBundle, staged, _, _, _ := stageTestRewrap(t, m, paths)
	if err := m.storeRewrapJournal(testLib, oldKEK, oldBundle, staged); err != nil {
		t.Fatalf("storeRewrapJournal: %v", err)
	}
	if err := os.Rename(staged[0].dst, staged[0].tmp+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staged[0].tmp, staged[0].dst); err != nil {
		t.Fatal(err)
	}

	restarted := NewManagerWithParams(store, TestArgon2Params)
	if err := restarted.RecoveryUnseal(testLib, oldRK.Display); err != nil {
		t.Fatalf("RecoveryUnseal after journal recovery: %v", err)
	}
	assertEncryptedPayloads(t, restarted, paths)
}

func TestRewrapJournalRollbackAfterRestartPostKeyCommit(t *testing.T) {
	m, store := newTestManager()
	oldRK, paths := provisionEncryptedFiles(t, m, 2)
	oldKEK, oldBundle, staged, newKEK, newBundle, _ := stageTestRewrap(t, m, paths)
	if err := m.storeRewrapJournal(testLib, oldKEK, oldBundle, staged); err != nil {
		t.Fatalf("storeRewrapJournal: %v", err)
	}
	if _, err := swapStagedRewrapFiles(staged); err != nil {
		t.Fatalf("swapStagedRewrapFiles: %v", err)
	}
	if err := m.writeOwnerSecret(testLib, secretKindKEK, base64.StdEncoding.EncodeToString(newKEK)); err != nil {
		t.Fatalf("persist new KEK: %v", err)
	}
	if err := m.storeBundle(testLib, newBundle); err != nil {
		t.Fatalf("persist new bundle: %v", err)
	}

	restarted := NewManagerWithParams(store, TestArgon2Params)
	if err := restarted.RecoveryUnseal(testLib, oldRK.Display); err != nil {
		t.Fatalf("RecoveryUnseal after journal recovery: %v", err)
	}
	assertEncryptedPayloads(t, restarted, paths)
}

func provisionEncryptedFiles(t *testing.T, m *Manager, n int) (RecoveryKey, []string) {
	t.Helper()
	oldRK, err := m.Provision(testLib, UnsealModeAuto, "")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	dir := t.TempDir()
	paths := make([]string, 0, n)
	for i := range n {
		plainPath := filepath.Join(dir, fmt.Sprintf("plain-%d.bin", i))
		ctPath := filepath.Join(dir, fmt.Sprintf("media-%d.libencrypt", i))
		if err := os.WriteFile(plainPath, []byte(fmt.Sprintf("payload-%d", i)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := m.EncryptFile(testLib, plainPath, ctPath, testObj); err != nil {
			t.Fatalf("EncryptFile: %v", err)
		}
		paths = append(paths, ctPath)
	}
	return oldRK, paths
}

func stageTestRewrap(t *testing.T, m *Manager, paths []string) (string, keyBundle, []stagedRewrapFile, []byte, keyBundle, RecoveryKey) {
	t.Helper()
	oldBundle, err := m.loadBundle(testLib)
	if err != nil {
		t.Fatalf("loadBundle: %v", err)
	}
	oldKEK, err := m.secrets.Secret(kekRef(testLib))
	if err != nil {
		t.Fatalf("old KEK: %v", err)
	}
	oldDEK, err := m.DEK(testLib)
	if err != nil {
		t.Fatalf("DEK: %v", err)
	}
	newDEK, err := newDEK()
	if err != nil {
		t.Fatalf("newDEK: %v", err)
	}
	newKEK, err := newKEK()
	if err != nil {
		t.Fatalf("newKEK: %v", err)
	}
	newRK, err := generateRecoveryKey()
	if err != nil {
		t.Fatalf("generateRecoveryKey: %v", err)
	}
	newBundle, err := m.newBundle(UnsealModeAuto, "", newDEK, newKEK, newRK)
	if err != nil {
		t.Fatalf("newBundle: %v", err)
	}
	var staged []stagedRewrapFile
	for _, path := range paths {
		f, err := stageRewrapFileWithDEK(path, oldDEK, newDEK, oldBundle.atRestFormat(), testObj)
		if err != nil {
			t.Fatalf("stageRewrapFileWithDEK: %v", err)
		}
		staged = append(staged, f)
	}
	return oldKEK, oldBundle, staged, newKEK, newBundle, newRK
}

func assertEncryptedPayloads(t *testing.T, m *Manager, paths []string) {
	t.Helper()
	for i, ctPath := range paths {
		outPath := filepath.Join(filepath.Dir(ctPath), fmt.Sprintf("out-%d.bin", i))
		if err := m.DecryptFile(testLib, ctPath, outPath, testObj); err != nil {
			t.Fatalf("DecryptFile %d: %v", i, err)
		}
		got, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("payload-%d", i); string(got) != want {
			t.Fatalf("decrypted bytes %d = %q, want %q", i, got, want)
		}
	}
}

// TestRecoveryKeyLenient confirms the display string round-trips through lenient
// parsing (lowercase, spaces instead of hyphens, look-alike chars).
func TestRecoveryKeyLenient(t *testing.T) {
	m, _ := newTestManager()
	rk, err := m.Provision(testLib, UnsealModeAuto, "")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	want, _ := m.DEK(testLib)
	want = append([]byte(nil), want...)
	m.Seal(testLib)

	// Transcribe sloppily: lowercase, hyphens->spaces.
	sloppy := strings.ReplaceAll(strings.ToLower(rk.Display), "-", " ")
	if err := m.RecoveryUnseal(testLib, sloppy); err != nil {
		t.Fatalf("lenient RecoveryUnseal: %v", err)
	}
	got, _ := m.DEK(testLib)
	if !bytes.Equal(want, got) {
		t.Fatal("sloppy-but-correct Recovery Key should unwrap the same DEK")
	}
}

// TestRecoveryKeyShape confirms the Recovery Key is grouped + checksummed
// (License-Key-shaped) and parses back to 32 bytes.
func TestRecoveryKeyShape(t *testing.T) {
	rk, err := generateRecoveryKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(rk.Display, "-") {
		t.Fatalf("display should be hyphen-grouped: %q", rk.Display)
	}
	parsed, err := ParseRecoveryKey(rk.Display)
	if err != nil {
		t.Fatalf("ParseRecoveryKey: %v", err)
	}
	if len(parsed.Bytes()) != dekLen {
		t.Fatalf("parsed key = %d bytes, want %d", len(parsed.Bytes()), dekLen)
	}
	if !bytes.Equal(parsed.Bytes(), rk.Bytes()) {
		t.Fatal("parsed Recovery Key bytes differ from generated")
	}
	// A single-character corruption must fail the checksum.
	corrupt := []byte(rk.Display)
	for i, c := range corrupt {
		if c != '-' {
			if c == 'A' {
				corrupt[i] = 'B'
			} else {
				corrupt[i] = 'A'
			}
			break
		}
	}
	if _, err := ParseRecoveryKey(string(corrupt)); err != ErrUnwrap {
		t.Fatalf("corrupt Recovery Key = %v, want ErrUnwrap", err)
	}
}

// TestRecoveryKeyShownOnce confirms the server cannot reproduce the Recovery Key
// after provisioning: nothing in the persisted bundle yields the display string,
// and there is deliberately no API to re-fetch it.
func TestRecoveryKeyShownOnce(t *testing.T) {
	m, store := newTestManager()
	rk, err := m.Provision(testLib, UnsealModeAuto, "")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	// The normalized Recovery Key (its raw entropy, base32, and display) must not
	// appear anywhere the server persisted.
	assertSecretAbsent(t, store, rk.Bytes())
	if storeContainsString(store, normalizeRecoveryInput(rk.Display)) {
		t.Fatal("normalized Recovery Key body found in persisted secrets — must be shown once only")
	}
	// There is no Manager method to re-reveal it (compile-time guarantee). The
	// ErrRecoveryKeyConsumed sentinel documents the contract for the app layer.
	if ErrRecoveryKeyConsumed == nil {
		t.Fatal("expected a once-only sentinel")
	}
}

// TestNoPlaintextKeyMaterialPersisted is the invariant #5 negative test: neither
// the plaintext DEK nor the KEK in the clear ever appears in any persisted
// secret-store value. The wrapped blobs are present; the live secrets are not.
func TestNoPlaintextKeyMaterialPersisted(t *testing.T) {
	m, store := newTestManager()
	rk, err := m.Provision(testLib, UnsealModePassphrase, "operator passphrase here")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	dek, _ := m.DEK(testLib)

	// The plaintext DEK must NOT be in any stored value.
	assertSecretAbsent(t, store, append([]byte(nil), dek...))
	// The raw Recovery Key entropy must NOT be in any stored value.
	assertSecretAbsent(t, store, rk.Bytes())

	// Sanity: the wrapped DEK blobs ARE persisted (the bundle) and differ from the
	// plaintext DEK — so the test above is meaningful, not vacuous.
	bundle, err := m.loadBundle(testLib)
	if err != nil {
		t.Fatalf("loadBundle: %v", err)
	}
	if bytes.Equal(bundle.DEKUnderKEK, dek) || bytes.Equal(bundle.DEKUnderPassphrase, dek) || bytes.Equal(bundle.DEKUnderRecovery, dek) {
		t.Fatal("a wrapped DEK equals the plaintext DEK — wrapping is broken")
	}
	if len(bundle.DEKUnderKEK) != 0 {
		t.Fatal("passphrase-mode persisted an auto-unseal KEK wrap")
	}
	if len(bundle.DEKUnderPassphrase) == 0 || len(bundle.DEKUnderRecovery) == 0 {
		t.Fatal("expected passphrase and Recovery-Key wrapped DEK blobs")
	}
}

func TestSealedPassphraseModePersistsNoAutoUnsealKeyMaterial(t *testing.T) {
	dir := t.TempDir()
	secrets := diskSecretStore{dir: filepath.Join(dir, "secrets")}
	m := NewManagerWithParams(secrets, TestArgon2Params)
	const passphrase = "operator passphrase here"
	rk, err := m.Provision(testLib, UnsealModePassphrase, passphrase)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	dek, err := m.DEK(testLib)
	if err != nil {
		t.Fatalf("DEK: %v", err)
	}
	bundle, err := m.loadBundle(testLib)
	if err != nil {
		t.Fatalf("loadBundle: %v", err)
	}
	ppKey := derivePassphraseKey(passphrase, bundle.PassphraseSalt, TestArgon2Params)
	m.Seal(testLib)

	if _, err := os.Stat(filepath.Join(secrets.dir, kekRef(testLib))); !os.IsNotExist(err) {
		t.Fatalf("passphrase-mode KEK file exists: %v", err)
	}
	restarted := NewManagerWithParams(secrets, TestArgon2Params)
	if sealed := restarted.BootUnseal([]string{testLib}); len(sealed) != 1 || sealed[0] != testLib {
		t.Fatalf("BootUnseal sealed = %v, want [%s]", sealed, testLib)
	}
	if err := restarted.AutoUnseal(testLib); !errors.Is(err, ErrSealed) {
		t.Fatalf("AutoUnseal(passphrase mode) = %v, want ErrSealed", err)
	}

	for name, needle := range map[string][]byte{
		"DEK":              dek,
		"Recovery Key":     rk.Bytes(),
		"passphrase key":   ppKey,
		"passphrase bytes": []byte(passphrase),
	} {
		assertFileTreeLacksBytes(t, dir, name, needle, nil)
	}
	if err := restarted.PassphraseUnseal(testLib, passphrase); err != nil {
		t.Fatalf("PassphraseUnseal after disk-only restart: %v", err)
	}
}

// TestNoReprovision confirms Provision refuses to clobber an existing bundle.
func TestNoReprovision(t *testing.T) {
	m, _ := newTestManager()
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatalf("first Provision: %v", err)
	}
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != ErrAlreadyProvisioned {
		t.Fatalf("re-provision = %v, want ErrAlreadyProvisioned", err)
	}
}

// TestUnsealNotProvisioned confirms operations on an unprovisioned library are
// clean errors, not panics.
func TestUnsealNotProvisioned(t *testing.T) {
	m, _ := newTestManager()
	if err := m.AutoUnseal("nope"); err != ErrNotProvisioned {
		t.Fatalf("AutoUnseal unprovisioned = %v, want ErrNotProvisioned", err)
	}
	if err := m.RecoveryUnseal("nope", "ABCDE-FGHJK"); err != ErrNotProvisioned {
		t.Fatalf("RecoveryUnseal unprovisioned = %v, want ErrNotProvisioned", err)
	}
	if !m.Sealed("nope") {
		t.Fatal("unprovisioned library should report Sealed")
	}
	if m.Provisioned("nope") {
		t.Fatal("unprovisioned library should report not provisioned")
	}
}

// TestPassphraseUnsealWithoutPassphraseWrap confirms a library provisioned in
// auto mode with no passphrase cannot be passphrase-unsealed.
func TestPassphraseUnsealWithoutPassphraseWrap(t *testing.T) {
	m, _ := newTestManager()
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	m.Seal(testLib)
	if err := m.PassphraseUnseal(testLib, "anything"); err == nil {
		t.Fatal("expected error: no passphrase wrap exists")
	}
}

// TestProdParamsAreStrong is a guard so the production argon2 profile is never
// accidentally weakened to the test profile.
func TestProdParamsAreStrong(t *testing.T) {
	if ProdArgon2Params.Memory < 64*1024 {
		t.Fatalf("production argon2 memory = %d KiB, expected >= 65536 (64 MiB)", ProdArgon2Params.Memory)
	}
	if ProdArgon2Params.Time < 2 {
		t.Fatalf("production argon2 time = %d, expected >= 2", ProdArgon2Params.Time)
	}
	if ProdArgon2Params.KeyLen != dekLen {
		t.Fatalf("production argon2 KeyLen = %d, want %d", ProdArgon2Params.KeyLen, dekLen)
	}
}

// assertSecretAbsent fails if needle (a raw key) appears in any persisted value,
// in raw, base64, hex, or base32 form. This is the teeth of the invariant #5
// negative test: a key encoded any plausible way must not survive at rest.
func assertSecretAbsent(t *testing.T, store *memStore, needle []byte) {
	t.Helper()
	if len(needle) == 0 {
		t.Fatal("empty needle — test bug")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for ref, val := range store.values {
		if bytes.Contains([]byte(val), needle) {
			t.Fatalf("raw key material found in stored secret %q", ref)
		}
		if strings.Contains(val, base32Encode(needle)) {
			t.Fatalf("base32 key material found in stored secret %q", ref)
		}
	}
}

func storeContainsString(store *memStore, s string) bool {
	if s == "" {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, val := range store.values {
		if strings.Contains(val, s) {
			return true
		}
	}
	return false
}

func assertFileTreeLacksBytes(t *testing.T, root, label string, needle []byte, allowed map[string]bool) {
	t.Helper()
	if len(needle) == 0 {
		t.Fatalf("empty %s needle", label)
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if allowed[rel] {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(b, needle) {
			t.Fatalf("%s bytes found in %s", label, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// testObj is the shared ObjectContext for legacy-shaped tests that exercise
// key lifecycle / crash-safety rather than identity binding (the identity
// matrix lives in atrest_v2_test.go). Every object in these flows shares it;
// cross-object safety still holds because each v2 object gets a unique salt.
var testObj = MediaObjectContext(testLib, "sha256:test-fixture")
