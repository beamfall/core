package scan

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

func (s *scanWalkState) consumeTask(task *workerTask) error {
	if task.err != nil {
		return task.err
	}
	path := task.path
	if decision, err := s.preflightDirectory(path); err != nil {
		return err
	} else if decision != nil && decision.refused {
		return s.queueContextRefusal(path, decision.refusal)
	} else if decision.skipsStillImage(path) {
		// A catalogued image this scan now leaves out goes missing here
		// rather than in the guarded end-of-scan pass, which it would
		// otherwise count against (CORE-1833). An image never catalogued
		// matches no row.
		if err := s.st.MarkMediaFileMissing(s.ctx, s.req.LibraryID, path); err != nil {
			return err
		}
		return s.ctx.Err()
	}
	if err := s.refreshMovedRow(task); err != nil {
		return err
	}
	if task.fastPath {
		return s.consumeFastTask(task)
	}
	return s.consumeFullTask(task)
}

// refreshMovedRow looks task's file row up again when this walk has imported
// the same bytes at another path since the worker's lookup. The catalog keeps
// one file row per content fingerprint, so that import took the row the worker
// may have read, and a worker that read it first would send the file down the
// fast path, or count it as changed, where a sequential walk imports and
// counts it as added. Which of the two a parallel scan reported depended on
// worker timing (CORE-2210).
func (s *scanWalkState) refreshMovedRow(task *workerTask) error {
	if !task.stored.OK || !s.movedFingerprints[task.stored.ContentFingerprint] {
		return nil
	}
	fastPath := task.fastPath
	stored, err := s.st.MediaFileFingerprintByPath(s.ctx, task.path)
	if err != nil {
		return err
	}
	task.stored = stored
	if !fastPath || stored.OK {
		return nil
	}
	// The worker skipped the hashes and the probe for a row the file no
	// longer has, so the file is filled again as the new file it now is.
	s.fill(s.ctx, task)
	return task.err
}

func (s *scanWalkState) consumeFastTask(task *workerTask) error {
	path := task.path
	stored := task.stored
	imported := store.ImportMediaFile{
		LibraryID:          s.req.LibraryID,
		Path:               path,
		Title:              titleFromPath(path),
		Container:          containerFromPath(path),
		Codec:              "h264",
		ContentFingerprint: stored.ContentFingerprint,
		SizeBytes:          stored.SizeBytes,
		ModTime:            stored.ModTime,
		Extra:              isExtraPath(s.extrasRoot, path),
		ExtraFromPath:      true,
		Sidecars:           s.sidecars.discover(path),
	}
	if imageClassForPath(path) {
		imported.Class = store.MediaClassImage
		imported.Codec = ""
	}
	// Without a probe the import carries only path-derived placeholders, so
	// the Media keeps its recorded probe facts. A file probed here (legacy
	// parser fields, or a Media without a duration) refreshes them.
	if task.probe.succeeded {
		applyProbeFacts(&imported, task.probe.value)
	} else {
		imported.KeepProbeFacts = true
	}
	var parsed ParsedMedia
	var parsedFields []store.MediaField
	parsedOK := false
	var refresh store.ParserFieldRefresh
	// The fast path skipped the probe, so the parse sees the directory
	// preflight's probe or, for a file with legacy parser fields, the one-off
	// probe fill took (CORE-2554). Only a parse that saw a real probe, or one
	// no probe could change, settles probe-dependent fields; a scan without a
	// prober never does, so it cannot undo an earlier probed parse.
	parseProbe := ParseProbe{}
	authoritative := imported.Class == store.MediaClassImage
	if decision := s.directoryPreflights[filepath.Dir(path)]; decision != nil {
		parseProbe = decision.probes[path]
		authoritative = authoritative || !parseProbeEmpty(parseProbe)
	} else if task.probe.succeeded {
		parseProbe = ParseProbe{
			HasVideo:        task.probe.hasVideo,
			HasAudio:        task.probe.hasAudio,
			DurationSeconds: task.probe.value.DurationSeconds,
			Tags:            task.probe.value.Tags,
		}
		authoritative = true
	}
	if s.parser != nil {
		var refused bool
		parsed, parsedOK, refused = s.parseFilename(path, parseProbe)
		if refused {
			return s.queueContextRefusal(path, parsed)
		}
		parsedFields = parserMediaFields(s.parser, path, parseProbe)
		if parsedOK {
			imported.Title = parsedMediaTitle(s.parser, path, parseProbe, parsed.Title)
			imported.Year = parsed.Year
		}
		refresh = s.parserFieldRefresh(path, parseProbe, parsed, parsedOK, parsedFields, authoritative)
	}
	// A file with extracted local metadata imports on its own, like a still:
	// a queued part or an attached version would drop it.
	standalone := imported.Class == store.MediaClassImage || len(task.local) > 0
	if !standalone && s.isParsedPart(parsed, parsedOK) {
		if err := s.queueParsedPart(imported, parsed, refresh, partMarkerTitle(s.parser, path, parseProbe), task.probe.value.Chapters, task.probe.succeeded, false, nil); err != nil {
			return err
		}
		return s.ctx.Err()
	}
	if err := s.flushPendingPartsUnlessImage(imported.Class == store.MediaClassImage); err != nil {
		return err
	}
	versionKey := ""
	if !standalone {
		versionKey = s.versionGroupKey(imported, parsed, parsedOK)
	}
	if mediaID, attached, err := s.attachVersion(versionKey, imported, parsed); err != nil || attached {
		if err != nil {
			return err
		}
		if err := s.queueCompletion(path, mediaID, stored.ContentFingerprint, false); err != nil {
			return err
		}
		return s.ctx.Err()
	}
	media, _, err := s.st.ImportMediaFile(s.ctx, imported)
	if err != nil {
		return skipManagedAdmissionInFlight(err, path)
	}
	s.rememberVersionAnchor(versionKey, media.ID, stored.ContentFingerprint, parsed)
	if err := s.refreshParserFields(media.ID, refresh); err != nil {
		return err
	}
	if len(task.local) > 0 {
		if err := applyLocalMetadata(s.ctx, s.st, s.req.LibraryID, media.ID, task.local); err != nil {
			return err
		}
	}
	if err := s.retryAutoIdentify(path, media.ID, stored.ContentFingerprint, parsed); err != nil {
		return err
	}
	s.noteVersion(versionKey, path, media.ID)
	if err := s.backfillHydration(media, path, stored.ContentFingerprint); err != nil {
		return err
	}
	if err := s.queueCompletion(path, media.ID, stored.ContentFingerprint, imported.Class == store.MediaClassImage); err != nil {
		return err
	}
	return s.ctx.Err()
}

