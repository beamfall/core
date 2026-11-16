package scan

import (
	"errors"
	"log/slog"
	"sort"
	"strings"

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

// CredentialLabeler is an optional Identifier capability that names a
// provider credential ref for the owner: the plugin's display name and the
// credential's label, for example "Example Plugin: API key". The ref is the
// internal scoped id (bfplugin-<hex>-<hex>), which status text must never
// carry (CORE-2187). An unknown ref yields "".
type CredentialLabeler interface {
	CredentialLabel(ref string) string
}

// unknownProviderLabel stands in for a ref no labeler can name, so the
// status text still never carries the internal id.
const unknownProviderLabel = "a plugin's provider credential"

// credentialLabel names ref for status text and logs.
func credentialLabel(identifier Identifier, ref string) string {
	if labeler, ok := identifier.(CredentialLabeler); ok {
		if label := labeler.CredentialLabel(ref); label != "" {
			return label
		}
	}
	return unknownProviderLabel
}

// autoIdentify runs the best-effort identification pass for one Media. A
// missing provider credential is not a per-file failure: every file in the
// Library would hit it, so it is noted once per scan (CORE-1101) and the file
// continues unidentified. Any other error is logged per file as before.
func (s *scanWalkState) autoIdentify(path string, mediaID store.MediaID, fingerprint string, parsed ParsedMedia, failure string) {
	err := s.identifier.AutoIdentify(s.ctx, mediaID, s.libraryPluginID, fingerprint, parsed)
	if err == nil {
		s.noteUnmatched(path, mediaID)
		return
	}
	var missing MissingCredentialError
	if errors.As(err, &missing) && missing.MissingCredential() != "" {
		s.noteMissingCredential(missing.MissingCredential())
		return
	}
	slog.Warn(failure, "path", path, "media", string(mediaID), "error", err.Error())
}

// noteUnmatched counts the item for the scan summary when identification
// left it in the Unmatched queue (CORE-2040). A failed lookup only loses the
// count; the queue itself is unaffected.
func (s *scanWalkState) noteUnmatched(path string, mediaID store.MediaID) {
	queued, err := s.st.IsUnmatched(s.ctx, mediaID)
	if err != nil {
		slog.Warn("scan: unmatched lookup failed", "media", string(mediaID), "error", err.Error())
		return
	}
	if queued {
		s.noteFile(path, func(t *fileTally) { t.unmatched = true })
	}
}

// noteUnmatchedRecord records a folder-level Unmatched record once per scan,
// however many of the folder's files were refused, and counts it in the summary
// only while it is still pending in the review queue: a refusal the operator
// already resolved or suppressed leaves nothing Unmatched (CORE-2040).
func (s *scanWalkState) noteUnmatchedRecord(id string) error {
	if _, noted := s.result.unmatchedRecords[id]; noted {
		return nil
	}
	pending, err := s.st.UnmatchedRecordPending(s.ctx, id)
	if err != nil {
		return err
	}
	if s.result.unmatchedRecords == nil {
		s.result.unmatchedRecords = map[string]bool{}
	}
	s.result.unmatchedRecords[id] = pending
	if pending {
		s.result.MediaUnmatched++
	}
	return nil
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
		"provider", credentialLabel(s.identifier, ref),
		"credential", ref,
		"hint", "save the key as a 0600 file named after the credential in the data directory's secrets folder (or set it with the setScraperCredential admin mutation), then rescan")
}

// missingCredentialsMessage is the completed scan job's progress message: it
// names each credential to add by its plugin and label, and where to add it,
// instead of a bare "complete". It never shows the internal ref (CORE-2187).
func missingCredentialsMessage(refs []string, identifier Identifier) string {
	if len(refs) == 0 {
		return "Scan complete"
	}
	labels := make([]string, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		label := credentialLabel(identifier, ref)
		if seen[label] {
			continue
		}
		seen[label] = true
		labels = append(labels, label)
	}
	return "Scan complete; items not identified: provider credential not configured (" +
		strings.Join(labels, ", ") + "); add it in the console under Plugins, then rescan"
}
