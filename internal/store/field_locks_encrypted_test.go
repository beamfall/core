package store

import (
	"context"
	"testing"
)

// CORE-1653: field locks on Media in an encrypted Library live on that
// Library's catalog plane, so setting, reading and honouring a lock must all
// resolve the Media's own plane rather than the default catalog plane.
func TestMediaFieldLockOnEncryptedLibraryMediaSurvivesRehydration(t *testing.T) {
	st, media := seededEncryptedMedia(t, "locked-original")
	defer st.Close()
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}

	locks, err := st.SetMediaFieldLock(ctx, media.ID, "title", true, filter)
	if err != nil {
		t.Fatalf("SetMediaFieldLock: %v", err)
	}
	if !fieldLocked(locks, "title") {
		t.Fatalf("SetMediaFieldLock returned %+v, want title locked", locks)
	}
	read, err := st.MediaFieldLocks(ctx, media.ID, filter)
	if err != nil {
		t.Fatalf("MediaFieldLocks: %v", err)
	}
	if !fieldLocked(read, "title") || fieldLocked(read, "summary") || fieldLocked(read, "year") {
		t.Fatalf("MediaFieldLocks = %+v, want only title locked", read)
	}

	reimport := encryptedImport(media.LibraryID, "sha256:locked-original", "/enc/locked-original.mkv")
	reimport.Title = "rehydrated-title"
	refreshed, _, err := st.ImportMediaFile(ctx, reimport)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if refreshed.ID != media.ID {
		t.Fatalf("re-import Media ID = %q, want %q", refreshed.ID, media.ID)
	}
	got, err := st.Media(ctx, media.ID, filter)
	if err != nil {
		t.Fatalf("Media: %v", err)
	}
	if got.Title != "locked-original" {
		t.Fatalf("re-hydration overwrote locked title: %q", got.Title)
	}

	if _, err := st.SetMediaFieldLock(ctx, media.ID, "title", false, filter); err != nil {
		t.Fatalf("clear lock: %v", err)
	}
	if _, _, err := st.ImportMediaFile(ctx, reimport); err != nil {
		t.Fatalf("re-import after unlock: %v", err)
	}
	if got, err = st.Media(ctx, media.ID, filter); err != nil {
		t.Fatalf("Media after unlock: %v", err)
	}
	if got.Title != "rehydrated-title" {
		t.Fatalf("unlocked title = %q, want re-hydrated value", got.Title)
	}
}

func fieldLocked(locks []MediaFieldLock, field string) bool {
	for _, lock := range locks {
		if lock.Field == field {
			return lock.Locked
		}
	}
	return false
}
