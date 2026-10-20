package libencrypt

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// rewrapReplay drives the re-wrap protocol one durable step at a time so a test
// can stop at any phase and simulate a crash by restarting the Manager. Each
// method is one persisted step in the same order Rewrap performs them.
type rewrapReplay struct {
	t         *testing.T
	m         *Manager
	store     *memStore
	staged    []stagedRewrapFile
	oldKEK    string
	oldBundle keyBundle
	newKEK    []byte
	newBundle keyBundle
}

func (r *rewrapReplay) journal() {
	if err := r.m.storeRewrapJournal(testLib, r.oldKEK, r.oldBundle, r.staged); err != nil {
		r.t.Fatalf("storeRewrapJournal: %v", err)
	}
}

func (r *rewrapReplay) swapOne(i int) {
	if err := os.Rename(r.staged[i].dst, r.staged[i].tmp+".old"); err != nil {
		r.t.Fatal(err)
	}
	if err := os.Rename(r.staged[i].tmp, r.staged[i].dst); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rewrapReplay) swapAll() {
	if _, err := swapStagedRewrapFiles(r.staged); err != nil {
		r.t.Fatalf("swapStagedRewrapFiles: %v", err)
	}
}

func (r *rewrapReplay) commitKeys() {
	if err := r.m.writeOwnerSecret(testLib, secretKindKEK, base64.StdEncoding.EncodeToString(r.newKEK)); err != nil {
		r.t.Fatalf("persist new KEK: %v", err)
	}
	if err := r.m.storeBundle(testLib, r.newBundle); err != nil {
		r.t.Fatalf("persist new bundle: %v", err)
	}
}

func (r *rewrapReplay) commitRecord() {
	if err := r.m.commitRewrapJournal(testLib, r.oldKEK, r.oldBundle, r.staged); err != nil {
		r.t.Fatalf("commitRewrapJournal: %v", err)
	}
}

func (r *rewrapReplay) clearJournal() {
	if err := r.m.clearRewrapJournal(testLib); err != nil {
		r.t.Fatalf("clearRewrapJournal: %v", err)
	}
}

func (r *rewrapReplay) removeBackups() {
	for _, f := range r.staged {
		_ = os.Remove(f.tmp + ".old")
	}
}

// rewrapKillPhases is the shared per-journal-phase crash table used by both
// the v2→v2 and the mixed v1→v2 kill matrices.
var rewrapKillPhases = []struct {
	name          string
	wantCommitted bool
	drive         func(r *rewrapReplay)
}{
	{"staged_no_journal", false, func(r *rewrapReplay) {}},
	{"journal_uncommitted", false, func(r *rewrapReplay) { r.journal() }},
	{"partial_swap", false, func(r *rewrapReplay) { r.journal(); r.swapOne(0) }},
	{"full_swap_no_keys", false, func(r *rewrapReplay) { r.journal(); r.swapAll() }},
	{"keys_committed_journal_uncommitted", false, func(r *rewrapReplay) {
		r.journal()
		r.swapAll()
		r.commitKeys()
	}},
	{"committed_backups_present", true, func(r *rewrapReplay) {
		r.journal()
		r.swapAll()
		r.commitKeys()
		r.commitRecord()
	}},
	// The historical data-loss window: journal survived a crash while the
	// rollback backups were already gone. It must now roll FORWARD, not
	// restore old keys over new ciphertext.
	{"committed_backups_removed", true, func(r *rewrapReplay) {
		r.journal()
		r.swapAll()
		r.commitKeys()
		r.commitRecord()
		r.removeBackups()
	}},
	{"committed_cleared", true, func(r *rewrapReplay) {
		r.journal()
		r.swapAll()
		r.commitKeys()
		r.commitRecord()
		r.clearJournal()
		r.removeBackups()
	}},
}

// TestRewrapKillMatrixRecoversExactlyOneKey stops the re-wrap at every phase,
// simulates a crash (fresh Manager), and asserts the ADR-0117 §2 invariant:
// recovery yields exactly one valid key, the old Recovery Key is rejected only
// after the durable commit record, and no phase loses data.
func TestRewrapKillMatrixRecoversExactlyOneKey(t *testing.T) {
	for _, ph := range rewrapKillPhases {
		t.Run(ph.name, func(t *testing.T) {
			m, store := newTestManager()
			oldRK, paths := provisionEncryptedFiles(t, m, 2)
			oldKEK, oldBundle, staged, newKEK, newBundle, newRK := stageTestRewrap(t, m, paths)
			ph.drive(&rewrapReplay{
				t: t, m: m, store: store, staged: staged,
				oldKEK: oldKEK, oldBundle: oldBundle, newKEK: newKEK, newBundle: newBundle,
			})

			valid, invalid := oldRK, newRK
			if ph.wantCommitted {
				valid, invalid = newRK, oldRK
			}

			// Restart: recovery resolves the interrupted re-wrap on unseal.
			restarted := NewManagerWithParams(store, TestArgon2Params)
			if err := restarted.RecoveryUnseal(testLib, valid.Display); err != nil {
				t.Fatalf("the surviving key must unseal after recovery: %v", err)
			}
			assertEncryptedPayloads(t, restarted, paths) // no data-loss window

			// Exactly one valid key: the other must be rejected.
			restarted.Seal(testLib)
			if err := restarted.RecoveryUnseal(testLib, invalid.Display); err == nil {
				t.Fatal("the other Recovery Key must be rejected (exactly one valid key)")
			}
		})
	}
}

