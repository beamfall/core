package scan

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

const partBoundaryMarkerMethod = "part-boundary"

// groupMarkerMethods are the Marker methods derived from a grouped Media's
// whole Part list. The store drops them when the membership changes.
var groupMarkerMethods = []string{partBoundaryMarkerMethod, "chapter-import"}

func partMarkerTitle(parser FilenameParser, path string, probe ParseProbe) string {
	if titler, ok := parser.(PartMarkerTitler); ok {
		if title := strings.TrimSpace(titler.PartMarkerTitle(path, probe)); title != "" {
			return title
		}
	}
	return titleFromPath(path)
}

// parsedMediaTitle is the Media title for a parsed file: the parser's
// per-file title when it supplies one, else the parsed title.
func parsedMediaTitle(parser FilenameParser, path string, probe ParseProbe, parsedTitle string) string {
	if titler, ok := parser.(MediaTitler); ok {
		if title := strings.TrimSpace(titler.MediaTitle(path, probe)); title != "" {
			return title
		}
	}
	return parsedTitle
}

// ensurePartBoundaryMarkers synthesizes one navigation Marker per ordered Part
// only when the whole grouped Media has no embedded chapters. Any fresh probe,
// including one Part in a mixed full/fast rescan, recomputes the deterministic
// set. An all-fast cheap-triple rescan keeps an already complete set without
// probing; if a scan crashed after importing Parts but before writing Markers,
// the missing set forces a bounded re-probe so resume still converges instead
// of permanently skipping the synthesis. A change in Part membership clears
// both derived sets in the import transaction itself (groupMarkerMethods,
// CORE-3093), so a regrouped Media always takes that re-probe path.
func ensurePartBoundaryMarkers(ctx context.Context, st *store.Store, prober Prober, parser FilenameParser, parts []parsedPartImport, mediaID store.MediaID, mediaDuration int, observedAt time.Time) error {
	anyProbed := false
	for _, part := range parts {
		if part.probed {
			anyProbed = true
			break
		}
	}
	if !anyProbed {
		hintTotal, allHintsKnown := summedPartImportDuration(parts)
		existing, err := st.MarkersForMedia(ctx, mediaID, store.LibraryFilter{AllLibraries: true})
		if err != nil {
			return err
		}
		partMarkers := 0
		embeddedMarkers := 0
		partEnd := 0
		chapterEnd := 0
		for _, marker := range existing {
			endSec := 0
			if marker.EndSeconds != nil {
				endSec = int(math.Round(*marker.EndSeconds))
			}
			switch marker.Method {
			case partBoundaryMarkerMethod:
				partMarkers++
				if endSec > partEnd {
					partEnd = endSec
				}
			case "chapter-import":
				embeddedMarkers++
				if endSec > chapterEnd {
					chapterEnd = endSec
				}
			}
		}
		switch {
		case embeddedMarkers > 0:
			if allHintsKnown && hintTotal > 0 {
				if mediaDuration != hintTotal {
					if _, err := st.SetMultiPartMediaDuration(ctx, mediaID, hintTotal); err != nil {
						return err
					}
				}
				return nil
			}
			if mediaDuration > 0 && (chapterEnd == 0 || mediaDuration >= chapterEnd) {
				return nil
			}
			if prober == nil {
				if chapterEnd > mediaDuration {
					if _, err := st.SetMultiPartMediaDuration(ctx, mediaID, chapterEnd); err != nil {
						return err
					}
				}
				return nil
			}
		case partMarkers == len(parts):
			target := 0
			if allHintsKnown && hintTotal > 0 {
				target = hintTotal
			} else if partEnd > mediaDuration {
				target = partEnd
			}
			if target > 0 && mediaDuration != target {
				if _, err := st.SetMultiPartMediaDuration(ctx, mediaID, target); err != nil {
					return err
				}
			}
			return nil
		}
	}

	prepared := append([]parsedPartImport(nil), parts...)
	hasEmbeddedChapters := false
	for i := range prepared {
		part := &prepared[i]
		if !part.probed {
			if prober == nil {
				return nil
			}
			probeCtx, probeCancel := context.WithTimeout(ctx, probeTimeout)
			probe, err := prober.Probe(probeCtx, part.imported.Path)
			probeCancel()
			if err != nil {
				slog.Warn("scan: part-boundary marker probe failed", "path", part.imported.Path, "error", err.Error())
				return nil
			}
			part.imported.DurationSecondsHint = probe.DurationSeconds
			part.probeChapters = append([]ffmpeg.ProbeChapter(nil), probe.Chapters...)
			part.markerTitle = partMarkerTitle(parser, part.imported.Path, ParseProbe{
				HasVideo:        probe.HasVideo,
				HasAudio:        probe.AudioCodec != "",
				DurationSeconds: probe.DurationSeconds,
				Tags:            probe.Tags,
			})
			part.probed = true
		}
		if len(part.probeChapters) > 0 {
			hasEmbeddedChapters = true
		}
		if part.imported.DurationSecondsHint <= 0 {
			return nil
		}
	}
	if totalDuration, allKnown := summedPartImportDuration(prepared); allKnown && totalDuration > 0 && totalDuration != mediaDuration {
		if _, err := st.SetMultiPartMediaDuration(ctx, mediaID, totalDuration); err != nil {
			return err
		}
	}
	filter := store.LibraryFilter{AllLibraries: true}
	if hasEmbeddedChapters {
		// Embedded chapter atoms are authoritative for the grouped item. Clear
		// only this synthesizer's old rows, then import all embedded chapters
		// with each Part's duration folded into the canonical Media timeline.
		if err := st.ReplaceDetectedMarkers(ctx, mediaID, partBoundaryMarkerMethod, nil, filter, observedAt); err != nil {
			return err
		}
		return st.ReplaceDetectedMarkers(ctx, mediaID, "chapter-import", markersFromPartProbeChapters(prepared), filter, observedAt)
	}
	// A changed grouped item may lose its embedded chapter atoms. Remove stale
	// chapter-import rows before installing the deterministic Part boundaries.
	if err := st.ReplaceDetectedMarkers(ctx, mediaID, "chapter-import", nil, filter, observedAt); err != nil {
		return err
	}

	markers := make([]store.Marker, 0, len(prepared))
	start := 0.0
	for i, part := range prepared {
		end := start + float64(part.imported.DurationSecondsHint)
		title := strings.TrimSpace(part.markerTitle)
		if title == "" {
			title = fmt.Sprintf("Part %d", i+1)
		}
		markers = append(markers, store.Marker{Title: title, StartSeconds: start, EndSeconds: &end})
		start = end
	}
	return st.ReplaceDetectedMarkers(ctx, mediaID, partBoundaryMarkerMethod, markers, filter, observedAt)
}

func summedPartImportDuration(parts []parsedPartImport) (int, bool) {
	if len(parts) == 0 {
		return 0, false
	}
	total := 0
	for _, part := range parts {
		if part.imported.DurationSecondsHint <= 0 {
			return 0, false
		}
		total += part.imported.DurationSecondsHint
	}
	return total, true
}

func markersFromPartProbeChapters(parts []parsedPartImport) []store.Marker {
	var out []store.Marker
	offset := 0.0
	for _, part := range parts {
		for _, chapter := range part.probeChapters {
			end := offset + chapter.EndSeconds
			out = append(out, store.Marker{
				Title:        chapter.Title,
				StartSeconds: offset + chapter.StartSeconds,
				EndSeconds:   &end,
			})
		}
		offset += float64(part.imported.DurationSecondsHint)
	}
	return out
}

func markersFromProbeChapters(chapters []ffmpeg.ProbeChapter) []store.Marker {
	out := make([]store.Marker, 0, len(chapters))
	for _, chapter := range chapters {
		end := chapter.EndSeconds
		out = append(out, store.Marker{
			Title:        chapter.Title,
			StartSeconds: chapter.StartSeconds,
			EndSeconds:   &end,
		})
	}
	return out
}
