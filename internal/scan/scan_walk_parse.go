package scan

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

type contextualDirectoryPreflight struct {
	probes   map[string]ParseProbe
	parsed   map[string]ParsedMedia
	accepted map[string]bool
	refused  bool
	refusal  ParsedMedia
	// audioParts reports that the plugin accepted an audio-only sibling as a
	// numbered part, so the folder is a parts folder and a still image in it
	// is never an item (CORE-1833).
	audioParts bool
}

func (s *scanWalkState) preflightDirectory(path string) (*contextualDirectoryPreflight, error) {
	dir := filepath.Dir(path)
	if cached := s.directoryPreflights[dir]; cached != nil {
		return cached, nil
	}
	contextual, ok := s.parser.(ContextFilenameParser)
	if !ok || s.directoryContext(path).DirectoryMediaFileCount != DirectoryMediaFileCountMultiple {
		return nil, nil
	}
	decision := &contextualDirectoryPreflight{
		probes:   map[string]ParseProbe{},
		parsed:   map[string]ParsedMedia{},
		accepted: map[string]bool{},
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	// Images are never probed, so a parser that wants embedded evidence
	// refuses every image. That says nothing about the folder: an image
	// refusal refuses the folder only when no probed sibling was accepted
	// (CORE-1710).
	var imageRefusal ParsedMedia
	imageRefusalPath, refusalPath, otherAccepted := "", "", false
	var candidates []string
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		candidate := filepath.Join(dir, entry.Name())
		if s.isSupportedMediaFile(candidate, info) {
			candidates = append(candidates, candidate)
		}
	}
	probes := s.probeSiblings(candidates)
	for i, candidate := range candidates {
		isImage := imageClassForPath(candidate)
		parseProbe := probes[i]
		decision.probes[candidate] = parseProbe
		legacy, legacyOK := s.parser.Parse(candidate, parseProbe)
		parsed, accepted := contextual.ParseWithContext(candidate, parseProbe, s.directoryContext(candidate))
		decision.parsed[candidate], decision.accepted[candidate] = parsed, accepted
		if !isImage && s.isParsedPart(parsed, accepted) && parseProbe.HasAudio && !parseProbe.HasVideo {
			decision.audioParts = true
		}
		switch {
		case accepted && !isImage:
			otherAccepted = true
		case !legacyOK || accepted:
		case isImage:
			if imageRefusalPath == "" {
				imageRefusal, imageRefusalPath = legacy, candidate
			}
		case !decision.refused:
			decision.refused, decision.refusal, refusalPath = true, legacy, candidate
		}
	}
	if !decision.refused && imageRefusalPath != "" && !otherAccepted {
		decision.refused, decision.refusal, refusalPath = true, imageRefusal, imageRefusalPath
	}
	if decision.refused {
		// The folder goes to the Unmatched queue; say why in the scan log
		// too, once per folder, so it is never silently missing.
		slog.Warn("scan: library plugin refused a file in a multi-file folder; folder queued as unmatched",
			"folder", dir, "file", refusalPath, "plugin", s.libraryPluginID, "reason", store.UnmatchedReasonAmbiguous)
	}
	s.directoryPreflights[dir] = decision
	return decision, nil
}

// probeSiblings probes a folder's media files for the contextual preflight,
// in parallel within the scan's worker budget: a folder often holds dozens of media
// files, and probing them one after another stalled the ordered walk for the
// sum of their probe times on a slow share (CORE-2353). Images are never
// probed, and a failed probe yields the empty ParseProbe, as before. The
// result is in candidates' order, so the decision does not depend on timing.
func (s *scanWalkState) probeSiblings(candidates []string) []ParseProbe {
	probes := make([]ParseProbe, len(candidates))
	if s.prober == nil {
		return probes
	}
	var wg sync.WaitGroup
	for i, candidate := range candidates {
		if imageClassForPath(candidate) {
			continue
		}
		if err := s.acquireBudget(s.ctx); err != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer s.releaseBudget()
			probeCtx, cancel := context.WithTimeout(s.ctx, probeTimeout)
			defer cancel()
			if probe, err := s.prober.Probe(probeCtx, candidate); err == nil {
				probes[i] = ParseProbe{HasVideo: probe.HasVideo, HasAudio: probe.AudioCodec != "", DurationSeconds: probe.DurationSeconds, Tags: probe.Tags}
			}
		}()
	}
	wg.Wait()
	return probes
}