// readMagic returns a file's four magic bytes.
func readMagic(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test-owned temp path
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil {
		t.Fatal(err)
	}
	return string(magic)
}

// TestRewrapKillMatrixV1ToV2 is the mixed-generation matrix:
// a legacy v1 library rewraps to a fresh v2-only DEK,
// killed at every journal phase. Rollback must yield the byte-identical v1
// objects readable ONLY under the old key in the restored v1 generation;
// roll-forward must yield context-bound v2 objects readable ONLY under the new
// key, with restored v1 permanently rejected. No plaintext, temp file, or
// version ambiguity may survive either way.
func TestRewrapKillMatrixV1ToV2(t *testing.T) {
	for _, ph := range rewrapKillPhases {
		t.Run(ph.name, func(t *testing.T) {
			m, store := newTestManager()
			oldRK, paths := provisionEncryptedFiles(t, m, 2)
			// Rewind the fresh provision to a legacy v1 generation: v1 format
			// commitment + v1 object bytes under the same DEK.
			forceV1Generation(t, m, testLib)
			dek, err := m.DEK(testLib)
			if err != nil {
				t.Fatal(err)
			}
			originalV1 := map[string][]byte{}
			for i, path := range paths {
				var v1 bytes.Buffer
				if err := EncryptStream(&v1, bytes.NewReader([]byte(fmt.Sprintf("payload-%d", i))), dek); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, v1.Bytes(), 0o600); err != nil {
					t.Fatal(err)
				}
				originalV1[path] = v1.Bytes()
			}

			oldKEK, oldBundle, staged, newKEK, newBundle, newRK := stageTestRewrap(t, m, paths)
			ph.drive(&rewrapReplay{
				t: t, m: m, store: store, staged: staged,
				oldKEK: oldKEK, oldBundle: oldBundle, newKEK: newKEK, newBundle: newBundle,
			})

			valid, invalid := oldRK, newRK
			if ph.wantCommitted {
				valid, invalid = newRK, oldRK
			}

			restarted := NewManagerWithParams(store, TestArgon2Params)
			if err := restarted.RecoveryUnseal(testLib, valid.Display); err != nil {
				t.Fatalf("the surviving key must unseal after recovery: %v", err)
			}
			// No data loss, and no version ambiguity: every object decrypts
			// through the recovered generation's committed format.
			assertEncryptedPayloads(t, restarted, paths)
			format, err := restarted.atRestFormat(testLib)
			if err != nil {
				t.Fatal(err)
			}
			if ph.wantCommitted {
				// Roll-forward: v2-only generation, every file is context-bound v2.
				if format != atRestFormatV2 {
					t.Fatalf("recovered format = %d, want v2 after roll-forward", format)
				}
				for _, path := range paths {
					if got := readMagic(t, path); got != atRestV2Magic {
						t.Fatalf("roll-forward left %s with magic %q, want BFE2", path, got)
					}
				}
				// Restored v1 ciphertext is permanently rejected in the new
				// generation (committed-v2 → v1 replay).
				replayPath := paths[0] + ".replay"
				if err := os.WriteFile(replayPath, originalV1[paths[0]], 0o600); err != nil {
					t.Fatal(err)
				}
				if err := restarted.DecryptFile(testLib, replayPath, replayPath+".out", testObj); err == nil {
					t.Fatal("restored v1 object decrypted in a committed v2 generation")
				}
			} else {
				// Rollback: byte-identical v1 under the old key only.
				if format != atRestFormatV1 {
					t.Fatalf("recovered format = %d, want v1 after rollback", format)
				}
				for _, path := range paths {
					got, err := os.ReadFile(path) //nolint:gosec // test-owned temp path
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, originalV1[path]) {
						t.Fatalf("rollback did not restore %s byte-identically", path)
					}
				}
			}
			// No plaintext or temp residue in the object directory once a
			// journal existed (recovery owns temps only after the journal
			// records them; pre-journal staged temps are opaque ciphertext the
			// failing Rewrap call itself cleans up).
			if ph.name != "staged_no_journal" {
				residue, err := filepath.Glob(filepath.Join(filepath.Dir(paths[0]), ".libenc-*"))
				if err != nil {
					t.Fatal(err)
				}
				if len(residue) != 0 {
					t.Fatalf("temp residue survived recovery: %v", residue)
				}
			}

			// Exactly one valid key.
			restarted.Seal(testLib)
			if err := restarted.RecoveryUnseal(testLib, invalid.Display); err == nil {
				t.Fatal("the other Recovery Key must be rejected (exactly one valid key)")
			}
		})
	}
}