// retryAutoIdentify gives an unchanged file another auto-identify pass when it
// was never identified: no identification or manual field writes and no row in
// the Unmatched queue. That is the state a failed attempt leaves (a missing
// provider credential, a network error), and without this a rescan's fast path
// would never revisit it. Pinned matches are left alone inside AutoIdentify.
func (s *scanWalkState) retryAutoIdentify(path string, mediaID store.MediaID, fingerprint string, parsed ParsedMedia) error {
	if s.identifier == nil || s.libraryPluginID == "" {
		return nil
	}
	curated, err := s.st.MediaHasCuratedFields(s.ctx, mediaID)
	if err != nil || curated {
		return err
	}
	queued, err := s.st.IsUnmatched(s.ctx, mediaID)
	if err != nil {
		return err
	}
	if queued {
		s.noteFile(path, func(t *fileTally) { t.unmatched = true })
		return nil
	}
	s.autoIdentify(path, mediaID, fingerprint, parsed, "scan: auto-identify retry failed")
	return nil
}

// A changed timestamp alone cannot replace a legacy identity. Compare the
// digest whenever the cheap path missed and the bytes could still match.
func (task *workerTask) legacyComparison(ctx context.Context, reservations []store.StoredFingerprint) store.SampleFingerprintCheck {
	eligible := func(fp store.StoredFingerprint) bool {
		return (fp.SizeBytes == 0 || fp.SizeBytes == task.info.size) && strings.HasPrefix(fp.ContentFingerprint, "sha256:")
	}
	if !eligible(task.stored) && !slices.ContainsFunc(reservations, eligible) {
		return nil
	}
	return func(file io.Reader, sample string) (string, error) {
		full, err := store.HashMediaFileContentReader(ctx, file)
		if err != nil || (eligible(task.stored) && full == task.stored.ContentFingerprint) || slices.ContainsFunc(reservations, func(fp store.StoredFingerprint) bool {
			return eligible(fp) && full == fp.ContentFingerprint
		}) {
			return full, err
		}
		return sample, nil
	}
}

