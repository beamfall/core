package graph

import (
	"context"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/lyrics"
	"github.com/beamfall/core/internal/store"
)

// lyricsFieldName is the text Media field a plugin hydrates lyrics into.
const lyricsFieldName = "lyrics"

// MediaLyricsInfo is mediaLyrics(mediaID): the lyrics of an audio Media item.
// Synced lyrics carry a startMs on every line, ascending. Source is
// "sidecar-lrc", "embedded", "metadata", "sidecar-txt", or "" with no lines.
type MediaLyricsInfo struct {
	Synced bool             `json:"synced"`
	Source string           `json:"source"`
	Lines  []MediaLyricLine `json:"lines"`
}

// MediaLyricLine is one lyric line; startMs is set only for synced lyrics.
type MediaLyricLine struct {
	StartMs *int   `json:"startMs,omitempty"`
	Text    string `json:"text"`
}

// mediaLyrics resolves mediaLyrics(mediaID), read at request time from the
// item's primary file, its same-name sidecars and its lyrics field (CORE-1638).
// Non-audio items answer no lines without touching the file system.
func (h *Handler) mediaLyrics(ctx context.Context, scope auth.Scope, req request) (MediaLyricsInfo, error) {
	empty := MediaLyricsInfo{Lines: []MediaLyricLine{}}
	mediaID := store.MediaID(trimmedVariable(req, "mediaID"))
	if mediaID == "" {
		return empty, clientError("mediaID is required")
	}
	media, err := h.store.Media(ctx, mediaID, scope.LibraryFilter())
	if err != nil {
		return empty, clientError("unknown media")
	}
	if media.Class != store.MediaClassAudio {
		return empty, nil
	}
	files, err := h.store.MediaFilesForMedia(ctx, mediaID, scope.LibraryFilter())
	if err != nil {
		return empty, clientError("unknown media")
	}
	doc, source := lyrics.Resolve(primaryMediaPath(files), h.lyricsFieldText(ctx, mediaID, scope))
	info := MediaLyricsInfo{Synced: doc.Synced, Source: source, Lines: make([]MediaLyricLine, 0, len(doc.Lines))}
	for _, line := range doc.Lines {
		info.Lines = append(info.Lines, MediaLyricLine{StartMs: line.StartMs, Text: line.Text})
	}
	return info, nil
}

// primaryMediaPath picks the first Part of the default Version, else the
// first file.
func primaryMediaPath(files []store.MediaFile) string {
	for _, file := range files {
		if file.VersionLabel == "" && file.PartOrdinal == 0 {
			return file.Path
		}
	}
	if len(files) > 0 {
		return files[0].Path
	}
	return ""
}

// lyricsFieldText returns the item's lyrics field text unless the owning
// plugin marks the field Sensitive.
func (h *Handler) lyricsFieldText(ctx context.Context, mediaID store.MediaID, scope auth.Scope) string {
	fields, err := h.store.MediaFields(ctx, mediaID, scope.LibraryFilter())
	if err != nil {
		return ""
	}
	for _, field := range fields {
		if field.Name != lyricsFieldName || field.Type != store.EntityFieldText {
			continue
		}
		if h.mediaFieldSpecs(ctx, mediaID)[lyricsFieldName].Sensitive {
			return ""
		}
		return field.Text
	}
	return ""
}
