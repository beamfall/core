package scan

import (
	"errors"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/beamfall/core/internal/store"
)

// versionAnchor is the first file of a title the walk imported in a
// directory. Later files the parser places on the same title join its Media
// as Versions (ADR-0009) instead of becoming Media of their own.
type versionAnchor struct {
	mediaID     store.MediaID
	fingerprint string
	label       string
}

// versionGroupKey names the title a parsed file belongs to within one
// directory, or "" when the file cannot group: an unparsed, untitled, Part,
// image or Extra file always imports on its own. The key is built from the
// plugin's parsed fields only, so the grouping rule itself stays in plugins.
func (s *scanWalkState) versionGroupKey(imported store.ImportMediaFile, parsed ParsedMedia, parsedOK bool) string {
	if !parsedOK || parsed.Title == "" || parsed.PartGroup != "" || imported.Extra || imported.Class == store.MediaClassImage {
		return ""
	}
	if !store.ValidMediaVersionLabel(parsed.VersionLabel) {
		return ""
	}
	return string(s.req.LibraryID) + "\x00" + filepath.Dir(imported.Path) + "\x00" + parsed.Class + "\x00" + parsed.Title +
		"\x00" + strconv.Itoa(parsed.Year) + "\x00" + parsed.Sequence + "\x00" + strconv.Itoa(parsed.Index) + "\x00" + strconv.Itoa(parsed.IndexEnd)
}

// attachVersion places imported on the anchor's Media when the walk already
// imported a file of the same title. It reports false when there is no anchor
// or the store refuses the attach, and the caller then imports the file as
// its own Media.
func (s *scanWalkState) attachVersion(key string, imported store.ImportMediaFile, parsed ParsedMedia) (store.MediaID, bool, error) {
	if key == "" {
		return "", false, nil
	}
	anchor, ok := s.versionAnchors[key]
	if !ok {
		if err := s.seedVersionAnchors(filepath.Dir(imported.Path)); err != nil {
			return "", false, err
		}
		if anchor, ok = s.versionAnchors[key]; !ok {
			return "", false, nil
		}
	}
	if anchor.fingerprint == imported.ContentFingerprint {
		return "", false, nil
	}
	// Two files without a parsed label would contend for the default slot,
	// so the store labels them from their file facts instead (CORE-2473).
	_, err := s.st.ImportMediaFileVersion(s.ctx, store.MediaVersionImport{
		MediaID:           anchor.mediaID,
		File:              imported,
		Label:             parsed.VersionLabel,
		AnchorFingerprint: anchor.fingerprint,
		AnchorLabel:       anchor.label,
		DeriveLabels:      parsed.VersionLabel == "" && anchor.label == "",
	})
	if errors.Is(err, store.ErrVersionNotAttached) {
		s.splitVersionDirs[filepath.Dir(imported.Path)] = true
	}
	if errors.Is(err, store.ErrVersionNotAttached) || errors.Is(err, store.ErrManagedAdmissionInFlight) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	s.noteVersion(key, imported.Path, anchor.mediaID)
	return anchor.mediaID, true, nil
}

// noteVersion records path as a file of the Version Media mediaID. A Media's
// Unmatched state is the Media's, not a file's: identifying a later Version
// can queue or clear it after an earlier Version was walked. So no Version
// file carries the count; the finished scan counts each Version Media once
// (countUnmatchedVersions, CORE-2194).
func (s *scanWalkState) noteVersion(key, path string, mediaID store.MediaID) {
	if key == "" {
		return
	}
	if s.result.versionMedia == nil {
		s.result.versionMedia = map[store.MediaID]bool{}
	}
	s.result.versionMedia[mediaID] = true
	s.noteFile(path, func(t *fileTally) { t.unmatched = false })
}

// countUnmatchedVersions counts each Version Media the scan walked that is
// still in the Unmatched queue once the walk is done, except the ones a
// finished walk of the same job already counted. A failed lookup only loses
// the count, as in noteUnmatched.
func (s *scanWalkState) countUnmatchedVersions() {
	for _, id := range mediaIDs(s.result.versionMedia) {
		if s.result.countedVersionMedia[store.MediaID(id)] {
			continue
		}
		queued, err := s.st.IsUnmatched(s.ctx, store.MediaID(id))
		if err != nil {
			slog.Warn("scan: unmatched lookup failed", "media", id, "error", err.Error())
			continue
		}
		if queued {
			s.result.MediaUnmatched++
			if s.result.countedVersionMedia == nil {
				s.result.countedVersionMedia = map[store.MediaID]bool{}
			}
			s.result.countedVersionMedia[store.MediaID(id)] = true
		}
	}
}