func (s *scanWalkState) consumeFullTask(task *workerTask) error {
	path := task.path
	fingerprint := task.fingerprint
	imported := store.ImportMediaFile{
		LibraryID:          s.req.LibraryID,
		Path:               path,
		Title:              titleFromPath(path),
		Container:          containerFromPath(path),
		Codec:              "h264",
		ContentFingerprint: fingerprint,
		OSHash:             task.oshash,
		SizeBytes:          task.info.size,
		ModTime:            task.info.modTime.UTC(),
		Extra:              isExtraPath(s.extrasRoot, path),
		ExtraFromPath:      true,
		Sidecars:           s.sidecars.discover(path),
	}
	isImage := imageClassForPath(path)
	if isImage {
		imported.Class = store.MediaClassImage
		imported.Codec = ""
	}
	hasVideo := task.probe.hasVideo
	hasAudio := task.probe.hasAudio
	probeSucceeded := task.probe.succeeded
	imported.KeepProbeFacts = !probeSucceeded && !isImage
	var probeTags map[string]string
	var probeChapters []ffmpeg.ProbeChapter
	if probeSucceeded {
		probe := task.probe.value
		applyProbeFacts(&imported, probe)
		probeTags = probe.Tags
		probeChapters = probe.Chapters
	}
	if task.phash != "" {
		imported.PHash = task.phash
	}
	var parsed ParsedMedia
	var parsedFields []store.MediaField
	var refresh store.ParserFieldRefresh
	parsedOK := false
	parseProbe := ParseProbe{
		HasVideo:        hasVideo,
		HasAudio:        hasAudio,
		DurationSeconds: imported.DurationSecondsHint,
		Tags:            probeTags,
	}
	authoritative := probeSucceeded || isImage
	// A folder preflight parsed the file with its own probe, so the parse and
	// its field ownership follow that probe, not the worker's (CORE-2554).
	if decision := s.directoryPreflights[filepath.Dir(path)]; decision != nil {
		parseProbe = decision.probes[path]
		authoritative = isImage || !parseProbeEmpty(parseProbe)
	}
	if s.parser != nil {
		var refused bool
		parsed, parsedOK, refused = s.parseFilename(path, parseProbe)
		if refused {
			return s.queueContextRefusal(path, parsed)
		}
		parsedFields = parserMediaFields(s.parser, path, parseProbe)
		if parsedOK {
			imported.Title = parsedMediaTitle(s.parser, path, parseProbe, parsed.Title)
			imported.Year = parsed.Year
		}
		refresh = s.parserFieldRefresh(path, parseProbe, parsed, parsedOK, parsedFields, authoritative)
	}
	// The fast path took every file whose size and mtime are unchanged, so a
	// file here is new to the catalog or changed on disk (CORE-2040).
	s.noteFile(path, func(t *fileTally) {
		if task.stored.OK {
			t.changed = true
		} else {
			t.added = true
		}
	})
	// A file with extracted local metadata imports on its own, like a still:
	// a queued part or an attached version would drop it.
	standalone := isImage || len(task.local) > 0
	if !standalone && s.isParsedPart(parsed, parsedOK) {
		if err := s.queueParsedPart(imported, parsed, refresh, partMarkerTitle(s.parser, path, parseProbe), probeChapters, probeSucceeded, probeSucceeded, probeTags); err != nil {
			return err
		}
		return s.ctx.Err()
	}
	if err := s.flushPendingPartsUnlessImage(standalone); err != nil {
		return err
	}
	versionKey := ""
	if !standalone {
		versionKey = s.versionGroupKey(imported, parsed, parsedOK)
	}
	if mediaID, attached, err := s.attachVersion(versionKey, imported, parsed); err != nil || attached {
		if err != nil {
			return err
		}
		if err := s.queueCompletion(path, mediaID, fingerprint, false); err != nil {
			return err
		}
		return s.ctx.Err()
	}
	// A file whose own row already holds these bytes takes no row from
	// another file, so only other bytes can move a counted file's row.
	if !task.stored.OK || task.stored.ContentFingerprint != fingerprint {
		if err := s.keepCountedMediaBeforeMove(path, fingerprint); err != nil {
			s.dropTallies(path)
			return err
		}
	}
	media, _, err := s.st.ImportMediaFile(s.ctx, imported)
	if err != nil {
		s.dropTallies(path)
		return skipManagedAdmissionInFlight(err, path)
	}
	if !task.stored.OK || task.stored.ContentFingerprint != fingerprint {
		if s.movedFingerprints == nil {
			s.movedFingerprints = map[string]bool{}
		}
		s.movedFingerprints[fingerprint] = true
	}
	s.committed = path
	s.rememberVersionAnchor(versionKey, media.ID, fingerprint, parsed)
	if len(task.local) > 0 {
		if err := applyLocalMetadata(s.ctx, s.st, s.req.LibraryID, media.ID, task.local); err != nil {
			return err
		}
	}
	if err := s.hydrateMetadata(media, path, fingerprint, probeTags); err != nil {
		return err
	}
	if err := s.refreshParserFields(media.ID, refresh); err != nil {
		return err
	}
	if len(probeChapters) > 0 {
		if err := s.st.ReplaceDetectedMarkers(s.ctx, media.ID, "chapter-import", markersFromProbeChapters(probeChapters), store.LibraryFilter{AllLibraries: true}, now()); err != nil {
			return err
		}
	}
	if s.identifier != nil && s.libraryPluginID != "" {
		s.autoIdentify(path, media.ID, fingerprint, parsed, "scan: auto-identify failed")
	}
	s.noteVersion(versionKey, path, media.ID)
	if err := s.queueCompletion(path, media.ID, fingerprint, isImage); err != nil {
		return err
	}
	return s.ctx.Err()
}

