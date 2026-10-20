package libencrypt

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestSealWaitsForOpenDecryptReaderClose(t *testing.T) {
	m, _ := newTestManager()
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain")
	ciphertext := filepath.Join(dir, "cipher")
	if err := os.WriteFile(plain, []byte("lease-bound plaintext"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.EncryptFile(testLib, plain, ciphertext, testObj); err != nil {
		t.Fatal(err)
	}
	reader, err := m.OpenDecryptReader(testLib, ciphertext, testObj)
	if err != nil {
		t.Fatal(err)
	}
	sealStarted := make(chan struct{})
	sealDone := make(chan struct{})
	go func() {
		close(sealStarted)
		m.Seal(testLib)
		close(sealDone)
	}()
	<-sealStarted
	select {
	case <-sealDone:
		t.Fatal("Seal escaped the open reader's ownership lease")
	case <-time.After(25 * time.Millisecond):
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil { // repeated Close must close/unlock exactly once.
		t.Fatalf("second Close = %v", err)
	}
	select {
	case <-sealDone:
	case <-time.After(time.Second):
		t.Fatal("Seal did not resume after reader Close")
	}
	if !m.Sealed(testLib) {
		t.Fatal("library remained unsealed after reader Close")
	}
	if err := m.AutoUnseal(testLib); err != nil {
		t.Fatal(err)
	}
	reader, err = m.OpenDecryptReader(testLib, ciphertext, testObj)
	if err != nil {
		t.Fatal(err)
	}
	rewrapStarted := make(chan struct{})
	rewrapDone := make(chan error, 1)
	go func() {
		close(rewrapStarted)
		_, err := m.Rewrap(testLib, UnsealModeAuto, "", func(RewrapCipher) error { return nil })
		rewrapDone <- err
	}()
	<-rewrapStarted
	select {
	case err := <-rewrapDone:
		t.Fatalf("Rewrap escaped the open reader's ownership lease: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-rewrapDone; err != nil {
		t.Fatal(err)
	}
	if _, err := m.OpenDecryptReader(testLib, filepath.Join(dir, "missing"), testObj); err == nil {
		t.Fatal("missing ciphertext opened")
	}
	failedOpenSealDone := make(chan struct{})
	go func() {
		m.Seal(testLib)
		close(failedOpenSealDone)
	}()
	select {
	case <-failedOpenSealDone:
	case <-time.After(time.Second):
		t.Fatal("failed OpenDecryptReader retained the ownership lease")
	}
}

func TestLeakedDecryptReaderDoesNotBlockSealPastDeadline(t *testing.T) {
	m, _ := newTestManager()
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain")
	ciphertext := filepath.Join(dir, "cipher")
	if err := os.WriteFile(plain, []byte("abandoned reader plaintext"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.EncryptFile(testLib, plain, ciphertext, testObj); err != nil {
		t.Fatal(err)
	}
	func() {
		reader, err := m.OpenDecryptReader(testLib, ciphertext, testObj)
		if err != nil {
			t.Fatal(err)
		}
		runtime.KeepAlive(reader)
	}()

	sealDone := make(chan struct{})
	go func() {
		m.Seal(testLib)
		close(sealDone)
	}()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	collect := time.NewTicker(10 * time.Millisecond)
	defer collect.Stop()
	for {
		select {
		case <-sealDone:
			if !m.Sealed(testLib) {
				t.Fatal("Seal returned without sealing the library")
			}
			return
		case <-collect.C:
			runtime.GC()
		case <-deadline.C:
			t.Fatal("Seal remained blocked past the leaked-reader deadline")
		}
	}
}

func TestProvisionPreparedTailFailuresRollback(t *testing.T) {
	tests := []struct {
		name      string
		failRef   func(string) string
		failSetAt int
	}{
		{"prepare", nil, 0},
		{"kek", kekRef, 1},
		{"bundle", bundleRef, 1},
		{"commit-journal", provisionJournalRef, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, secrets := newTestManager()
			boom := errors.New("tail failure")
			if tc.failRef != nil {
				secrets.failSetRef = tc.failRef(testLib)
				secrets.failSetAt = tc.failSetAt
				secrets.failSetErr = boom
			}
			var prepared, rolledBack atomic.Int32
			_, err := m.ProvisionPrepared(testLib, UnsealModeAuto, "", ProvisionHooks{
				Prepare: func(OperationCipher) error {
					prepared.Add(1)
					if tc.name == "prepare" {
						return boom
					}
					return nil
				},
				Rollback: func() error {
					rolledBack.Add(1)
					return nil
				},
			})
			if !errors.Is(err, boom) {
				t.Fatalf("error = %v, want injected failure", err)
			}
			if prepared.Load() != 1 || rolledBack.Load() != 1 {
				t.Fatalf("prepare/rollback = %d/%d", prepared.Load(), rolledBack.Load())
			}
			secrets.failSetRef = ""
			if err := m.RecoverProvisionJournals([]string{testLib}); err != nil {
				t.Fatal(err)
			}
			if m.Provisioned(testLib) || !m.Sealed(testLib) {
				t.Fatal("failed provision published a key generation")
			}
			for _, ref := range []string{kekRef(testLib), bundleRef(testLib), provisionJournalRef(testLib)} {
				if _, ok := secrets.values[ref]; ok {
					t.Fatalf("failed provision left %s", ref)
				}
			}
		})
	}
}

func TestOwnershipLeaseSerializesEveryPublicLifecycleAndObjectOperation(t *testing.T) {
	tests := []struct {
		name string
		run  func(*Manager, string)
	}{
		{"provision", func(m *Manager, id string) { _, _ = m.Provision(id, UnsealModeAuto, "") }},
		{"rewrap", func(m *Manager, id string) {
			_, _ = m.Rewrap(id, UnsealModeAuto, "", func(RewrapCipher) error { return nil })
		}},
		{"auto-unseal", func(m *Manager, id string) { _ = m.AutoUnseal(id) }},
		{"passphrase-unseal", func(m *Manager, id string) { _ = m.PassphraseUnseal(id, "pw") }},
		{"recovery-unseal", func(m *Manager, id string) { _ = m.RecoveryUnseal(id, "invalid") }},
		{"seal", func(m *Manager, id string) { m.Seal(id) }},
		{"sealed", func(m *Manager, id string) { _ = m.Sealed(id) }},
		{"dek", func(m *Manager, id string) { _, _ = m.DEK(id) }},
		{"provisioned", func(m *Manager, id string) { _ = m.Provisioned(id) }},
		{"mode", func(m *Manager, id string) { _, _ = m.Mode(id) }},
		{"key-locus", func(m *Manager, id string) { _ = m.KeyLocus(id) }},
		{"encrypt-file", func(m *Manager, id string) {
			dir := t.TempDir()
			plain := filepath.Join(dir, "plain")
			_ = os.WriteFile(plain, []byte("x"), 0o600)
			_ = m.EncryptFile(id, plain, filepath.Join(dir, "cipher"), testObj)
		}},
		{"decrypt-file", func(m *Manager, id string) {
			dir := t.TempDir()
			_ = m.DecryptFile(id, filepath.Join(dir, "cipher"), filepath.Join(dir, "plain"), testObj)
		}},
		{"open-reader", func(m *Manager, id string) {
			r, _ := m.OpenDecryptReader(id, filepath.Join(t.TempDir(), "cipher"), testObj)
			if r != nil {
				_ = r.Close()
			}
		}},
		{"unseal-db", func(m *Manager, id string) {
			dir := t.TempDir()
			_ = m.UnsealDBFile(id, filepath.Join(dir, "cipher"), filepath.Join(dir, "work"), LibraryDBObjectContext(id))
		}},
		{"seal-db", func(m *Manager, id string) {
			dir := t.TempDir()
			work := filepath.Join(dir, "work")
			_ = os.WriteFile(work, []byte("db"), 0o600)
			_ = m.SealDBFile(id, work, filepath.Join(dir, "cipher"), LibraryDBObjectContext(id))
		}},
		{"boot-unseal", func(m *Manager, id string) { _ = m.BootUnseal([]string{id}) }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestManager()
			entered := make(chan struct{})
			releaseTail := make(chan struct{})
			provisionDone := make(chan error, 1)
			go func() {
				_, err := m.ProvisionPrepared(testLib, UnsealModeAuto, "", ProvisionHooks{Prepare: func(OperationCipher) error {
					close(entered)
					<-releaseTail
					return nil
				}})
				provisionDone <- err
			}()
			<-entered
			started := make(chan struct{})
			done := make(chan struct{})
			go func() {
				close(started)
				tc.run(m, testLib)
				close(done)
			}()
			<-started
			select {
			case <-done:
				t.Fatal("operation escaped ownership lease")
			default:
			}
			close(releaseTail)
			if err := <-provisionDone; err != nil {
				t.Fatal(err)
			}
			<-done
		})
	}
}

func TestRecoverProvisionJournalCrashPhases(t *testing.T) {
	t.Run("uncommitted rolls back", func(t *testing.T) {
		m, secrets := newTestManager()
		dek, _ := newDEK()
		kek, _ := newKEK()
		rk, _ := generateRecoveryKey()
		bundle, err := m.newBundle(UnsealModeAuto, "", dek, kek, rk)
		if err != nil {
			t.Fatal(err)
		}
		kekValue := "candidate"
		if err := m.writeProvisionJournal(testLib, kekValue, bundle, false); err != nil {
			t.Fatal(err)
		}
		if err := m.writeOwnerSecret(testLib, secretKindKEK, kekValue); err != nil {
			t.Fatal(err)
		}
		if err := m.storeBundle(testLib, bundle); err != nil {
			t.Fatal(err)
		}
		restarted := NewManagerWithParams(secrets, TestArgon2Params)
		var compensated atomic.Int32
		if err := restarted.RecoverProvisionJournals([]string{testLib}, func(owner string) error {
			if owner != testLib {
				t.Fatalf("compensated owner = %q", owner)
			}
			compensated.Add(1)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if compensated.Load() != 1 {
			t.Fatal("uncommitted crash state did not compensate staged store work")
		}
		if restarted.Provisioned(testLib) {
			t.Fatal("uncommitted crash state rolled forward")
		}
	})

	t.Run("failed compensation preserves journal", func(t *testing.T) {
		m, secrets := newTestManager()
		dek, _ := newDEK()
		kek, _ := newKEK()
		rk, _ := generateRecoveryKey()
		bundle, err := m.newBundle(UnsealModeAuto, "", dek, kek, rk)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.writeProvisionJournal(testLib, "candidate", bundle, false); err != nil {
			t.Fatal(err)
		}
		boom := errors.New("compensation failed")
		restarted := NewManagerWithParams(secrets, TestArgon2Params)
		if err := restarted.RecoverProvisionJournals([]string{testLib}, func(string) error { return boom }); !errors.Is(err, boom) {
			t.Fatalf("recovery error = %v", err)
		}
		if exists, err := restarted.ownerSecretExists(testLib, secretKindProvisionJournal); err != nil || !exists {
			t.Fatalf("journal existence = %v, %v", exists, err)
		}
	})

	t.Run("committed rolls forward", func(t *testing.T) {
		m, secrets := newTestManager()
		rk, err := m.Provision(testLib, UnsealModeAuto, "")
		if err != nil || rk.Display == "" {
			t.Fatal(err)
		}
		bundle, err := m.loadBundle(testLib)
		if err != nil {
			t.Fatal(err)
		}
		kek, err := m.readOwnerSecret(testLib, secretKindKEK)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.writeProvisionJournal(testLib, kek, bundle, true); err != nil {
			t.Fatal(err)
		}
		restarted := NewManagerWithParams(secrets, TestArgon2Params)
		if err := restarted.RecoverProvisionJournals([]string{testLib}); err != nil {
			t.Fatal(err)
		}
		if !restarted.Provisioned(testLib) {
			t.Fatal("committed crash state rolled back")
		}
	})
}

func TestOwnershipLeaseCoversProvisionGraphTail(t *testing.T) {
	m, _ := newTestManager()
	entered := make(chan struct{})
	releaseTail := make(chan struct{})
	provisionDone := make(chan error, 1)
	go func() {
		_, err := m.ProvisionPrepared(testLib, UnsealModeAuto, "", ProvisionHooks{
			Prepare: func(OperationCipher) error {
				close(entered)
				<-releaseTail
				return nil
			},
		})
		provisionDone <- err
	}()
	<-entered
	started := make(chan struct{})
	queryDone := make(chan bool, 1)
	go func() {
		close(started)
		queryDone <- m.Provisioned(testLib)
	}()
	<-started
	select {
	case <-queryDone:
		t.Fatal("object/lifecycle query escaped the owner lease")
	default:
	}
	close(releaseTail)
	if err := <-provisionDone; err != nil {
		t.Fatal(err)
	}
	if !<-queryDone {
		t.Fatal("query after commit did not observe provisioned generation")
	}
}

func TestRewrapPreparedTailFailureRestoresOldLifecycle(t *testing.T) {
	m, _ := newTestManager()
	oldRK, err := m.Provision(testLib, UnsealModeAuto, "")
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("audit tail failed")
	var prepared, restored atomic.Int32
	_, err = m.RewrapPrepared(
		testLib,
		UnsealModeAuto,
		"",
		func(OperationCipher) error { prepared.Add(1); return nil },
		func(OperationCipher) error { restored.Add(1); return nil },
		func(RewrapCipher) error { return nil },
		func() error { return boom },
		true,
	)
	if !errors.Is(err, boom) || prepared.Load() != 1 || restored.Load() != 1 {
		t.Fatalf("error/prepared/restored = %v/%d/%d", err, prepared.Load(), restored.Load())
	}
	m.Seal(testLib)
	if err := m.RecoveryUnseal(testLib, oldRK.Display); err != nil {
		t.Fatalf("old recovery key did not survive tail failure: %v", err)
	}
}

func TestRewrapPreparedPartialPrepareFailureRestoresOldLifecycle(t *testing.T) {
	m, _ := newTestManager()
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("partial store seal failed")
	var restored atomic.Int32
	_, err := m.RewrapPrepared(
		testLib,
		UnsealModeAuto,
		"",
		func(OperationCipher) error { return boom },
		func(OperationCipher) error { restored.Add(1); return nil },
		func(RewrapCipher) error { t.Fatal("rewrite ran after preparation failure"); return nil },
		nil,
		true,
	)
	if !errors.Is(err, boom) || restored.Load() != 1 {
		t.Fatalf("error/restored = %v/%d", err, restored.Load())
	}
}

// TestProvisionRewrapThenRecoverBoots reproduces the boot refusal: a committed
// provision journal that a later Rewrap superseded must be treated as stale
// residue, not as a KEK/bundle mismatch that refuses startup.
func TestProvisionRewrapThenRecoverBoots(t *testing.T) {
	m, secrets := newTestManager()
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := secrets.values[provisionJournalRef(testLib)]; ok {
		t.Fatal("committed provision retained its journal (second at-rest KEK copy)")
	}
	// Simulate a crash between the durable commit record and its removal.
	bundle, err := m.loadBundle(testLib)
	if err != nil {
		t.Fatal(err)
	}
	kek, err := m.readOwnerSecret(testLib, secretKindKEK)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.writeProvisionJournal(testLib, kek, bundle, true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Rewrap(testLib, UnsealModeAuto, "", func(RewrapCipher) error { return nil }); err != nil {
		t.Fatal(err)
	}

	restarted := NewManagerWithParams(secrets, TestArgon2Params)
	if err := restarted.RecoverProvisionJournals([]string{testLib}); err != nil {
		t.Fatalf("boot recovery refused a superseded provision journal: %v", err)
	}
	if _, ok := secrets.values[provisionJournalRef(testLib)]; ok {
		t.Fatal("superseded provision journal survived recovery")
	}
	if err := restarted.AutoUnseal(testLib); err != nil {
		t.Fatalf("boot unseal after recovery: %v", err)
	}
}

// TestRecoverProvisionJournalRollsForwardMissingGeneration keeps the repair half
// of recovery able to fail: a committed journal whose generation never reached
// the secure store is still rolled forward.
func TestRecoverProvisionJournalRollsForwardMissingGeneration(t *testing.T) {
	m, secrets := newTestManager()
	dek, _ := newDEK()
	kek, _ := newKEK()
	rk, _ := generateRecoveryKey()
	bundle, err := m.newBundle(UnsealModeAuto, "", dek, kek, rk)
	if err != nil {
		t.Fatal(err)
	}
	kekValue := base64.StdEncoding.EncodeToString(kek)
	if err := m.writeProvisionJournal(testLib, kekValue, bundle, true); err != nil {
		t.Fatal(err)
	}
	restarted := NewManagerWithParams(secrets, TestArgon2Params)
	if err := restarted.RecoverProvisionJournals([]string{testLib}); err != nil {
		t.Fatal(err)
	}
	if !restarted.Provisioned(testLib) {
		t.Fatal("half-committed provision was not rolled forward")
	}
	if err := restarted.AutoUnseal(testLib); err != nil {
		t.Fatalf("rolled-forward generation does not unseal: %v", err)
	}
	got, err := restarted.DEK(testLib)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, dek) {
		t.Fatal("rolled-forward generation unsealed a different DEK")
	}
}

// TestConcurrentOpenDecryptReadersDoNotSerialize pins the reader-lease redesign:
// a stalled stream must not hold the library against a second reader.
func TestConcurrentOpenDecryptReadersDoNotSerialize(t *testing.T) {
	m, _ := newTestManager()
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain")
	ciphertext := filepath.Join(dir, "cipher")
	if err := os.WriteFile(plain, []byte("concurrent reader plaintext"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.EncryptFile(testLib, plain, ciphertext, testObj); err != nil {
		t.Fatal(err)
	}
	stalled, err := m.OpenDecryptReader(testLib, ciphertext, testObj)
	if err != nil {
		t.Fatal(err)
	}
	second := make(chan error, 1)
	go func() {
		reader, err := m.OpenDecryptReader(testLib, ciphertext, testObj)
		if err != nil {
			second <- err
			return
		}
		if _, err := io.ReadAll(reader); err != nil {
			second <- errors.Join(err, reader.Close())
			return
		}
		second <- reader.Close()
	}()
	select {
	case err := <-second:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second reader waited on the first reader's open stream")
	}
	// A status query must not queue behind the stalled stream either.
	statusDone := make(chan bool, 1)
	go func() { statusDone <- m.Sealed(testLib) }()
	select {
	case <-statusDone:
	case <-time.After(2 * time.Second):
		t.Fatal("status query waited on an open stream")
	}
	if err := stalled.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestRewrapBeforeCommitDoesNotBlockOtherLibraries pins the global-lock scoping:
// a beforeCommit callback that queries ANOTHER library must not deadlock.
func TestRewrapBeforeCommitDoesNotBlockOtherLibraries(t *testing.T) {
	m, _ := newTestManager()
	const otherLib = "lib-test-002"
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Provision(otherLib, UnsealModeAuto, ""); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := m.RewrapPrepared(
			testLib,
			UnsealModeAuto,
			"",
			nil,
			nil,
			func(RewrapCipher) error { return nil },
			func() error {
				if m.Sealed(otherLib) {
					return errors.New("other library reported sealed")
				}
				if !m.Provisioned(otherLib) {
					return errors.New("other library reported unprovisioned")
				}
				return nil
			},
			false,
		)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("beforeCommit callback deadlocked against another library's queries")
	}
}