// skipsStillImage reports whether path is a still image the plugin did not
// accept, inside a folder of audio parts. Whatever its name, such an image is
// a scan, a booklet page or other artwork beside the parts, never a playable
// item, so the walk does not catalogue it (CORE-1833).
func (d *contextualDirectoryPreflight) skipsStillImage(path string) bool {
	return d != nil && d.audioParts && imageClassForPath(path) && !d.accepted[path]
}

func (s *scanWalkState) parseFilename(path string, probe ParseProbe) (ParsedMedia, bool, bool) {
	legacy, legacyOK := s.parser.Parse(path, probe)
	contextual, ok := s.parser.(ContextFilenameParser)
	if !ok {
		return legacy, legacyOK, false
	}
	parseContext := s.directoryContext(path)
	if decision := s.directoryPreflights[filepath.Dir(path)]; decision != nil {
		return decision.parsed[path], decision.accepted[path], false
	}
	parsed, parsedOK := contextual.ParseWithContext(path, probe, parseContext)
	refused := parseContext.DirectoryMediaFileCount == DirectoryMediaFileCountMultiple && legacyOK && !parsedOK
	if refused {
		return legacy, false, true
	}
	return parsed, parsedOK, false
}

func (s *scanWalkState) parsedPartKey(parsed ParsedMedia) string {
	return string(s.req.LibraryID) + "\x00" + parsed.PartGroup + "\x00" + parsed.Class + "\x00" + parsed.Title
}

func (*scanWalkState) isParsedPart(parsed ParsedMedia, ok bool) bool {
	return ok && parsed.PartGroup != "" && parsed.Index > 0 && parsed.Title != ""
}

// flushPendingPartsUnlessImage flushes the pending parts group before a file
// that is not a part, except a still image: an image whose name sorts between
// numbered parts is not part of the group and must not split it (CORE-1837).
func (s *scanWalkState) flushPendingPartsUnlessImage(isImage bool) error {
	if isImage {
		return nil
	}
	return s.flushPendingParts()
}

func (s *scanWalkState) flushPendingParts() error {
	if len(s.pendingParts) == 0 {
		return nil
	}
	// Complete every file consumed before the group is flushed. An image
	// consumed between the parts may still be queued, and completing it after
	// the group would move the resume checkpoint back below the later parts.
	if err := s.completePending(); err != nil {
		return err
	}
	parts := s.pendingParts
	s.pendingParts = nil
	sort.SliceStable(parts, func(i, j int) bool {
		return parts[i].parsed.Index < parts[j].parsed.Index
	})
	imports := make([]store.ImportMediaFilePart, len(parts))
	for i, part := range parts {
		imports[i] = store.ImportMediaFilePart{File: part.imported, SourceOrdinal: part.parsed.Index}
	}
	media, files, err := s.st.ImportMediaFileParts(s.ctx, imports)
	if err != nil {
		// A member a managed import is still admitting skips the whole group
		// this pass; the next scan groups it with the cataloged file.
		if err = skipManagedAdmissionInFlight(err, parts[0].imported.Path); err == nil {
			for _, part := range parts {
				s.dropTallies(part.imported.Path)
			}
			s.releaseHeld()
		}
		return err
	}
	// The group is in the catalog from here, so a scan stopped before
	// reporting it still counts every part (see resumeCheckpoint).
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = file.Path
	}
	s.unreportedParts = paths
	first := parts[0]
	if err := s.refreshParserFields(media.ID, first.refresh); err != nil {
		return err
	}
	if err := ensurePartBoundaryMarkers(s.ctx, s.st, s.prober, parts, media.ID, now()); err != nil {
		return err
	}
	// A multi-part Media hydrates from its first part's tags and path, as a
	// single-file Media does from its own, when that part was probed this
	// pass: an unchanged first part carries no tags, and hydrating from its
	// path alone would re-add relations on every scan.
	if first.probed {
		if err := s.hydrateMetadata(media, first.imported.Path, first.imported.ContentFingerprint, first.tags); err != nil {
			return err
		}
	}
	// The group's Unmatched count rides on its last part in walk order, which
	// part numbers need not follow. A scan stopped before reporting that part
	// walks it again on resume and re-imports it with the rest as a group,
	// which counts the Media there, once (CORE-2194).
	if s.identifier != nil && s.libraryPluginID != "" {
		last := paths[0]
		for _, path := range paths[1:] {
			if compareWalkOrder(s.req.Root, path, last) > 0 {
				last = path
			}
		}
		s.autoIdentify(last, media.ID, first.imported.ContentFingerprint, first.parsed, "scan: auto-identify failed")
	}
	// The group reports once, after its last part. The resume point stays
	// before the group until then: a resume from a part inside it would
	// re-walk only the later parts and import them as a group of their own,
	// which drops the earlier parts from the Media. One report per group also
	// keeps a group larger than a job's batch from yielding inside it and
	// resuming onto it again forever (CORE-2195).
	for _, file := range files {
		s.result.FilesSeen++
		s.applyTally(file.Path)
		s.result.Files = append(s.result.Files, ImportedFile{
			Path:               file.Path,
			MediaID:            media.ID,
			ContentFingerprint: file.ContentFingerprint,
		})
		if s.result.LastPath == "" || compareWalkOrder(s.req.Root, file.Path, s.result.LastPath) > 0 {
			s.result.LastPath = file.Path
		}
		if !s.result.seenMedia[media.ID] {
			s.result.seenMedia[media.ID] = true
			s.result.MediaImported++
			s.noteGroupMedia(media.ID)
		}
	}
	s.unreportedParts = nil
	s.releaseHeld()
	if s.progress != nil {
		return s.progress(s.resumeCheckpoint(), store.JobProgress{
			Current: int64(s.result.FilesSeen),
			Total:   int64(s.result.totalFiles),
			Unit:    "files",
			Message: "Scanning",
		})
	}
	return nil
}