// TestRewrapKillMatrixRenameEIO injects EIO on the first rename of the second
// staged file. The first file has already been fully swapped at that point, so
// the error path must roll it back before the caller clears the uncommitted
// journal. This is the in-process equivalent of a kill between swap steps.
func TestRewrapKillMatrixRenameEIO(t *testing.T) {
	m, store := newTestManager()
	oldRK, paths := provisionEncryptedFiles(t, m, 2)
	original := make(map[string][]byte, len(paths))
	for i, path := range paths {
		ciphertext, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		original[path] = ciphertext
		if err := os.Remove(filepath.Join(filepath.Dir(path), fmt.Sprintf("plain-%d.bin", i))); err != nil {
			t.Fatal(err)
		}
	}
	newRK, err := generateRecoveryKey()
	if err != nil {
		t.Fatalf("generateRecoveryKey: %v", err)
	}
	originalGenerate := generateRewrapRecoveryKey
	t.Cleanup(func() { generateRewrapRecoveryKey = originalGenerate })
	generateRewrapRecoveryKey = func() (RecoveryKey, error) { return newRK, nil }

	originalRename := renameRewrapFile
	t.Cleanup(func() { renameRewrapFile = originalRename })
	renameCalls := 0
	renameRewrapFile = func(oldPath, newPath string) error {
		renameCalls++
		if renameCalls == 3 {
			return syscall.EIO
		}
		return os.Rename(oldPath, newPath)
	}
	_, err = m.Rewrap(testLib, UnsealModeAuto, "", func(c RewrapCipher) error {
		for _, path := range paths {
			if err := c.RewrapFile(path, testObj); err != nil {
				return err
			}
		}
		return nil
	})
	renameRewrapFile = originalRename
	generateRewrapRecoveryKey = originalGenerate
	if !errors.Is(err, syscall.EIO) {
		t.Fatalf("Rewrap error = %v, want EIO", err)
	}
	if renameCalls != 3 {
		t.Fatalf("rename calls = %d, want 3", renameCalls)
	}

	// Rewrap's real failure path must restore every destination, remove unused
	// staged files, and clear the uncommitted journal before it returns.
	for _, path := range paths {
		ciphertext, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(ciphertext, original[path]) {
			t.Fatalf("ciphertext %s was not restored byte-for-byte", path)
		}
	}
	residue, err := filepath.Glob(filepath.Join(filepath.Dir(paths[0]), ".libenc-*.tmp*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(residue) != 0 {
		t.Fatalf("partial rewrap files survived EIO recovery: %v", residue)
	}
	entries, err := os.ReadDir(filepath.Dir(paths[0]))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(paths) {
		t.Fatalf("unexpected plaintext or partial files survived EIO recovery: %v", entries)
	}

	restarted := NewManagerWithParams(store, TestArgon2Params)
	if err := restarted.RecoveryUnseal(testLib, oldRK.Display); err != nil {
		t.Fatalf("old Recovery Key must survive EIO recovery: %v", err)
	}
	assertEncryptedPayloads(t, restarted, paths)
	restarted.Seal(testLib)
	if err := restarted.RecoveryUnseal(testLib, newRK.Display); err == nil {
		t.Fatal("new Recovery Key became valid after failed swap")
	}
}

// TestRewrapStagingSpaceCheckAbortsCleanly forces the free-space seam to report
// a shortfall and asserts the re-wrap aborts before writing any journal, leaks no
// staged temp, and leaves the old key and ciphertext intact (explicit space
// accounting).
func TestRewrapStagingSpaceCheckAbortsCleanly(t *testing.T) {
	m, store := newTestManager()
	oldRK, paths := provisionEncryptedFiles(t, m, 1)

	orig := freeBytesFn
	freeBytesFn = func(string) (uint64, bool, error) { return 0, true, nil }
	defer func() { freeBytesFn = orig }()

	_, err := m.Rewrap(testLib, UnsealModeAuto, "", func(c RewrapCipher) error {
		return c.RewrapFile(paths[0], testObj)
	})
	if !errors.Is(err, ErrRewrapInsufficientSpace) {
		t.Fatalf("Rewrap error = %v, want ErrRewrapInsufficientSpace", err)
	}
	if store.wrote(rewrapJournalRef(testLib)) {
		t.Fatal("rewrap journal written despite the space abort")
	}
	temps, err := filepath.Glob(filepath.Join(filepath.Dir(paths[0]), ".libenc-*.tmp*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 0 {
		t.Fatalf("staged temps leaked after space abort: %v", temps)
	}

	m.Seal(testLib)
	if err := m.RecoveryUnseal(testLib, oldRK.Display); err != nil {
		t.Fatalf("old Recovery Key must remain valid after space abort: %v", err)
	}
	assertEncryptedPayloads(t, m, paths)
}
