package mediahttp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Online subtitle search (CORE-1690). The handler owns access, visibility and
// the sidecar write; the provider work sits behind SubtitleSearcher, which
// composition adapts from internal/subtitlesearch. A nil searcher (the
// default) means the operator has not enabled any provider: the status route
// reports that and the search and download routes refuse, so the server makes
// no outbound request.

// SubtitleSearchQuery is what the handler knows about the Media: the primary
// file's OpenSubtitles-style hash and size, plus generic title, year and the
// ordered position (sequence, index) plugins contributed. Zero means unknown.
type SubtitleSearchQuery struct {
	OSHash    string
	ByteSize  int64
	Title     string
	Year      int
	Sequence  int
	Index     int
	Languages []string
}

// SubtitleSearchResult is one downloadable candidate, already in wire shape.
type SubtitleSearchResult struct {
	Provider  string  `json:"provider"`
	FileID    string  `json:"fileId"`
	Language  string  `json:"language"`
	Release   string  `json:"release"`
	Rating    float64 `json:"rating"`
	Downloads int     `json:"downloads"`
	HashMatch bool    `json:"hashMatch"`
	SDH       bool    `json:"sdh"`
	Forced    bool    `json:"forced"`
}

// SubtitleSearchProvider reports one configured provider and whether it can be
// asked (a provider without its API key is configured but not ready).
type SubtitleSearchProvider struct {
	ID    string `json:"id"`
	Ready bool   `json:"ready"`
}

// SubtitleSearcher is the consumer-side view of the provider service.
type SubtitleSearcher interface {
	Status(ctx context.Context) []SubtitleSearchProvider
	Search(ctx context.Context, query SubtitleSearchQuery) ([]SubtitleSearchResult, error)
	Download(ctx context.Context, provider, fileID string) ([]byte, error)
}

// Sentinel errors a SubtitleSearcher adapter maps its provider errors onto.
var (
	// ErrSubtitleSearchNoKey: no configured provider has an API key.
	ErrSubtitleSearchNoKey = errors.New("no subtitle provider has an API key")
	// ErrSubtitleSearchNotSubtitle: the downloaded bytes are not a subtitle.
	ErrSubtitleSearchNotSubtitle = errors.New("downloaded file is not a subtitle")
	// ErrSubtitleSearchUnknownProvider: the request named a provider that is not configured.
	ErrSubtitleSearchUnknownProvider = errors.New("unknown subtitle provider")
)

const (
	maxSubtitleSearchBody = 4 << 10
	// maxSidecarNumber bounds the numbered-name walk on collision
	// (<stem>.en.srt, <stem>.en.2.srt, ...).
	maxSidecarNumber = 20
)

// SetSubtitleSearch wires the online subtitle searcher. Nil disables it.
func (h *Handler) SetSubtitleSearch(searcher SubtitleSearcher) {
	h.subtitleSearch = searcher
}

type subtitleSearchRequest struct {
	MediaID   string   `json:"mediaId"`
	Languages []string `json:"languages"`
}

type subtitleDownloadRequest struct {
	MediaID  string `json:"mediaId"`
	Provider string `json:"provider"`
	FileID   string `json:"fileId"`
	Language string `json:"language"`
	SDH      bool   `json:"sdh"`
	Forced   bool   `json:"forced"`
}

// SubtitleSearchStatus reports whether online search is enabled and which
// providers are ready. Any Profile session may read it so the UI can hide the
// entry; canSearch says whether this session may use it.
func (h *Handler) SubtitleSearchStatus(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.profileScope(w, r)
	if !ok {
		return
	}
	providers := []SubtitleSearchProvider{}
	if h.subtitleSearch != nil {
		providers = h.subtitleSearch.Status(r.Context())
	}
	writeSubtitleJSON(w, map[string]any{
		"enabled":   h.subtitleSearch != nil && len(providers) > 0,
		"canSearch": subtitleSearchAllowed(scope),
		"providers": providers,
	})
}

// SearchSubtitles asks the ready providers for subtitles matching one Media.
func (h *Handler) SearchSubtitles(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req subtitleSearchRequest
	target, ok := h.subtitleSearchTarget(w, r, &req, func() string { return req.MediaID })
	if !ok {
		return
	}
	languages := make([]string, 0, len(req.Languages))
	for _, tag := range req.Languages {
		if lang := sidecarLanguage(tag); lang != "" {
			languages = append(languages, lang)
		}
	}
	query := SubtitleSearchQuery{
		OSHash:    target.file.OSHash,
		ByteSize:  target.file.SizeBytes,
		Title:     target.media.Title,
		Year:      target.media.Year,
		Languages: languages,
	}
	query.Sequence, query.Index = h.orderedPosition(r.Context(), target.media.ID)
	results, err := h.subtitleSearch.Search(r.Context(), query)
	if err != nil {
		writeSubtitleSearchError(w, err)
		return
	}
	if results == nil {
		results = []SubtitleSearchResult{}
	}
	writeSubtitleJSON(w, map[string]any{"results": results})
}

