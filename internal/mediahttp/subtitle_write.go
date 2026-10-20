package mediahttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
)

// The subtitle MUTATION surface: the write halves of the series
// pin and the offset nudge, whose read halves are playback.resolveSubtitles and
// Handler.applySubtitleOffset. They live beside the sidecar byte route because
// the nudge is keyed on the sidecar's own track fingerprint, which only this
// package derives (readFingerprintedSidecar).
//
// Both are SESSION writes, not token writes. The byte routes above are gated by
// a Stream Token because they serve bytes to a player; these persist per-Profile
// preference state, so they take the same posture as the other per-Profile
// command routes (server.moderationScope): a real Profile session, never an
// admin token or a scoped developer key, whose closed vocabulary does not cover
// a viewer's subtitle preferences. Cookie sessions additionally carry the CSRF
// header check inside auth.ScopeFromRequest, because these are POSTs.

// maxSubtitleWriteBody bounds a subtitle command body. These are a handful of
// short fields; anything larger is malformed or hostile.
const maxSubtitleWriteBody = 8 << 10 // 8 KiB

// subtitlePinRequest pins (or un-pins) the subtitle choice for the item's group.
// Track is the SAME format-independent language+kind+role identity the read half
// publishes as SelectedSubtitleTrack.Track, so a client echoes back the shape it
// was handed rather than translating into a second vocabulary. Selected must be
// present: explicit false is a deliberate subtitles-OFF pin that ignores Track,
// while an omitted field is rejected rather than interpreted as false.
type subtitlePinRequest struct {
	MediaID       string                    `json:"mediaId"`
	Selected      *bool                     `json:"selected"`
	Track         playback.SubtitleTrackRef `json:"track"`
	AudioLanguage string                    `json:"audioLanguage,omitempty"`
}

// subtitleOffsetRequest nudges one sidecar caption track's timing. The track is
// addressed exactly as the serve route addresses it — the item plus the sidecar's
// index in the media file's recorded sidecar list — and NEVER by fingerprint:
// the fingerprint is derived server-side from the same bytes the serve path
// hashes, so a client can neither choose nor forge the storage key, and no new
// producer field has to publish it. SidecarIndex and OffsetMS must both be
// present; explicit zero values address the first sidecar and clear its nudge,
// while omitted fields are rejected rather than interpreted as zero.
type subtitleOffsetRequest struct {
	MediaID      string `json:"mediaId"`
	SidecarIndex *int   `json:"sidecarIndex"`
	OffsetMS     *int   `json:"offsetMs"`
}

// SetSubtitlePin persists the viewer's pinned subtitle choice for the item's
// group. The group spans a whole ordered container when the item
// belongs to one, so the pin resolves on every later item in that container
// through playback.resolveSubtitles.
func (h *Handler) SetSubtitlePin(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	scope, ok := h.profileScope(w, r)
	if !ok {
		return
	}
	var req subtitlePinRequest
	if !decodeSubtitleWrite(w, r, &req) {
		return
	}
	if req.Selected == nil {
		http.Error(w, "selected is required", http.StatusBadRequest)
		return
	}
	selected := *req.Selected
	mediaID := store.MediaID(strings.TrimSpace(req.MediaID))
	if mediaID == "" {
		http.Error(w, "media ID is required", http.StatusBadRequest)
		return
	}
	if !validPinnedTrack(req, selected) {
		http.Error(w, "pinned track must carry a language and a known kind", http.StatusBadRequest)
		return
	}
	// Resolving under the session's playback filter is the reveal gate: an item
	// this session cannot see cannot be pinned against, and the 404 is
	// indistinguishable from a genuinely unknown item.
	if _, err := h.store.Media(r.Context(), mediaID, scope.PlaybackFilter()); err != nil {
		http.Error(w, "unknown media", http.StatusNotFound)
		return
	}
	groupKey, err := h.store.MediaSubtitleGroup(r.Context(), mediaID)
	if err != nil {
		http.Error(w, "subtitle pin unavailable", http.StatusInternalServerError)
		return
	}
	if err := h.store.PutSubtitleChoice(r.Context(), scope.ProfileID, pinnedChoiceRecord(groupKey, req, selected), time.Now().UTC()); err != nil {
		writeSubtitleWriteError(w, err, "subtitle pin failed")
		return
	}
	writeSubtitleJSON(w, map[string]any{"pinned": selected})
}

