package scan

import (
	"errors"
	"log/slog"
	"sync"

	"github.com/beamfall/core/internal/store"
)

// missingCredentialError is scan's consumer-side view of scrape's typed
// "credential not configured" error (scan stays free of a scrape import). An
// identifier whose provider has no key configured fails identically for every
// file, so the cause is reported once per scan rather than per file.
type missingCredentialError interface {
	error
	MissingCredentialRef() string
	ConfigureHint() string
}

// credentialNotices dedupes missing-credential reports within one scan.
type credentialNotices struct {
	mu       sync.Mutex
	reported map[string]struct{}
}

// first reports whether ref has not been noticed yet in this scan, and marks it.
func (n *credentialNotices) first(ref string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.reported[ref]; ok {
		return false
	}
	if n.reported == nil {
		n.reported = map[string]struct{}{}
	}
	n.reported[ref] = struct{}{}
	return true
}

// reportAutoIdentifyError logs one auto-identify failure. A missing provider
// credential is logged once per scan per credential with a hint naming where
// to configure it; every other failure is logged per file as before.
func (s *scanWalkState) reportAutoIdentifyError(path string, mediaID store.MediaID, err error) {
	var missing missingCredentialError
	if errors.As(err, &missing) {
		if s.missingCredentials.first(missing.MissingCredentialRef()) {
			slog.Warn("scan: metadata provider credential not configured; items import without provider metadata",
				"library", string(s.req.LibraryID), "plugin", s.libraryPluginID,
				"hint", missing.ConfigureHint())
		}
		return
	}
	slog.Warn("scan: auto-identify failed", "path", path, "media", string(mediaID), "error", err.Error())
}
