package store

import (
	"context"
	"testing"
)

// TestEntityTitleFoldAppliesOnlyToSharedCatalogLibraries pins CORE-2459: the
// title-fold merge journals in the shared catalog, so it stays off for an
// encrypted Library.
func TestEntityTitleFoldAppliesOnlyToSharedCatalogLibraries(t *testing.T) {
	st, media := seededEncryptedMedia(t, "fold-secret")
	defer st.Close()
	ctx := context.Background()
	if applies, err := st.EntityTitleFoldApplies(ctx, media.LibraryID); err != nil || applies {
		t.Fatalf("encrypted Library applies = %v, %v; want false", applies, err)
	}
	plain, err := st.CreateLibrary(ctx, "Plain", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if applies, err := st.EntityTitleFoldApplies(ctx, plain.ID); err != nil || !applies {
		t.Fatalf("shared-catalog Library applies = %v, %v; want true", applies, err)
	}
}