// SetSubtitleOffset persists the viewer's timing nudge for one sidecar caption
// track. The next serve of that track applies it in
// applySubtitleOffset.
//
// Accepted range: an INTEGER count of milliseconds in
// [-store.MaxSubtitleOffsetMillis, +store.MaxSubtitleOffsetMillis] (±10 minutes).
// Negative pulls cues earlier, positive pushes them later, and exactly 0 CLEARS
// the nudge — "no shift" is the absence of a row, so nudging back to centre
// leaves nothing behind. A value outside the range is refused whole with 400 and
// nothing is written; a body that is not an integer in that field (a string, a
// fraction, malformed JSON, an oversize body) fails the decode and is likewise
// refused with 400 before any store call.
func (h *Handler) SetSubtitleOffset(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	scope, ok := h.profileScope(w, r)
	if !ok {
		return
	}
	var req subtitleOffsetRequest
	if !decodeSubtitleWrite(w, r, &req) {
		return
	}
	if req.SidecarIndex == nil {
		http.Error(w, "sidecar index is required", http.StatusBadRequest)
		return
	}
	if req.OffsetMS == nil {
		http.Error(w, "subtitle offset is required", http.StatusBadRequest)
		return
	}
	sidecarIndex := *req.SidecarIndex
	offsetMS := *req.OffsetMS
	mediaID := store.MediaID(strings.TrimSpace(req.MediaID))
	if mediaID == "" {
		http.Error(w, "media ID is required", http.StatusBadRequest)
		return
	}
	if offsetMS > store.MaxSubtitleOffsetMillis || offsetMS < -store.MaxSubtitleOffsetMillis {
		http.Error(w, "subtitle offset is out of range", http.StatusBadRequest)
		return
	}
	fingerprint, status := h.sidecarTrackFingerprint(r, mediaID, sidecarIndex, scope)
	if status != 0 {
		http.Error(w, "unknown subtitle", status)
		return
	}
	if err := h.store.PutSubtitleOffset(r.Context(), scope.ProfileID, fingerprint, offsetMS, time.Now().UTC()); err != nil {
		writeSubtitleWriteError(w, err, "subtitle offset failed")
		return
	}
	writeSubtitleJSON(w, map[string]any{"offsetMs": offsetMS})
}

// sidecarTrackFingerprint resolves (item, sidecar index) to the track fingerprint
// the serve path will look the nudge up under, or reports the HTTP status to
// refuse with (0 = resolved). It walks the SAME resolution the Sidecars byte
// route walks — reveal filter, sealed check, recorded caption list, path
// re-confinement, ingest boundary — so a track the viewer cannot be served
// cannot be nudged either.
func (h *Handler) sidecarTrackFingerprint(r *http.Request, mediaID store.MediaID, index int, scope auth.Scope) (string, int) {
	if index < 0 {
		return "", http.StatusBadRequest
	}
	if h.sealedMedia(r, mediaID, scope) {
		return "", http.StatusNotFound
	}
	file, err := h.store.PrimaryMediaFile(r.Context(), mediaID, scope.PlaybackFilter())
	if err != nil {
		return "", http.StatusNotFound
	}
	captions, err := h.store.MediaFileSidecarCaptions(r.Context(), file.ContentFingerprint)
	if err != nil {
		return "", http.StatusInternalServerError
	}
	if index >= len(captions) {
		return "", http.StatusNotFound
	}
	caption := captions[index]
	// A nudge is only meaningful on a track the serve path actually shifts, and
	// applySubtitleOffset runs on the WebVTT rung alone. Bitmap and interactive
	// sidecars are served as raw bytes and would silently ignore the correction,
	// so storing one for them is refused rather than accepted-and-inert.
	if caption.Kind == store.SidecarKindInteractive || isBitmapSubtitleFormat(caption.Format) {
		return "", http.StatusBadRequest
	}
	path, ok := sidecarFilePath(file.Path, caption.RelPath)
	if !ok {
		return "", http.StatusNotFound
	}
	_, fingerprint, err := readFingerprintedSidecar(path, caption.Format)
	if err != nil {
		return "", http.StatusNotFound
	}
	return fingerprint, 0
}