func appendParsedStructuralMediaFields(fields []store.MediaField, parsed ParsedMedia) []store.MediaField {
	seen := make(map[string]bool, len(fields)+4)
	for _, field := range fields {
		seen[field.Name] = true
	}
	add := func(field store.MediaField) {
		if !seen[field.Name] {
			seen[field.Name] = true
			fields = append(fields, field)
		}
	}
	if parsed.Class != "" {
		add(store.MediaField{Name: "class", Type: store.EntityFieldText, Text: parsed.Class})
	}
	if sequence, err := strconv.ParseFloat(strings.TrimSpace(parsed.Sequence), 64); err == nil && sequence > 0 {
		add(store.MediaField{Name: "sequence", Type: store.EntityFieldNumber, Number: sequence})
	}
	if parsed.Index > 0 {
		add(store.MediaField{Name: "index", Type: store.EntityFieldNumber, Number: float64(parsed.Index)})
	}
	if parsed.IndexEnd > 0 {
		add(store.MediaField{Name: "index-end", Type: store.EntityFieldNumber, Number: float64(parsed.IndexEnd)})
	}
	return fields
}

func (s *scanWalkState) queueParsedPart(imported store.ImportMediaFile, parsed ParsedMedia, refresh store.ParserFieldRefresh, markerTitle string, probeChapters []ffmpeg.ProbeChapter, probed bool, tags map[string]string) error {
	key := s.parsedPartKey(parsed)
	if len(s.pendingParts) > 0 && s.pendingParts[len(s.pendingParts)-1].key != key {
		if err := s.flushPendingParts(); err != nil {
			return err
		}
	}
	s.pendingParts = append(s.pendingParts, parsedPartImport{
		key:           key,
		imported:      imported,
		parsed:        parsed,
		refresh:       refresh,
		markerTitle:   markerTitle,
		probeChapters: append([]ffmpeg.ProbeChapter(nil), probeChapters...),
		probed:        probed,
		tags:          tags,
	})
	return nil
}

func (s *scanWalkState) queueContextRefusal(path string, parsed ParsedMedia) error {
	if err := s.flushPendingParts(); err != nil {
		return err
	}
	recordID, err := s.st.QueueUnmatchedRecordOnce(s.ctx, store.UnmatchedRecord{
		Origin:         store.UnmatchedOriginScan,
		LibraryID:      s.req.LibraryID,
		ImporterID:     s.libraryPluginID,
		SourceHandle:   filepath.Clean(filepath.Dir(path)),
		CandidateTitle: parsed.Title,
		FailureReason:  store.UnmatchedReasonAmbiguous,
	})
	if err != nil {
		return err
	}
	if err := s.noteUnmatchedRecord(recordID); err != nil {
		return err
	}
	// The refusal moves the resume point to path, so every file consumed
	// before it completes first, or a resume would skip it (CORE-2194).
	if err := s.completePending(); err != nil {
		return err
	}
	s.result.FilesSeen++
	s.result.LastPath = path
	if s.progress != nil {
		return s.progress(s.resumeCheckpoint(), store.JobProgress{
			Current: int64(s.result.FilesSeen),
			Total:   int64(s.result.totalFiles),
			Unit:    "files",
			Message: "Scanning",
		})
	}
	return nil
}

