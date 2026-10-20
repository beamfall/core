// Package secretstoretest provides the shared SecretStore conformance checks.
package secretstoretest

import (
	"errors"
	"io/fs"
	"testing"
)

const absentRef = "libencrypt-conformance-absent"

// AssertAbsence verifies that a missing secret remains distinguishable from an
// unreadable secret, as required by libencrypt.SecretStore.
func AssertAbsence(t testing.TB, store interface {
	Secret(string) (string, error)
}) {
	t.Helper()
	_, err := store.Secret(absentRef)
	if err == nil {
		t.Fatal("missing secret returned no error")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing secret error = %v, want errors.Is(err, fs.ErrNotExist)", err)
	}
}