// profileScope resolves a Profile-session scope for a subtitle write, or writes
// the refusal and reports ok=false. Subtitle preferences are per-Profile viewer
// state: an admin API token or a scoped developer key carries no Profile and is
// refused rather than silently writing under an empty one.
func (h *Handler) profileScope(w http.ResponseWriter, r *http.Request) (auth.Scope, bool) {
	scope, err := h.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		http.Error(w, "invalid session", http.StatusUnauthorized)
		return auth.Scope{}, false
	}
	if scope.APITokenID != "" || scope.APIKeyID != "" || scope.ProfileID == "" {
		http.Error(w, "subtitle preferences require a Profile session", http.StatusForbidden)
		return auth.Scope{}, false
	}
	return scope, true
}

// decodeSubtitleWrite reads a capped JSON command body, refusing anything
// malformed or oversize with 400 before the handler touches the store.
func decodeSubtitleWrite(w http.ResponseWriter, r *http.Request, into any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxSubtitleWriteBody)
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return false
	}
	return true
}

// validPinnedTrack reports whether the request names a track the pin can be
// resolved against later. A subtitles-OFF pin names no track, so it is always
// valid; a selecting pin needs a language and a kind from the closed vocabulary,
// because matchPinnedTrack resolves on exactly that tuple.
func validPinnedTrack(req subtitlePinRequest, selected bool) bool {
	if !selected {
		return true
	}
	if strings.TrimSpace(req.Track.Language) == "" {
		return false
	}
	return slices.Contains(playback.SubtitleTrackKinds(), req.Track.Kind)
}

// pinnedChoiceRecord projects the request onto the stored record. A subtitles-OFF
// pin stores an EMPTY tuple regardless of what the client sent: the record means
// "this group shows no subtitles", and carrying a track identity alongside that
// would leave a contradiction for the resolver to arbitrate.
func pinnedChoiceRecord(groupKey string, req subtitlePinRequest, selected bool) store.SubtitleChoiceRecord {
	record := store.SubtitleChoiceRecord{
		GroupKey:      groupKey,
		Selected:      selected,
		AudioLanguage: strings.TrimSpace(req.AudioLanguage),
	}
	if !selected {
		return record
	}
	record.SubLanguage = strings.TrimSpace(req.Track.Language)
	record.SubKind = req.Track.Kind
	record.SubForced = req.Track.Roles.Forced
	record.SubSDH = req.Track.Roles.SDH
	record.SubVisualImpaired = req.Track.Roles.VisualImpaired
	record.SubCommentary = req.Track.Roles.Commentary
	return record
}

// writeSubtitleWriteError maps a store refusal onto its status: a rejected value
// is the caller's fault (400), anything else is ours (500).
func writeSubtitleWriteError(w http.ResponseWriter, err error, message string) {
	var validation store.ValidationError
	if errors.As(err, &validation) {
		http.Error(w, validation.Error(), http.StatusBadRequest)
		return
	}
	http.Error(w, message, http.StatusInternalServerError)
}

func writeSubtitleJSON(w http.ResponseWriter, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(data, '\n'))
}
