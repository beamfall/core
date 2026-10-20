package store

import (
	"context"
	"testing"
)

func TestLibraryEncryptionOwnersIncludesTrashedLibraries(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	trashed, err := st.CreateLibrary(ctx, "Trashed Crypto Owner", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteLibrary(ctx, trashed.ID); err != nil {
		t.Fatal(err)
	}
	owners, err := st.LibraryEncryptionOwners(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, owner := range owners {
		if owner == trashed.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("trashed owner %q omitted from %v", trashed.ID, owners)
	}
}