// DownloadSubtitle fetches one result and records it as a sidecar caption
// beside the Media's primary file, exactly where a scan would find it. The
// file is created exclusively (an existing file is never overwritten: a taken
// name moves to the next number), the sidecar row makes the track playable at
// once, and an activity row records who added it so it can be found and
// removed later.
func (h *Handler) DownloadSubtitle(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req subtitleDownloadRequest
	target, ok := h.subtitleSearchTarget(w, r, &req, func() string { return req.MediaID })
	if !ok {
		return
	}
	language := sidecarLanguage(req.Language)
	if language == "" || !validSubtitleToken(req.Provider) || !validSubtitleToken(req.FileID) {
		http.Error(w, "download needs a provider, a file ID and a language", http.StatusBadRequest)
		return
	}
	body, err := h.subtitleSearch.Download(r.Context(), req.Provider, req.FileID)
	if err != nil {
		writeSubtitleSearchError(w, err)
		return
	}
	name, err := writeSidecarExclusive(target.file.Path, language, req.SDH, req.Forced, body)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) || isReadOnlyFS(err) {
			http.Error(w, "the media folder is not writable", http.StatusConflict)
			return
		}
		slog.Error("subtitle download write failed", "media", target.media.ID, "error", err)
		http.Error(w, "subtitle download failed", http.StatusInternalServerError)
		return
	}
	caption := store.MediaFileSidecarCaption{
		RelPath: name, Kind: store.SidecarKindCaption, Format: "srt",
		Language: language, SDH: req.SDH, Forced: req.Forced,
	}
	if err := h.store.AddMediaFileSidecarCaption(r.Context(), target.file.ContentFingerprint, caption); err != nil {
		// The file stays on disk: the next scan records it like any sidecar.
		writeSubtitleWriteError(w, err, "subtitle download failed")
		return
	}
	if err := h.store.AppendActivity(r.Context(), store.ActivityEntry{
		OccurredAt: time.Now().UTC(),
		Action:     store.ActivitySubtitleDownload,
		SessionID:  target.scope.SessionID,
		DeviceID:   target.scope.DeviceID,
		ProfileID:  target.scope.ProfileID,
		TargetType: "media",
		TargetID:   string(target.media.ID),
		LibraryID:  target.media.LibraryID,
	}); err != nil {
		slog.Error("subtitle download audit append failed", "media", target.media.ID, "error", err)
	}
	writeSubtitleJSON(w, map[string]any{"file": name, "language": language})
}

type subtitleSearchTarget struct {
	scope auth.Scope
	media store.Media
	file  store.MediaFile
}

