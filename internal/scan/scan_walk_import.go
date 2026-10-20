package scan

import (
	"log/slog"
	"path/filepath"

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
	}
	if task.fastPath {
		return s.consumeFastTask(task)
	}
	return s.consumeFullTask(task)
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
		SizeBytes:          task.info.size,
		ModTime:            task.info.modTime.UTC(),
		Extra:              isExtraPath(path),
		Sidecars:           s.sidecars.discover(path),
	}
	if imageClassForPath(path) {
		imported.Class = store.MediaClassImage
		imported.Codec = ""
	}
	var parsed ParsedMedia
	var parsedFields []store.MediaField
	parsedOK := false
	parseProbe := ParseProbe{}
	if decision := s.directoryPreflights[filepath.Dir(path)]; decision != nil {
		parseProbe = decision.probes[path]
	}
	if s.parser != nil {
		var refused bool
		parsed, parsedOK, refused = s.parseFilename(path, parseProbe)
		if refused {
			return s.queueContextRefusal(path, parsed)
		}
		parsedFields = parserMediaFields(s.parser, path, parseProbe)
		if parsedOK {
			imported.Title = parsed.Title
			imported.Year = parsed.Year
		}
	}
	if s.isParsedPart(parsed, parsedOK) {
		if err := s.queueParsedPart(imported, parsed, parsedFields, partMarkerTitle(s.parser, path, parseProbe), nil, false); err != nil {
			return err
		}
		return s.ctx.Err()
	}
	if err := s.flushPendingParts(); err != nil {
		return err
	}
	media, _, err := s.st.ImportMediaFile(s.ctx, imported)
	if err != nil {
		return err
	}
	fields := append([]store.MediaField(nil), parsedFields...)
	if parsedOK {
		fields = appendParsedStructuralMediaFields(fields, parsed)
	}
	if len(fields) > 0 {
		if err := s.st.InsertMediaFieldsIfAbsent(s.ctx, media.ID, fields); err != nil {
			return err
		}
	}
	if imported.Class == store.MediaClassImage {
		if err := applyLocalMetadata(s.ctx, s.st, s.req.LibraryID, media.ID, task.local); err != nil {
			return err
		}
	}
	if _, stale := s.backfillStale[media.ID]; stale && s.metadataHydrator != nil && s.prober != nil {
		if media.Class == store.MediaClassImage {
			if err := s.hydrateMetadata(media, path, nil); err != nil {
				return err
			}
		} else if probe, probeErr := s.prober.Probe(s.ctx, path); probeErr != nil {
			slog.Warn("scan: metadata backfill probe failed, skipping hydration", "path", path, "error", probeErr.Error())
		} else {
			media.Class = mediaClassFromProbe(probe)
			if err := s.hydrateMetadata(media, path, probe.Tags); err != nil {
				return err
			}
		}
	}
	if err := s.queueCompletion(path, media.ID, stored.ContentFingerprint, imported.Class == store.MediaClassImage); err != nil {
		return err
	}
	return s.ctx.Err()
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
		Extra:              isExtraPath(path),
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
	var probeTags map[string]string
	var probeChapters []ffmpeg.ProbeChapter
	if probeSucceeded {
		probe := task.probe.value
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
		probeTags = probe.Tags
		probeChapters = probe.Chapters
	}
	if task.phash != "" {
		imported.PHash = task.phash
	}
	var parsed ParsedMedia
	var parsedFields []store.MediaField
	parsedOK := false
	parseProbe := ParseProbe{
		HasVideo:        hasVideo,
		HasAudio:        hasAudio,
		DurationSeconds: imported.DurationSecondsHint,
		Tags:            probeTags,
	}
	if s.parser != nil {
		var refused bool
		parsed, parsedOK, refused = s.parseFilename(path, parseProbe)
		if refused {
			return s.queueContextRefusal(path, parsed)
		}
		parsedFields = parserMediaFields(s.parser, path, parseProbe)
		if parsedOK {
			imported.Title = parsed.Title
			imported.Year = parsed.Year
		}
	}
	if s.isParsedPart(parsed, parsedOK) {
		if err := s.queueParsedPart(imported, parsed, parsedFields, partMarkerTitle(s.parser, path, parseProbe), probeChapters, probeSucceeded); err != nil {
			return err
		}
		return s.ctx.Err()
	}
	if err := s.flushPendingParts(); err != nil {
		return err
	}
	media, _, err := s.st.ImportMediaFile(s.ctx, imported)
	if err != nil {
		return err
	}
	if isImage {
		if err := applyLocalMetadata(s.ctx, s.st, s.req.LibraryID, media.ID, task.local); err != nil {
			return err
		}
	}
	if err := s.hydrateMetadata(media, path, probeTags); err != nil {
		return err
	}
	fields := append([]store.MediaField(nil), parsedFields...)
	if parsedOK {
		fields = appendParsedStructuralMediaFields(fields, parsed)
	}
	if len(fields) > 0 {
		if err := s.st.InsertMediaFieldsIfAbsent(s.ctx, media.ID, fields); err != nil {
			return err
		}
	}
	if len(probeChapters) > 0 {
		if err := s.st.ReplaceDetectedMarkers(s.ctx, media.ID, "chapter-import", markersFromProbeChapters(probeChapters), store.LibraryFilter{AllLibraries: true}, now()); err != nil {
			return err
		}
	}
	if s.identifier != nil && s.libraryPluginID != "" {
		if err := s.identifier.AutoIdentify(s.ctx, media.ID, s.libraryPluginID, fingerprint, parsed); err != nil {
			slog.Warn("scan: auto-identify failed", "path", path, "media", string(media.ID), "error", err.Error())
		}
	}
	if err := s.queueCompletion(path, media.ID, fingerprint, isImage); err != nil {
		return err
	}
	return s.ctx.Err()
}

func (s *scanWalkState) hydrateMetadata(media store.Media, path string, metadata map[string]string) error {
	if s.metadataHydrator == nil {
		return nil
	}
	if !s.metadataCapability.Supports(media.Class) {
		return nil
	}
	return s.metadataHydrator.HydrateMetadata(s.ctx, MetadataHydrationInput{
		MediaID:   media.ID,
		LibraryID: s.req.LibraryID,
		Class:     media.Class,
		Path:      path,
		Metadata:  metadata,
	})
}

func mediaClassFromProbe(probe ffmpeg.Probe) store.MediaClass {
	if !probe.HasVideo && probe.AudioCodec != "" {
		return store.MediaClassAudio
	}
	return store.MediaClassVideo
}