// seedVersionAnchors remembers the first Version of each title already
// cataloged in the directory. A later file can attach even when it sorts
// before that Version in the walk.
func (s *scanWalkState) seedVersionAnchors(dir string) error {
	if s.versionDirsSeeded[dir] {
		return nil
	}
	s.versionDirsSeeded[dir] = true
	firsts, split, err := s.directoryVersions(dir)
	if err != nil {
		return err
	}
	for key, first := range firsts {
		s.rememberVersionAnchor(key, first.file.MediaID, first.file.ContentFingerprint, first.parsed)
		if len(split[key]) > 1 {
			s.splitVersionDirs[dir] = true
		}
	}
	return nil
}

type directoryVersion struct {
	file   store.MediaFile
	parsed ParsedMedia
}

func (s *scanWalkState) directoryVersions(dir string) (map[string]directoryVersion, map[string][]store.MediaID, error) {
	files, err := s.st.MediaFilesInDirectory(s.ctx, s.req.LibraryID, dir)
	if err != nil {
		return nil, nil, err
	}
	firsts := make(map[string]directoryVersion)
	split := make(map[string][]store.MediaID)
	for _, file := range files {
		if file.PartOrdinal != 0 {
			continue
		}
		parsed, parsedOK, refused := s.parseFilename(file.Path, ParseProbe{})
		if refused {
			continue
		}
		candidate := store.ImportMediaFile{Path: file.Path, Extra: isExtraPath(s.extrasRoot, file.Path)}
		if imageClassForPath(file.Path) {
			candidate.Class = store.MediaClassImage
		}
		key := s.versionGroupKey(candidate, parsed, parsedOK)
		if key == "" {
			continue
		}
		prior, ok := firsts[key]
		if !ok || (file.MediaID == prior.file.MediaID && file.VersionLabel < prior.file.VersionLabel) {
			firsts[key] = directoryVersion{file: file, parsed: parsed}
		}
		if parsed.VersionLabel == "" && !slices.Contains(split[key], file.MediaID) {
			split[key] = append(split[key], file.MediaID)
		}
	}
	return firsts, split, nil
}

var mergeScanSplitVersions = (*store.Store).MergeSplitVersions

// Reread affected directories after verified copies have kept their sampled
// Media. Distinct files still use the default CORE-2490 survivor rule.
func (s *scanWalkState) mergeSplitVersions(unverified unverifiedCopies) error {
	if s.parser == nil {
		return s.ctx.Err()
	}
	for _, dir := range pendingIDs(s.splitVersionDirs) {
		_, split, err := s.directoryVersions(dir)
		if err != nil {
			return err
		}
		for _, key := range slices.Sorted(maps.Keys(split)) {
			if err := s.ctx.Err(); err != nil {
				return err
			}
			s.mergeTitleVersions(split[key], unverified)
		}
		if err := s.ctx.Err(); err != nil {
			return err
		}
		delete(s.splitVersionDirs, dir)
	}
	return s.ctx.Err()
}

// mergeTitleVersions batches a title's Media, then batches whatever neither
// survived nor merged. Each pass removes at least its survivor, so n Media take
// at most n-1 store calls.
func (s *scanWalkState) mergeTitleVersions(ids []store.MediaID, unverified unverifiedCopies) {
	remaining := slices.DeleteFunc(slices.Clone(ids), func(id store.MediaID) bool { return unverified[id] })
	for len(remaining) >= 2 && s.ctx.Err() == nil {
		result, err := mergeScanSplitVersions(s.st, s.ctx, s.req.LibraryID, remaining, time.Now().UTC())
		if err != nil {
			slog.Warn("scan: version merge failed", "library", s.req.LibraryID, "media", remaining, "error", err)
			return
		}
		if result.Survivor == "" {
			return // fewer than two candidates are eligible
		}
		for _, loser := range result.Merged {
			if s.result.versionMedia[loser] {
				delete(s.result.versionMedia, loser)
				s.result.versionMedia[result.Survivor] = true
			}
			for i := range s.result.Files {
				if s.result.Files[i].MediaID == loser {
					s.result.Files[i].MediaID = result.Survivor
				}
			}
		}
		remaining = slices.DeleteFunc(remaining, func(id store.MediaID) bool {
			return id == result.Survivor || slices.Contains(result.Merged, id)
		})
	}
}

// rememberVersionAnchor records the first imported file of a title.
func (s *scanWalkState) rememberVersionAnchor(key string, mediaID store.MediaID, fingerprint string, parsed ParsedMedia) {
	if key == "" {
		return
	}
	if _, ok := s.versionAnchors[key]; ok {
		return
	}
	s.versionAnchors[key] = versionAnchor{mediaID: mediaID, fingerprint: fingerprint, label: parsed.VersionLabel}
}