// subtitleSearchTarget runs the shared preamble: a Profile session allowed to
// search, an enabled searcher, a capped body, and a Media this session can see
// in a scanned, unencrypted, unsealed Library with a primary file.
func (h *Handler) subtitleSearchTarget(w http.ResponseWriter, r *http.Request, into any, mediaID func() string) (subtitleSearchTarget, bool) {
	scope, ok := h.profileScope(w, r)
	if !ok {
		return subtitleSearchTarget{}, false
	}
	if !subtitleSearchAllowed(scope) {
		http.Error(w, "online subtitle search needs an adult Profile or a manager device", http.StatusForbidden)
		return subtitleSearchTarget{}, false
	}
	if h.subtitleSearch == nil {
		http.Error(w, "online subtitle search is not enabled", http.StatusNotFound)
		return subtitleSearchTarget{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSubtitleSearchBody)
	if !decodeSubtitleWrite(w, r, into) {
		return subtitleSearchTarget{}, false
	}
	id := store.MediaID(strings.TrimSpace(mediaID()))
	if id == "" {
		http.Error(w, "media ID is required", http.StatusBadRequest)
		return subtitleSearchTarget{}, false
	}
	media, err := h.store.Media(r.Context(), id, scope.PlaybackFilter())
	if err != nil || h.sealedMedia(r, id, scope) {
		http.Error(w, "unknown media", http.StatusNotFound)
		return subtitleSearchTarget{}, false
	}
	library, err := h.mediaLibrary(r.Context(), media.LibraryID, scope.PlaybackFilter())
	if err != nil {
		http.Error(w, "unknown media", http.StatusNotFound)
		return subtitleSearchTarget{}, false
	}
	if library.Encrypted || library.Custodial {
		http.Error(w, "online subtitles are not available for this Library", http.StatusConflict)
		return subtitleSearchTarget{}, false
	}
	file, err := h.store.PrimaryMediaFile(r.Context(), id, scope.PlaybackFilter())
	if err != nil || file.Path == "" {
		http.Error(w, "media has no file", http.StatusConflict)
		return subtitleSearchTarget{}, false
	}
	return subtitleSearchTarget{scope: scope, media: media, file: file}, true
}

// subtitleSearchAllowed: searching reaches the network and downloading writes
// beside the media, so it takes the same authority as other operator-facing
// lookups: an adult Profile or a manager device.
func subtitleSearchAllowed(scope auth.Scope) bool {
	return scope.DeviceRole == store.DeviceRoleManager || scope.Profile == store.ProfileAdult
}

// orderedPosition reads the generic ordered position plugins contribute as
// card fields. Missing or unreadable fields leave the position unknown.
func (h *Handler) orderedPosition(ctx context.Context, id store.MediaID) (sequence, index int) {
	fields, err := h.store.MediaCardFieldValues(ctx, []store.MediaID{id}, []string{"sequence", "index"})
	if err != nil {
		return 0, 0
	}
	return wholeNumber(fields[id]["sequence"].Number), wholeNumber(fields[id]["index"].Number)
}

func wholeNumber(v float64) int {
	if v < 1 || v > 100000 || v != math.Trunc(v) {
		return 0
	}
	return int(v)
}

func writeSubtitleSearchError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrSubtitleSearchNoKey):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"no subtitle provider has an API key","code":"NO_KEY"}` + "\n"))
	case errors.Is(err, ErrSubtitleSearchUnknownProvider):
		http.Error(w, "unknown subtitle provider", http.StatusBadRequest)
	case errors.Is(err, ErrSubtitleSearchNotSubtitle):
		http.Error(w, "the provider did not return a subtitle", http.StatusBadGateway)
	default:
		slog.Warn("subtitle provider request failed", "error", err)
		http.Error(w, "the subtitle provider did not answer", http.StatusBadGateway)
	}
}

// validSubtitleToken accepts a short opaque provider or file identifier.
func validSubtitleToken(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// sidecarLanguage normalizes a BCP 47-ish tag to the form a scan records for a
// sidecar ("pt-br" becomes "pt-BR"), or "" when it is not a plain language tag
// that can sit in a file name.
func sidecarLanguage(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(tag, "_", "-")))
	primary, region, hasRegion := strings.Cut(tag, "-")
	if len(primary) < 2 || len(primary) > 3 || !lowerLetters(primary) {
		return ""
	}
	if !hasRegion {
		return primary
	}
	if len(region) == 2 && lowerLetters(region) {
		return primary + "-" + strings.ToUpper(region)
	}
	return ""
}

func lowerLetters(s string) bool {
	for _, c := range s {
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return s != ""
}

// writeSidecarExclusive creates <stem>.<lang>[.sdh|.forced][.n].srt beside
// mediaPath through a root held on the media file's directory, never replacing
// an existing file, and returns the name it created.
func writeSidecarExclusive(mediaPath, language string, sdh, forced bool, body []byte) (string, error) {
	dir := filepath.Dir(mediaPath)
	if dir == "" || dir == "." {
		return "", errors.New("media path has no directory")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	base := filepath.Base(mediaPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	suffix := strings.ToLower(language)
	switch {
	case forced:
		suffix += ".forced"
	case sdh:
		suffix += ".sdh"
	}
	for n := 1; n <= maxSidecarNumber; n++ {
		name := stem + "." + suffix + ".srt"
		if n > 1 {
			name = fmt.Sprintf("%s.%s.%d.srt", stem, suffix, n)
		}
		f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.Write(body)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			_ = root.Remove(name)
			return "", errors.Join(werr, cerr)
		}
		return name, nil
	}
	return "", errors.New("too many subtitle files for this language")
}

// isReadOnlyFS reports a write refused because the filesystem is mounted
// read-only (a common media share setup).
func isReadOnlyFS(err error) bool {
	return errors.Is(err, syscall.EROFS)
}
