package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMediaUploadSweepPreservesUnreadableSidecar(t *testing.T) {
	staging := t.TempDir()
	now := time.Now()
	const unreadable = "11111111111111111111111111111111"
	const corrupt = "22222222222222222222222222222222"
	const expired = "33333333333333333333333333333333"
	for _, id := range []string{unreadable, corrupt, expired} {
		if err := os.WriteFile(filepath.Join(staging, id+".part"), []byte("part"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(staging, unreadable+".json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := readMediaUpload(staging, unreadable); !errors.Is(err, syscall.EISDIR) {
		t.Fatalf("injected sidecar read error = %v, want EISDIR", err)
	}
	if err := os.WriteFile(filepath.Join(staging, corrupt+".json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(MediaUpload{ID: expired, Size: 8})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, expired+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-MediaUploadTTL - time.Hour)
	if err := os.Chtimes(filepath.Join(staging, expired+".part"), old, old); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sweepMediaUploads(staging, now); !errors.Is(err, syscall.EISDIR) {
		t.Fatalf("sweep = %v, want sidecar read error", err)
	}
	for _, suffix := range []string{".part", ".json"} {
		if _, err := os.Lstat(filepath.Join(staging, unreadable+suffix)); err != nil {
			t.Errorf("unreadable session lost its %s file: %v", suffix, err)
		}
		for _, id := range []string{corrupt, expired} {
			if _, err := os.Lstat(filepath.Join(staging, id+suffix)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("sweep retained %s%s: %v", id, suffix, err)
			}
		}
	}
}

func TestMediaUploadPublishOverReservationDoesNotClobber(t *testing.T) {
	dir, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := dir.WriteFile(".upload-temp", []byte("staged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := dir.WriteFile("existing.mkv", []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := publishMediaUploadOverReservation(dir, ".upload-temp", "existing.mkv"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("conflicting publish = %v, want EEXIST", err)
	}
	if got, err := dir.ReadFile("existing.mkv"); err != nil || string(got) != "original" {
		t.Fatalf("existing file = %q, %v", got, err)
	}
	if err := publishMediaUploadOverReservation(dir, ".upload-temp", "placed.mkv"); err != nil {
		t.Fatal(err)
	}
	if got, err := dir.ReadFile("placed.mkv"); err != nil || string(got) != "staged" {
		t.Fatalf("published file = %q, %v", got, err)
	}
	if err := publishMediaUploadOverReservation(dir, ".missing-temp", "failed.mkv"); err == nil {
		t.Fatal("publishing a missing copy succeeded")
	}
	if _, err := dir.Lstat("failed.mkv"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed publish left a reservation: %v", err)
	}
}
