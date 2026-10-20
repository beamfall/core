package scan

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

const partBoundaryMarkerMethod = "part-boundary"

func partMarkerTitle(parser FilenameParser, path string, probe ParseProbe) string {
	if titler, ok := parser.(PartMarkerTitler); ok {
		if title := strings.TrimSpace(titler.PartMarkerTitle(path, probe)); title != "" {
			return title
		}
	}
	return titleFromPath(path)
}

// ensurePartBoundaryMarkers synthesizes one navigation Marker per ordered Part
// only when the whole grouped Media has no embedded chapters. Any fresh probe,
// including one Part in a mixed full/fast rescan, recomputes the deterministic
// set. An all-fast cheap-triple rescan keeps an already complete set without
// probing; if a scan crashed after importing Parts but before writing Markers,
// the missing set forces a bounded re-probe so resume still converges instead
// of permanently skipping the synthesis.
func ensurePartBoundaryMarkers(ctx context.Context, st *store.Store, prober Prober, parts []parsedPartImport, mediaID store.MediaID, observedAt time.Time) error {
	anyProbed := false
	for _, part := range parts {
		if part.probed {
			anyProbed = true
			break
		}
	}
	if !anyProbed {
		existing, err := st.MarkersForMedia(ctx, mediaID, store.LibraryFilter{AllLibraries: true})
		if err != nil {
			return err
		}
		partMarkers := 0
		embeddedMarkers := 0
		for _, marker := range existing {
			switch marker.Method {
			case partBoundaryMarkerMethod:
				partMarkers++
			case "chapter-import":
				embeddedMarkers++
			}
		}
		if partMarkers == len(parts) || embeddedMarkers > 0 {
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
			part.probed = true
		}
		if len(part.probeChapters) > 0 {
			hasEmbeddedChapters = true
		}
		if part.imported.DurationSecondsHint <= 0 {
			return nil
		}
	}
	filter := store.LibraryFilter{AllLibraries: true}
	if hasEmbeddedChapters {
		// Embedded chapter atoms are authoritative for the grouped book. Clear
		// only this synthesizer's old rows, then import all embedded chapters
		// with each Part's duration folded into the canonical Media timeline.
		if err := st.ReplaceDetectedMarkers(ctx, mediaID, partBoundaryMarkerMethod, nil, filter, observedAt); err != nil {
			return err
		}
		return st.ReplaceDetectedMarkers(ctx, mediaID, "chapter-import", markersFromPartProbeChapters(prepared), filter, observedAt)
	}
	// A changed grouped book may lose its embedded chapter atoms. Remove stale
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
