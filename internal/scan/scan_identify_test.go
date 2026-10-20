package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// pinAwareIdentifier is a test double for the PIPE-1 auto-identify pipeline at
// scan's seam. It mirrors the real pipeline's rescan contract: it reads the
// FREEZE-3 match-pin for the scanned file and, when the match is pinned, leaves
// it alone (records a skip) instead of re-identifying. It records every call so
// the test can assert scan triggers identification on each imported item with
// the right (mediaID, fingerprint).
type pinAwareIdentifier struct {
	st          *store.Store
	reidentify  []store.MediaID // items it re-identified (unpinned)
	skipped     []store.MediaID // items it left alone (pinned)
	fingerprint map[store.MediaID]string
}

func (p *pinAwareIdentifier) AutoIdentify(ctx context.Context, mediaID store.MediaID, pluginID, fingerprint string, parsed ParsedMedia) error {
	if p.fingerprint == nil {
		p.fingerprint = map[store.MediaID]string{}
	}
	p.fingerprint[mediaID] = fingerprint
	pinned, found, err := p.st.MatchPinned(ctx, fingerprint)
	if err != nil {
		return err
	}
	if found && pinned {
		p.skipped = append(p.skipped, mediaID)
		return nil
	}
	p.reidentify = append(p.reidentify, mediaID)
	return nil
}

// TestScanTriggersIdentificationAndPinSurvivesRescan covers PIPE-1 test (c): a
// scan triggers identification on each imported item, and once a match is pinned
// (the "Fix match" pin, FREEZE-3) a later rescan does NOT re-identify it — the
// pin survives the rescan. It also exercises the scan→Identifier wiring: the
// Identifier is invoked with the file's content fingerprint, the pin's key.
func TestScanTriggersIdentificationAndPinSurvivesRescan(t *testing.T) {
	// feature:scan-pipeline
	root := t.TempDir()
	writeScanFile(t, root, "movie.mp4", []byte("video bytes"))
	st := storetest.Open(t)
	ctx := context.Background()
	// A plugin-bound Library: scan resolves the plugin ID that keys the
	// auto-identify pipeline (an unbound Library has no plugin, so nothing to
	// auto-identify against).
	library, err := st.CreateLibrary(ctx, "Films", false, "filmtv", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	identifier := &pinAwareIdentifier{st: st}
	req := Request{LibraryID: library.ID, Root: root}

	// First scan: the item is unpinned, so the pipeline re-identifies it.
	first, err := Scan(ctx, st, nil, nil, nil, nil, identifier, req)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if len(first.Files) != 1 {
		t.Fatalf("first scan imported %d files, want 1", len(first.Files))
	}
	mediaID := first.Files[0].MediaID
	fingerprint := first.Files[0].ContentFingerprint
	if len(identifier.reidentify) != 1 || identifier.reidentify[0] != mediaID {
		t.Fatalf("first scan did not trigger identification on the import: reidentify=%v", identifier.reidentify)
	}
	if identifier.fingerprint[mediaID] != fingerprint {
		t.Fatalf("identifier saw fingerprint %q, want the scanned %q", identifier.fingerprint[mediaID], fingerprint)
	}

	// Pin the match — the "Fix match" mutation does this so the corrected match
	// survives rescans.
	if ok, err := st.SetMatchPinned(ctx, fingerprint, true); err != nil || !ok {
		t.Fatalf("SetMatchPinned: ok=%v err=%v", ok, err)
	}

	// Bump the file's mtime (content unchanged → same fingerprint, same pin) so
	// the rescan misses the unchanged-file fast-path and fully re-processes the
	// item — the case where the pipeline WOULD re-identify if the pin did not
	// protect it. This is the meaningful rescan: a fast-path skip leaves the
	// match untouched trivially; the pin must hold even when the file is re-walked.
	// A changed but settled mtime (past the ADR-0032 §4 stability window) so the
	// file is re-processed rather than skipped as still-streaming.
	bumped := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(filepath.Join(root, "movie.mp4"), bumped, bumped); err != nil {
		t.Fatal(err)
	}

	// Rescan. The scan triggers identification on the re-processed item, but
	// because it is now pinned the pipeline leaves it alone — the pin survives the
	// rescan rather than being overwritten.
	identifier.reidentify, identifier.skipped = nil, nil
	if _, err := Scan(ctx, st, nil, nil, nil, nil, identifier, req); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if len(identifier.reidentify) != 0 {
		t.Fatalf("rescan re-identified a PINNED match (%v) — the pin must survive", identifier.reidentify)
	}
	if len(identifier.skipped) != 1 || identifier.skipped[0] != mediaID {
		t.Fatalf("rescan did not leave the pinned match alone: skipped=%v", identifier.skipped)
	}

	// The pin is still set after the rescan.
	if pinned, _, err := st.MatchPinned(ctx, fingerprint); err != nil || !pinned {
		t.Fatalf("pin cleared by rescan: pinned=%v err=%v", pinned, err)
	}
}
