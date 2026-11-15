package offlinepack

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// TestBuildPackSizesEncryptedOriginalsFromTheDecryptReader pins CORE-1742: the
// store's Media file reads carry no size, so an Original pack in an Encrypted
// Library takes each item's plaintext length from its decrypt reader instead
// of refusing with a missing catalog size. Plaintext still streams one bounded
// source at a time.
func TestBuildPackSizesEncryptedOriginalsFromTheDecryptReader(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-revealed")
	files := fixtureMediaFiles(t, st, scope, "media-alpha", "media-archive")
	bodies := map[string][]byte{}
	want := map[store.MediaID]int64{}
	for _, file := range files {
		if file.SizeBytes != 0 {
			t.Fatalf("fixture %s catalog size = %d, want the store's unsized read", file.MediaID, file.SizeBytes)
		}
		body := bytes.Repeat([]byte(file.MediaID), 70000)
		bodies[file.Path] = body
		want[file.MediaID] = int64(len(body))
	}
	source := &observingDecryptSource{bodies: bodies}
	svc.SetEncryptedContent(source, func(store.LibraryID) bool { return true }, func(store.LibraryID, string) (string, string, error) {
		return "media", "item", nil
	})

	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{TargetKind: TargetKindMedia, TargetRef: "media-alpha,media-archive"})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	items, err := st.OfflinePackItems(context.Background(), pack.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	for _, item := range items {
		if item.PlaintextSize != want[item.MediaID] {
			t.Fatalf("%s plaintext size = %d, want %d", item.MediaID, item.PlaintextSize, want[item.MediaID])
		}
	}
	if source.maxActive != 1 {
		t.Fatalf("simultaneous plaintext sources = %d, want 1", source.maxActive)
	}
}

// TestBuildPackRefusesEncryptedSourceShorterThanItsHeader pins that the size
// read from the decrypt reader is re-proven by the full hashing pass: a stream
// that ends before its declared length refuses the build.
func TestBuildPackRefusesEncryptedSourceShorterThanItsHeader(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-revealed")
	files := fixtureMediaFiles(t, st, scope, "media-alpha")
	source := &truncatingDecryptSource{declared: 4096, body: bytes.Repeat([]byte("x"), 1024), path: files[0].Path}
	svc.SetEncryptedContent(source, func(store.LibraryID) bool { return true }, func(store.LibraryID, string) (string, string, error) {
		return "media", "item", nil
	})
	_, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{TargetKind: TargetKindMedia, TargetRef: "media-alpha"})
	if err == nil || !strings.Contains(err.Error(), "size differs") {
		t.Fatalf("BuildPack error = %v, want the hashing pass to refuse a short stream", err)
	}
}

type truncatingDecryptSource struct {
	declared int64
	body     []byte
	path     string
}

func (s *truncatingDecryptSource) OpenDecryptReader(_ string, path string, _, _ string) (io.ReadSeekCloser, error) {
	if path != s.path {
		return nil, errors.New("unknown decrypt path")
	}
	return &truncatedReader{Reader: bytes.NewReader(s.body), declared: s.declared}, nil
}

// truncatedReader declares a plaintext length at SeekEnd that its stream does
// not reach, as a tampered or truncated object would.
type truncatedReader struct {
	*bytes.Reader
	declared int64
}

func (r *truncatedReader) Seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekEnd {
		return r.declared + offset, nil
	}
	return r.Reader.Seek(offset, whence)
}

func (r *truncatedReader) Close() error { return nil }
