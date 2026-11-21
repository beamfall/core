package scan

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/beamfall/core/internal/store"
)

// unverifiedCopies holds Media with a possible copy that this scan could not
// verify. They sit out title grouping until a later scan settles the pair.
// A pair whose digests differ is distinct content and needs no entry.
type unverifiedCopies map[store.MediaID]bool

// mergeVerifiedCopies runs after missing files and Extra relations have
// settled. Candidate discovery covers the Library, including earlier roots
// and scans. Only possible matches are hashed. Legacy files with an unknown
// mtime also need a digest because their size alone cannot establish freshness.
func (s *scanWalkState) mergeVerifiedCopies() (unverifiedCopies, error) {
	pairs, err := s.st.VersionCopyCandidates(s.ctx, s.req.LibraryID)
	if err != nil {
		if s.ctx.Err() != nil {
			return nil, s.ctx.Err()
		}
		slog.Warn("scan: could not list Version copy candidates", "library", s.req.LibraryID, "error", err)
		return nil, nil
	}

	// A pair that could not be verified must stay separate for a later scan.
	unverified := make(unverifiedCopies)
	digests := make(map[string]string)
	mergedInto := make(map[store.MediaID]store.MediaID)
	currentOwner := func(id store.MediaID) store.MediaID {
		for mergedInto[id] != "" {
			id = mergedInto[id]
		}
		return id
	}
	for _, pair := range pairs {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		pair.Sample.MediaID = currentOwner(pair.Sample.MediaID)
		pair.Legacy.MediaID = currentOwner(pair.Legacy.MediaID)
		if pair.Sample.MediaID == pair.Legacy.MediaID {
			continue
		}
		unsettled := func() { unverified[pair.Sample.MediaID], unverified[pair.Legacy.MediaID] = true, true }
		if !versionCopyPathCurrent(pair.Sample) {
			unsettled()
			continue
		}

		fingerprint := pair.Sample.ContentFingerprint
		digest, checked := digests[fingerprint]
		if !checked {
			digest, _ = hashScanFile(s.ctx, pair.Sample.Path, func(reader io.Reader, sample string) (string, error) {
				if sample != fingerprint {
					return "", errors.New("file no longer has its cataloged sample fingerprint")
				}
				return store.HashMediaFileContentReader(s.ctx, reader)
			})
			digests[fingerprint] = digest
		}
		if digest == "" {
			unsettled()
			continue // hashScanFile already logged the failure
		}
		// A mismatch releases the pair into title grouping, so both files must still be current.
		current := func() bool { return versionCopyPathCurrent(pair.Legacy) && versionCopyPathCurrent(pair.Sample) }
		if !current() {
			unsettled()
			continue
		}
		legacyDigest := pair.Legacy.ContentFingerprint
		if pair.Legacy.ModTime.IsZero() {
			// Size alone cannot prove that a legacy file without an mtime is unchanged.
			var err error
			legacyDigest, err = hashScanFile(s.ctx, pair.Legacy.Path, func(reader io.Reader, _ string) (string, error) {
				return store.HashMediaFileContentReader(s.ctx, reader)
			})
			// A changed legacy file is re-imported before it can pair.
			if err != nil || legacyDigest != pair.Legacy.ContentFingerprint || !current() {
				unsettled()
				continue
			}
		}
		if digest != legacyDigest {
			slog.Warn("scan: Version copy digest did not match", "sample", pair.Sample.Path, "legacy", pair.Legacy.Path)
			continue
		}
		ids := []store.MediaID{pair.Sample.MediaID, pair.Legacy.MediaID}
		merged, err := s.st.MergeSplitVersions(s.ctx, s.req.LibraryID, ids, time.Now().UTC(),
			store.VersionMergeOptions{Survivor: pair.Sample.MediaID, VerifiedCopy: &pair})
		if err != nil {
			slog.Warn("scan: verified Version copy merge failed", "library", s.req.LibraryID, "media", ids, "error", err)
			if errors.Is(err, store.ErrVersionMergeEngagementPending) {
				return nil, err
			}
		}
		if len(merged.Merged) == 0 {
			unsettled() // a failed or refused merge leaves the pair for a later scan
		}
		for _, loser := range merged.Merged {
			mergedInto[loser] = merged.Survivor
			if unverified[loser] {
				delete(unverified, loser)
				unverified[merged.Survivor] = true
			}
		}
	}
	if len(mergedInto) != 0 {
		for i := range s.result.Files {
			file := &s.result.Files[i]
			file.MediaID = currentOwner(file.MediaID)
		}
	}
	return unverified, s.ctx.Err()
}

func versionCopyPathCurrent(file store.MediaFile) bool {
	info, err := os.Lstat(file.Path)
	if err == nil && info.Mode().IsRegular() && info.Size() == file.SizeBytes &&
		(file.ModTime.IsZero() || store.MediaFileModTimeMatches(file.ModTime, info.ModTime())) {
		return true
	}
	slog.Warn("scan: Version copy no longer matches its cataloged file", "path", file.Path, "error", err)
	return false
}
