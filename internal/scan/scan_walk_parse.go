package scan

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

type contextualDirectoryPreflight struct {
	probes   map[string]ParseProbe
	parsed   map[string]ParsedMedia
	accepted map[string]bool
	refused  bool
	refusal  ParsedMedia
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
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		candidate := filepath.Join(dir, entry.Name())
		if !s.isSupportedMediaFile(candidate, info) {
			continue
		}
		parseProbe := ParseProbe{}
		if s.prober != nil && !imageClassForPath(candidate) {
			probeCtx, cancel := context.WithTimeout(s.ctx, probeTimeout)
			probe, probeErr := s.prober.Probe(probeCtx, candidate)
			cancel()
			if probeErr == nil {
				parseProbe = ParseProbe{HasVideo: probe.HasVideo, HasAudio: probe.AudioCodec != "", DurationSeconds: probe.DurationSeconds, Tags: probe.Tags}
			}
		}
		decision.probes[candidate] = parseProbe
		legacy, legacyOK := s.parser.Parse(candidate, parseProbe)
		parsed, accepted := contextual.ParseWithContext(candidate, parseProbe, s.directoryContext(candidate))
		decision.parsed[candidate], decision.accepted[candidate] = parsed, accepted
		if legacyOK && !accepted && !decision.refused {
			decision.refused, decision.refusal = true, legacy
		}
	}
	s.directoryPreflights[dir] = decision
	return decision, nil
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

func (s *scanWalkState) flushPendingParts() error {
	if len(s.pendingParts) == 0 {
		return nil
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
		return err
	}
	first := parts[0]
	fields := append([]store.MediaField(nil), first.fields...)
	fields = appendParsedStructuralMediaFields(fields, first.parsed)
	if len(fields) > 0 {
		if err := s.st.InsertMediaFieldsIfAbsent(s.ctx, media.ID, fields); err != nil {
			return err
		}
	}
	if err := ensurePartBoundaryMarkers(s.ctx, s.st, s.prober, parts, media.ID, now()); err != nil {
		return err
	}
	if s.identifier != nil && s.libraryPluginID != "" {
		if err := s.identifier.AutoIdentify(s.ctx, media.ID, s.libraryPluginID, first.imported.ContentFingerprint, first.parsed); err != nil {
			slog.Warn("scan: auto-identify failed", "path", first.imported.Path, "media", string(media.ID), "error", err.Error())
		}
	}
	for _, file := range files {
		s.result.FilesSeen++
		s.result.Files = append(s.result.Files, ImportedFile{
			Path:               file.Path,
			MediaID:            media.ID,
			ContentFingerprint: file.ContentFingerprint,
		})
		if file.Path > s.result.LastPath {
			s.result.LastPath = file.Path
		}
		if !s.result.seenMedia[media.ID] {
			s.result.seenMedia[media.ID] = true
			s.result.MediaImported++
		}
		if s.progress != nil {
			if err := s.progress(s.result.checkpoint("scanning"), store.JobProgress{
				Current: int64(s.result.FilesSeen),
				Total:   int64(s.result.totalFiles),
				Unit:    "files",
				Message: "Scanning",
			}); err != nil {
				return err
			}
		}
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

func (s *scanWalkState) queueParsedPart(imported store.ImportMediaFile, parsed ParsedMedia, fields []store.MediaField, markerTitle string, probeChapters []ffmpeg.ProbeChapter, probed bool) error {
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
		fields:        fields,
		markerTitle:   markerTitle,
		probeChapters: append([]ffmpeg.ProbeChapter(nil), probeChapters...),
		probed:        probed,
	})
	return nil
}

func (s *scanWalkState) queueContextRefusal(path string, parsed ParsedMedia) error {
	if err := s.flushPendingParts(); err != nil {
		return err
	}
	if _, err := s.st.QueueUnmatchedRecordOnce(s.ctx, store.UnmatchedRecord{
		Origin:         store.UnmatchedOriginScan,
		LibraryID:      s.req.LibraryID,
		ImporterID:     s.libraryPluginID,
		SourceHandle:   filepath.Clean(filepath.Dir(path)),
		CandidateTitle: parsed.Title,
		FailureReason:  store.UnmatchedReasonAmbiguous,
	}); err != nil {
		return err
	}
	s.result.FilesSeen++
	s.result.LastPath = path
	if s.progress != nil {
		return s.progress(s.result.checkpoint("scanning"), store.JobProgress{
			Current: int64(s.result.FilesSeen),
			Total:   int64(s.result.totalFiles),
			Unit:    "files",
			Message: "Scanning",
		})
	}
	return nil
}