// vouchForLegacyScanValues says which older scan-tier values the parser alone
// wrote. Hydration and image local metadata write at the scan tier too: an
// image's are never vouched for, nor any under a hydrator that may write any
// field; a hydrator naming its fields leaves only those out (CORE-2554).
func (s *scanWalkState) vouchForLegacyScanValues(refresh *store.ParserFieldRefresh, path string) {
	if imageClassForPath(path) {
		return
	}
	if s.metadataHydrator == nil {
		refresh.LegacyScanOwned = true
		return
	}
	if !s.metadataCapability.MediaFieldsBounded {
		return
	}
	refresh.LegacyScanOwned = true
	for _, name := range s.metadataCapability.MediaFields {
		if refresh.LegacyScanShared == nil {
			refresh.LegacyScanShared = map[string]bool{}
		}
		refresh.LegacyScanShared[name] = true
	}
}

// parserFieldRefresh is the parser's whole Media field output for one file:
// its declared fields plus the structural ones. When the parse saw a probe,
// the file is parsed again without it: each field that differs is marked
// probe-dependent, and each field only that parse emits probe-absent, so a
// later parse without a probe leaves both alone (CORE-2554).
func (s *scanWalkState) parserFieldRefresh(path string, probe ParseProbe, parsed ParsedMedia, parsedOK bool, fields []store.MediaField, authoritative bool) store.ParserFieldRefresh {
	out := append([]store.MediaField(nil), fields...)
	if parsedOK {
		out = appendParsedStructuralMediaFields(out, parsed)
	}
	refresh := store.ParserFieldRefresh{Fields: out, Authoritative: authoritative}
	s.vouchForLegacyScanValues(&refresh, path)
	if parseProbeEmpty(probe) {
		return refresh
	}
	var unprobed ParsedMedia
	var unprobedOK bool
	if contextual, ok := s.parser.(ContextFilenameParser); ok {
		unprobed, unprobedOK = contextual.ParseWithContext(path, ParseProbe{}, s.directoryContext(path))
	} else {
		unprobed, unprobedOK = s.parser.Parse(path, ParseProbe{})
	}
	baseline := parserMediaFields(s.parser, path, ParseProbe{})
	if unprobedOK {
		baseline = appendParsedStructuralMediaFields(baseline, unprobed)
	}
	byName := make(map[string]store.MediaField, len(baseline))
	for _, field := range baseline {
		byName[field.Name] = field
	}
	absent := make(map[string]bool, len(byName))
	for name := range byName {
		absent[name] = true
	}
	for _, field := range out {
		delete(absent, field.Name)
		if base, ok := byName[field.Name]; ok && sameMediaFieldValue(base, field) {
			continue
		}
		if refresh.ProbeDependent == nil {
			refresh.ProbeDependent = map[string]bool{}
		}
		refresh.ProbeDependent[field.Name] = true
	}
	// What is left the parse without the probe emits and this one does not:
	// the probe decided its absence.
	for name := range absent {
		if refresh.ProbeAbsent == nil {
			refresh.ProbeAbsent = map[string]bool{}
		}
		refresh.ProbeAbsent[name] = true
	}
	return refresh
}

// refreshParserFields applies one parse to the Media's parser-owned fields:
// changed values are replaced and fields the parser no longer emits are
// removed, while fields any other writer holds stay (CORE-2554).
func (s *scanWalkState) refreshParserFields(mediaID store.MediaID, refresh store.ParserFieldRefresh) error {
	if s.parser == nil {
		return nil
	}
	return s.st.RefreshParserMediaFields(s.ctx, mediaID, refresh)
}

func parseProbeEmpty(probe ParseProbe) bool {
	return !probe.HasVideo && !probe.HasAudio && probe.DurationSeconds == 0 && len(probe.Tags) == 0
}

func sameMediaFieldValue(a, b store.MediaField) bool {
	return a.Type == b.Type && a.Text == b.Text && a.Number == b.Number && a.Flag == b.Flag && a.Date.Equal(b.Date)
}
