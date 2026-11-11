package offlinepack

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// builtPackMember builds and grants a one-member pack and returns the handlers,
// grant token and the member's stored ciphertext path.
func builtPackMember(t *testing.T) (*HTTPHandlers, string, string) {
	t.Helper()
	svc, authService, st, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-default")
	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia,
		TargetRef:  "media-alpha",
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	token, err := svc.IssuePackGrant(context.Background(), scope, pack.ID)
	if err != nil {
		t.Fatalf("IssuePackGrant: %v", err)
	}
	items, err := st.OfflinePackItems(context.Background(), pack.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("OfflinePackItems = %d items, %v; want one", len(items), err)
	}
	return NewHTTPHandlers(svc, authService), token, items[0].CiphertextPath
}

func TestContentRouteRefusesSymlinkedPackMember(t *testing.T) {
	handlers, token, member := builtPackMember(t)
	secret := []byte("outside-the-pack-root")
	outside := filepath.Join(t.TempDir(), "outside.bfop")
	if err := os.WriteFile(outside, secret, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(member); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, member); err != nil {
		t.Fatal(err)
	}

	rec := serveContent(t, handlers, token, "media-alpha")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("symlinked member status = %d, want 404", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), secret) {
		t.Fatal("content route served bytes through a symlinked pack member")
	}
}

func TestContentRouteRefusesSymlinkedPackDirectory(t *testing.T) {
	handlers, token, member := builtPackMember(t)
	packDir := filepath.Dir(member)
	secret := []byte("outside-pack-directory")
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, filepath.Base(member)), secret, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(packDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, packDir); err != nil {
		t.Fatal(err)
	}

	rec := serveContent(t, handlers, token, "media-alpha")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("symlinked pack directory status = %d, want 404", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), secret) {
		t.Fatal("content route served bytes through a symlinked pack directory")
	}
}

func TestOpenPackMemberRefusesPathOutsidePackDirectory(t *testing.T) {
	handlers, _, member := builtPackMember(t)
	packID := store.OfflinePackID(filepath.Base(filepath.Dir(member)))

	// A file that really exists at the pack root, one level above the pack's
	// directory: the stored path is not inside the pack directory, so it is
	// refused before any open.
	sibling := filepath.Join(filepath.Dir(filepath.Dir(member)), "stray.bfop")
	if err := os.WriteFile(sibling, []byte("stray"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{sibling, filepath.Join(filepath.Dir(member), "..", "stray.bfop")} {
		if f, _, err := handlers.openPackMember(packID, path); err == nil {
			_ = f.Close()
			t.Fatalf("openPackMember(%q) opened a file outside the pack directory", path)
		}
	}
	f, _, err := handlers.openPackMember(packID, member)
	if err != nil {
		t.Fatalf("openPackMember(member) = %v, want the stored member", err)
	}
	_ = f.Close()
}
