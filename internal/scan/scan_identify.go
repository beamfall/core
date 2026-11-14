package scan

import (
	"errors"
	"log/slog"
	"sort"

	"github.com/beamfall/core/internal/store"
)

// MissingCredentialError is the consumer-side view of an identification error
// caused by a provider credential that is not configured (scrape's
// CredentialMissingError satisfies it). scan matches it with errors.As rather
// than importing scrape, which the architecture whitelist forbids.
type MissingCredentialError interface {
	error
	// MissingCredential names the credential ref (the scraper AuthRef). It
	// is plugin data, never the secret or its file path.
	MissingCredential() string
}

// autoIdentify runs the best-effort identification pass for one Media. A
// missing provider credential is not a per-file failure: every file in the
// Library would hit it, so it is noted once per scan (CORE-1101) and the file
// continues unidentified. Any other error is logged per file as before.
func (s *scanWalkState) autoIdentify(path string, mediaID store.MediaID, fingerprint string, parsed ParsedMedia, failure string) {
	err := s.identifier.AutoIdentify(s.ctx, mediaID, s.libraryPluginID, fingerprint, parsed)
	if err == nil {
		return
	}
	var missing MissingCredentialError
	if errors.As(err, &missing) && missing.MissingCredential() != "" {
		s.noteMissingCredential(missing.MissingCredential())
		return
	}
	slog.Warn(failure, "path", path, "media", string(mediaID), "error", err.Error())
}

// noteMissingCredential records ref in the scan result and logs it the first
// time this scan (including earlier runs of a yielded scan job) sees it.
func (s *scanWalkState) noteMissingCredential(ref string) {
	refs := s.result.MissingCredentials
	i := sort.SearchStrings(refs, ref)
	if i < len(refs) && refs[i] == ref {
		return
	}
	refs = append(refs, "")
	copy(refs[i+1:], refs[i:])
	refs[i] = ref
	s.result.MissingCredentials = refs
	slog.Warn("scan: provider credential not configured; items stay unidentified until it is added",
		"library", string(s.req.LibraryID),
		"plugin", s.libraryPluginID,
		"credential", ref,
		"hint", "save the key as a 0600 file named after the credential in the data directory's secrets folder (or set it with the setScraperCredential admin mutation), then rescan")
}

// missingCredentialsMessage is the completed scan job's progress message: it
// names the credential to add instead of a bare "complete".
func missingCredentialsMessage(refs []string) string {
	if len(refs) == 0 {
		return "Scan complete"
	}
	msg := "Scan complete; items not identified: provider credential not configured ("
	for i, ref := range refs {
		if i > 0 {
			msg += ", "
		}
		msg += ref
	}
	return msg + "); add it and rescan"
}
