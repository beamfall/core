package scan

import (
	"errors"
	"path/filepath"
	"strconv"

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
	_, err := s.st.ImportMediaFileVersion(s.ctx, store.MediaVersionImport{
		MediaID:           anchor.mediaID,
		File:              imported,
		Label:             parsed.VersionLabel,
		AnchorFingerprint: anchor.fingerprint,
		AnchorLabel:       anchor.label,
	})
	if errors.Is(err, store.ErrVersionNotAttached) || errors.Is(err, store.ErrManagedAdmissionInFlight) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return anchor.mediaID, true, nil
}

// seedVersionAnchors adds, once per directory, an anchor for each title a
// previous scan cataloged there. Without it a file whose name sorts before an
// already cataloged sibling would become its own Media, and the sibling could
// then never join it. The stored files are parsed like walked ones. Each
// title's anchor is its first Version in label order: the anchor re-imports as
// a plain file and so refreshes the Media row's probe facts, which playback
// reads as that first Version's.
func (s *scanWalkState) seedVersionAnchors(dir string) error {
	if s.versionDirsSeeded[dir] {
		return nil
	}
	s.versionDirsSeeded[dir] = true
	if s.parser == nil {
		return nil
	}
	files, err := s.st.MediaFilesInDirectory(s.ctx, s.req.LibraryID, dir)
	if err != nil {
		return err
	}
	type seed struct {
		file   store.MediaFile
		parsed ParsedMedia
	}
	firsts := map[string]seed{}
	var keys []string
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
		if !ok {
			keys = append(keys, key)
		}
		if !ok || (file.MediaID == prior.file.MediaID && file.VersionLabel < prior.file.VersionLabel) {
			firsts[key] = seed{file: file, parsed: parsed}
		}
	}
	for _, key := range keys {
		first := firsts[key]
		s.rememberVersionAnchor(key, first.file.MediaID, first.file.ContentFingerprint, first.parsed)
	}
	return nil
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
