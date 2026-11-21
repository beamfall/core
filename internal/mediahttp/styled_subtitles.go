package mediahttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/playback/subtitleingest"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

type styledSubtitleConverter interface {
	ExtractStyledSubtitle(context.Context, string, int) ([]byte, error)
	SubtitleFontIndexes(context.Context, string) ([]int, error)
	ExtractSubtitleFont(context.Context, string, int) ([]byte, error)
}

func (h *Handler) serveStyledSubtitle(w http.ResponseWriter, r *http.Request, grant streamtoken.Grant, token string, library store.Library, file store.MediaFile, resource string) {
	// Styled extraction opens the original through the local-file runner.
	// Encrypted sources keep the encrypted WebVTT delivery path.
	converter, ok := h.subtitles.(styledSubtitleConverter)
	if !ok || library.Encrypted || grant.SubtitleFileFingerprint == "" || grant.SubtitleStreamIndex == 0 {
		http.NotFound(w, r)
		return
	}
	streams, err := h.store.MediaFileStreams(r.Context(), file.ContentFingerprint)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	streamIndex := grant.SubtitleStreamIndex - 1
	styled := false
	for _, stream := range streams {
		if stream.Index == streamIndex && stream.Type == "subtitle" {
			styled = strings.EqualFold(stream.Codec, "ass") || strings.EqualFold(stream.Codec, "ssa")
			break
		}
	}
	if !styled {
		http.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var data []byte
	contentType := "text/x-ssa; charset=utf-8"
	switch resource {
	case "":
		data, err = converter.ExtractStyledSubtitle(ctx, file.Path, streamIndex)
		if err == nil {
			err = subtitleingest.Guard("ass", data)
		}
	case "fonts":
		contentType = "application/json"
		var indexes []int
		indexes, err = converter.SubtitleFontIndexes(ctx, file.Path)
		if err == nil {
			data, err = json.Marshal(indexes)
		}
	default:
		attachment, ok := strings.CutPrefix(resource, "fonts/")
		index, parseErr := strconv.Atoi(attachment)
		if !ok || parseErr != nil || index < 0 {
			http.NotFound(w, r)
			return
		}
		contentType = "application/octet-stream"
		data, err = converter.ExtractSubtitleFont(ctx, file.Path, index)
	}
	if errors.Is(err, ffmpeg.ErrFontAttachmentNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "subtitle unavailable", http.StatusServiceUnavailable)
		return
	}
	// A file read can outlive a relock or a change to the viewer's library scope.
	_, scope, err := h.verifyMediaGrant(r.Context(), token, streamtoken.TargetMediaSubtitle)
	if err != nil {
		http.Error(w, "stream ended", h.endedSubtitleStatus(r.Context(), token))
		return
	}
	if h.sealed != nil && h.sealed(library.ID) ||
		h.subtitleSourceStillVisible(r.Context(), scope, grant.MediaID, library.ID, file.ContentFingerprint) != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}