// applyProbeFacts copies a successful probe's container, codec, class,
// duration, frame size and stream inventory onto imported.
func applyProbeFacts(imported *store.ImportMediaFile, probe ffmpeg.Probe) {
	if probe.Container != "" {
		imported.Container = probe.Container
	}
	if probe.VideoCodec != "" {
		imported.Codec = probe.VideoCodec
	}
	if !probe.HasVideo && probe.AudioCodec != "" {
		imported.Class = store.MediaClassAudio
		imported.Codec = probe.AudioCodec
	}
	imported.DurationSecondsHint = probe.DurationSeconds
	imported.VideoWidthHint = probe.VideoWidth
	imported.VideoHeightHint = probe.VideoHeight
	imported.Streams = streamsFromProbe(probe.Streams)
	imported.FontAttachments = fontsFromProbe(probe.FontAttachments)
}

func (s *scanWalkState) hydrateMetadata(media store.Media, path, fingerprint string, metadata map[string]string) error {
	if s.metadataHydrator == nil {
		return nil
	}
	if !s.metadataCapability.Supports(media.Class) {
		return nil
	}
	return s.metadataHydrator.HydrateMetadata(s.ctx, MetadataHydrationInput{
		MediaID:            media.ID,
		LibraryID:          s.req.LibraryID,
		Class:              media.Class,
		Path:               path,
		Metadata:           metadata,
		ContentFingerprint: fingerprint,
	})
}

// backfillHydration hydrates an unchanged file's Media once when the
// one-time backfill found its marker missing.
func (s *scanWalkState) backfillHydration(media store.Media, path, fingerprint string) error {
	if _, stale := s.backfillStale[media.ID]; !stale || s.metadataHydrator == nil || s.prober == nil {
		return nil
	}
	if media.Class == store.MediaClassImage {
		return s.hydrateMetadata(media, path, fingerprint, nil)
	}
	probe, err := s.prober.Probe(s.ctx, path)
	if err != nil {
		slog.Warn("scan: metadata backfill probe failed, skipping hydration", "path", path, "error", err.Error())
		return nil
	}
	media.Class = mediaClassFromProbe(probe)
	return s.hydrateMetadata(media, path, fingerprint, probe.Tags)
}

func mediaClassFromProbe(probe ffmpeg.Probe) store.MediaClass {
	if !probe.HasVideo && probe.AudioCodec != "" {
		return store.MediaClassAudio
	}
	return store.MediaClassVideo
}

// skipManagedAdmissionInFlight turns the store's refusal of a path that a
// managed import is still admitting into a skip: that admission catalogs the
// file itself (CORE-1089). Any other error is returned unchanged.
func skipManagedAdmissionInFlight(err error, path string) error {
	if errors.Is(err, store.ErrManagedAdmissionInFlight) {
		slog.Info("scan: skipping a file a managed import is admitting", "path", path)
		return nil
	}
	return err
}
