package libencrypt

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLegacyRefMigrationIncludesTrashedOwnerAndLeavesOrphan(t *testing.T) {
	secrets := newMemStore()
	secrets.values[legacyKEKRef("library-trashed")] = "legacy-kek"
	secrets.values[legacyBundleRef("library-trashed")] = `{"version":1}`
	secrets.values["libenc-bundle-orphaned"] = `{"version":1}`
	m := NewManagerWithParams(secrets, TestArgon2Params)

	report, err := m.MigrateLegacyRefs([]string{"library-live", "library-trashed"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(report.Orphans, "libenc-bundle-orphaned") {
		t.Fatalf("orphans = %v", report.Orphans)
	}
	if _, ok := secrets.values["libenc-bundle-orphaned"]; !ok {
		t.Fatal("orphan legacy ref was deleted")
	}
	if got, err := m.readOwnerSecret("library-trashed", secretKindKEK); err != nil || got != "legacy-kek" {
		t.Fatalf("migrated KEK = %q, %v", got, err)
	}
	if _, ok := secrets.values[legacyKEKRef("library-trashed")]; ok {
		t.Fatal("verified legacy source was not deleted")
	}
}

func TestLegacyRefMigrationCollisionFailsClosed(t *testing.T) {
	secrets := newMemStore()
	secrets.values[legacyBundleRef("library/a")] = `{"version":1}`
	m := NewManagerWithParams(secrets, TestArgon2Params)

	_, err := m.MigrateLegacyRefs([]string{"library/a", "library?a"})
	if !errors.Is(err, ErrLegacyRefCollision) {
		t.Fatalf("error = %v, want ErrLegacyRefCollision", err)
	}
	if _, ok := secrets.values[legacyBundleRef("library/a")]; !ok {
		t.Fatal("ambiguous source was moved")
	}
	if _, ok := secrets.values[bundleRef("library/a")]; ok {
		t.Fatal("first colliding owner received the ambiguous bundle")
	}
	if _, ok := secrets.values[bundleRef("library?a")]; ok {
		t.Fatal("second colliding owner received the ambiguous bundle")
	}
}

func TestLegacyRefMigrationResumesAfterDestinationPublication(t *testing.T) {
	secrets := newMemStore()
	owner := "library-resume"
	payload := `{"version":1}`
	secrets.values[legacyBundleRef(owner)] = payload
	destination, err := encodeOwnerSecret(owner, secretKindBundle, payload)
	if err != nil {
		t.Fatal(err)
	}
	// Crash state: destination is durable, source deletion did not happen.
	secrets.values[bundleRef(owner)] = destination
	m := NewManagerWithParams(secrets, TestArgon2Params)
	if _, err := m.MigrateLegacyRefs([]string{owner}); err != nil {
		t.Fatal(err)
	}
	if _, ok := secrets.values[legacyBundleRef(owner)]; ok {
		t.Fatal("resume did not retire verified legacy source")
	}
}

func TestLegacyRefMigrationDestinationMismatchPreservesSource(t *testing.T) {
	secrets := newMemStore()
	owner := "library-mismatch"
	legacy := legacyBundleRef(owner)
	secrets.values[legacy] = `{"version":1}`
	secrets.values[bundleRef(owner)] = `{"version":2,"owner":"wrong","kind":"bundle","payload":"bad"}`
	m := NewManagerWithParams(secrets, TestArgon2Params)
	if _, err := m.MigrateLegacyRefs([]string{owner}); err == nil {
		t.Fatal("mismatch migration succeeded")
	}
	if _, ok := secrets.values[legacy]; !ok {
		t.Fatal("mismatch deleted recoverable legacy source")
	}
}

func TestOwnerBoundRefRejectsCopiedRecord(t *testing.T) {
	secrets := newMemStore()
	record, err := encodeOwnerSecret("library-a", secretKindBundle, `{"version":1}`)
	if err != nil {
		t.Fatal(err)
	}
	secrets.values[bundleRef("library-b")] = record
	m := NewManagerWithParams(secrets, TestArgon2Params)
	if _, err := m.loadBundle("library-b"); !errors.Is(err, ErrSecretOwnership) {
		t.Fatalf("copied owner record error = %v, want ErrSecretOwnership", err)
	}
	if !m.Provisioned("library-b") || !m.Sealed("library-b") {
		t.Fatal("ownership corruption did not keep provisioned library fail-closed")
	}
}

func TestRefInventoryReportsValidatedV2Orphan(t *testing.T) {
	secrets := newMemStore()
	orphanOwner := "library-restorable"
	record, err := encodeOwnerSecret(orphanOwner, secretKindBundle, `{"version":1}`)
	if err != nil {
		t.Fatal(err)
	}
	ref := bundleRef(orphanOwner)
	secrets.values[ref] = record
	m := NewManagerWithParams(secrets, TestArgon2Params)
	report, err := m.MigrateLegacyRefs([]string{"library-present"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(report.Orphans, ref) {
		t.Fatalf("orphans = %v, want %s", report.Orphans, ref)
	}
	if secrets.values[ref] != record {
		t.Fatal("recoverable v2 orphan was changed or deleted")
	}
}

func TestRefInventoryRejectsV2HashOrKindMismatch(t *testing.T) {
	tests := []struct {
		name string
		ref  func(string) string
		kind secretKind
	}{
		{name: "hash", ref: func(string) string { return bundleRef("different-owner") }, kind: secretKindBundle},
		{name: "kind", ref: kekRef, kind: secretKindBundle},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			secrets := newMemStore()
			owner := "library-owner"
			record, err := encodeOwnerSecret(owner, tc.kind, "payload")
			if err != nil {
				t.Fatal(err)
			}
			ref := tc.ref(owner)
			secrets.values[ref] = record
			m := NewManagerWithParams(secrets, TestArgon2Params)
			if _, err := m.MigrateLegacyRefs([]string{owner}); !errors.Is(err, ErrSecretOwnership) {
				t.Fatalf("inventory error = %v, want ErrSecretOwnership", err)
			}
			if secrets.values[ref] != record {
				t.Fatal("invalid v2 ref was changed or deleted")
			}
		})
	}
}

func TestRefInventoryValidatesEveryV2EnvelopeBeforeLegacyMoves(t *testing.T) {
	owner := "library-owner"
	valid, err := encodeOwnerSecret(owner, secretKindBundle, "payload")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		ref  string
		raw  string
	}{
		{name: "malformed-json", ref: bundleRef(owner), raw: "{"},
		{name: "wrong-version", ref: bundleRef(owner), raw: `{"version":1,"owner":"library-owner","kind":"bundle","payload":"payload"}`},
		{name: "unknown-kind", ref: ownerRef(owner, secretKind("unknown")), raw: `{"version":2,"owner":"library-owner","kind":"unknown","payload":"payload"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			secrets := newMemStore()
			legacy := legacyKEKRef(owner)
			secrets.values[legacy] = "legacy-kek"
			secrets.values[tc.ref] = tc.raw
			m := NewManagerWithParams(secrets, TestArgon2Params)
			if _, err := m.MigrateLegacyRefs([]string{owner}); !errors.Is(err, ErrSecretOwnership) {
				t.Fatalf("inventory error = %v, want ErrSecretOwnership", err)
			}
			if secrets.values[legacy] != "legacy-kek" {
				t.Fatal("legacy ref moved before v2 validation completed")
			}
		})
	}

	t.Run("known-owner", func(t *testing.T) {
		secrets := newMemStore()
		secrets.values[bundleRef(owner)] = valid
		m := NewManagerWithParams(secrets, TestArgon2Params)
		report, err := m.MigrateLegacyRefs([]string{owner, owner})
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(report.Orphans, bundleRef(owner)) {
			t.Fatal("known owner was reported orphaned")
		}
	})
}

// TestProvisionRefusesUnreadableProvisionJournal keeps the inventory fail-closed:
// a journal the secure store cannot read is not absence, so a fresh provision
// must refuse rather than overwrite the key generation it could not inspect.
func TestProvisionRefusesUnreadableProvisionJournal(t *testing.T) {
	dir := t.TempDir()
	// A directory at the ref path is readable-by-name but not readable as a
	// secret: os.ReadFile reports EISDIR, which is not fs.ErrNotExist.
	if err := os.MkdirAll(filepath.Join(dir, provisionJournalRef(testLib)), 0o700); err != nil {
		t.Fatal(err)
	}
	m := NewManagerWithParams(diskSecretStore{dir: dir}, TestArgon2Params)
	exists, err := m.ownerSecretExists(testLib, secretKindProvisionJournal)
	if err == nil {
		t.Fatalf("ownerSecretExists = %v, nil; want the read error propagated", exists)
	}
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err == nil {
		t.Fatal("provision overwrote an unreadable provision journal")
	}
	if _, err := os.Stat(filepath.Join(dir, bundleRef(testLib))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("refused provision still wrote a bundle: %v", err)
	}
}
