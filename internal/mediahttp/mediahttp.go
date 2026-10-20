// Package mediahttp serves token-gated Media bytes.
package mediahttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/genassets"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/playback/subtitleingest"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

// Handler serves media-byte routes.
type Handler struct {
	store                 *store.Store
	auth                  *auth.Service
	tokens                *streamtoken.Service
	artwork               *artwork.Service
	hls                   *playback.Sessions
	assets                *genassets.Service
	artworkCandidateFetch ArtworkCandidateFetcher
	providerSubtitles     *ProviderSubtitleFetcher
	subtitles             SubtitleConverter
	subtitleDir           string
	subtitleMu            sync.Mutex
	hotSegments           *hotSegmentCache
	sealed                SealedPredicate
	encryptedBytes        EncryptedByteDecryptor
	mediaResolver         func(context.Context, store.MediaID, store.LibraryFilter) (store.Media, error)
}

// SealedPredicate reports whether a Library is currently Sealed (ADR-0098 area
// 2). Every byte/asset route refuses a previously-minted token once its Media's
// Library is sealed. Composition wires it off libencrypt.Manager.Sealed; nil
// (the default) treats no Library as sealed, so deployments without library
// encryption pay nothing.
type SealedPredicate func(libraryID store.LibraryID) bool

// SetSealed wires the Sealed Library predicate (ADR-0098 area 2). Nil disables it.
func (h *Handler) SetSealed(sealed SealedPredicate) {
	h.sealed = sealed
}

// EncryptedByteDecryptor decrypts one encrypted-Library ciphertext object after
// the byte route has already rechecked Stream Token, Session/Reveal, and Sealed
// state. It returns plaintext bytes in memory; no plaintext file is created.
type EncryptedByteDecryptor interface {
	DecryptBytes(libraryID store.LibraryID, ciphertextPath, objectClass, immutableObjectID string) ([]byte, error)
}

// EncryptedByteReadSeeker is the streaming extension used by direct play.
// Implementations decrypt chunks on demand and must return a fresh reader for
// each request. mediahttp owns closing the returned reader. Every open carries
// the caller-derived immutable object identity (persisted content fingerprint
// or persisted derivative ids — never the path) so ciphertext presented under
// the wrong identity fails closed. The identity travels as plain strings; the
// composition root adapts them onto libencrypt's ObjectContext.
type EncryptedByteReadSeeker interface {
	OpenDecryptReader(libraryID store.LibraryID, ciphertextPath, objectClass, immutableObjectID string) (io.ReadSeekCloser, error)
}

// Encrypted object classes — the byte routes' view of libencrypt's closed
// ObjectClass vocabulary (kept as plain strings so this package stays
// decoupled from the crypto layer; the adapter validates them fail-closed).
const (
	encryptedClassMediaBytes     = "media-bytes"
	encryptedClassGeneratedAsset = "generated-asset"
	streamTokenCacheControl      = "private, no-store"
)

type streamTokenResponseWriter struct {
	http.ResponseWriter
}

// MarkStreamTokenResponseNonStorable guards the header commit because
// http.ServeContent removes cache directives from its generated error replies.
func MarkStreamTokenResponseNonStorable(w http.ResponseWriter) http.ResponseWriter {
	wrapped := &streamTokenResponseWriter{ResponseWriter: w}
	wrapped.markNonStorable()
	return wrapped
}

func (w *streamTokenResponseWriter) WriteHeader(statusCode int) {
	w.markNonStorable()
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *streamTokenResponseWriter) Write(body []byte) (int, error) {
	w.markNonStorable()
	return w.ResponseWriter.Write(body)
}

func (w *streamTokenResponseWriter) ReadFrom(src io.Reader) (int64, error) {
	w.markNonStorable()
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(src)
	}
	return io.Copy(w.ResponseWriter, src)
}

func (w *streamTokenResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *streamTokenResponseWriter) markNonStorable() {
	w.Header().Set("Cache-Control", streamTokenCacheControl)
}

// SetEncryptedBytes wires the encrypted-Library byte decryptor. Nil means
// encrypted originals fail closed.
func (h *Handler) SetEncryptedBytes(decryptor EncryptedByteDecryptor) {
	h.encryptedBytes = decryptor
}

// sealedMedia reports whether the Media's Library is currently Sealed. Resolving
// the Media under scope.LibraryFilter also re-checks reveal/hide; a sealed (or
// unresolvable) Media short-circuits the byte route to a fail-closed refusal.
// Returns false when no sealed predicate is wired (nothing is sealed).
func (h *Handler) sealedMedia(r *http.Request, mediaID store.MediaID, scope auth.Scope) bool {
	if h.sealed == nil {
		return false
	}
	media, err := h.store.Media(r.Context(), mediaID, scope.PlaybackFilter())
	if err != nil {
		// Unresolvable under the live filter: treat as denied (fail closed).
		return true
	}
	return h.sealed(media.LibraryID)
}

func (h *Handler) verifyMediaGrant(ctx context.Context, token string, target streamtoken.Target) (streamtoken.Grant, auth.Scope, error) {
	if h.tokens == nil {
		return streamtoken.Grant{}, auth.Scope{}, errors.New("stream token service unavailable")
	}
	authenticated, err := h.tokens.Authenticate(token, target)
	if err != nil {
		return streamtoken.Grant{}, auth.Scope{}, err
	}
	grant := authenticated.Grant()
	scope, err := h.auth.Scope(ctx, grant.SessionID)
	if err != nil {
		return streamtoken.Grant{}, auth.Scope{}, err
	}
	grant, err = h.verifyCompatDecision(ctx, authenticated, scope)
	if err != nil {
		return streamtoken.Grant{}, auth.Scope{}, err
	}
	if err := verifyReceiverBinding(grant); err != nil {
		return streamtoken.Grant{}, auth.Scope{}, err
	}
	return grant, scope, nil
}

// errUnverifiedReceiver refuses bytes for a receiver-bound grant whose receiver
// profile never resolved through the ADR-0161 quirks selectors.
var errUnverifiedReceiver = errors.New("unverified receiver profile")

// verifyReceiverBinding enforces the SUBSYS-20 trust boundary: never serve bytes
// on an unverified receiver assumption. It lives in verifyMediaGrant so every
// byte route inherits it — a per-route copy left Direct unguarded.
func verifyReceiverBinding(grant streamtoken.Grant) error {
	if !grant.ReceiverBound {
		return nil
	}
	if !grant.ReceiverProfileResolved || !grant.HasResolvedProfileCapability() {
		return errUnverifiedReceiver
	}
	return nil
}

type ArtworkCandidateFetcher interface {
	FetchResource(ctx context.Context, rawURL string) ([]byte, error)
}

// SubtitleConverter extracts a muxed text subtitle stream (by absolute ffprobe
// stream index) from src and writes it to dst as WebVTT.
// Satisfied by *ffmpeg.Runner.
type SubtitleConverter interface {
	ExtractSubtitleToWebVTT(ctx context.Context, src string, streamIndex int, dst string) error
}

// NewHandler creates a media HTTP handler.
func NewHandler(st *store.Store, authService *auth.Service, tokens *streamtoken.Service, artworkService *artwork.Service, hls *playback.Sessions) *Handler {
	return &Handler{
		store:         st,
		auth:          authService,
		tokens:        tokens,
		artwork:       artworkService,
		hls:           hls,
		mediaResolver: st.Media,
	}
}

// SetAssets wires the Generated Assets service so the assets route can
// resolve fingerprint-keyed files. Nil (the default) serves nothing.
func (h *Handler) SetAssets(assets *genassets.Service) {
	h.assets = assets
}

func (h *Handler) SetArtworkCandidateFetcher(fetcher ArtworkCandidateFetcher) {
	h.artworkCandidateFetch = fetcher
}

// SetSubtitles wires the WebVTT subtitle converter and the directory under
// which converted sidecars are cached. Nil converter (the
// default) serves no subtitle sidecars. dir is created on first use.
func (h *Handler) SetSubtitles(converter SubtitleConverter, dir string) {
	h.subtitles = converter
	h.subtitleDir = dir
}

// safeSubtitleFingerprint maps a content fingerprint to the filesystem-safe stem
// the subtitle-sidecar cache keys on: the "sha256:" prefix's colon is replaced so
// the fingerprint is a single path element. It is the single source of truth for
// the sidecar naming — both the extract-cache write path and PurgeSubtitles use
// it, so the purge can never drift from the layout.
func safeSubtitleFingerprint(fingerprint string) string {
	return strings.ReplaceAll(fingerprint, ":", "_")
}

// PurgeSubtitlesForFingerprint removes every cached WebVTT subtitle sidecar for a
// content fingerprint — one file per subtitle stream index, all sharing the
// fingerprint stem — through the same keying the write path uses. It is the
// reconcile hook for the Encrypted-Library crash-recovery sweep (ADR-0045
// §reconcile, ENCPRIV-10d): subtitle extraction is fail-closed for an Encrypted
// Library, so any sidecar found for its media's fingerprints is a crash-stranded
// plaintext leak. No configured subtitle dir (the default) is a no-op. Missing
// files are fine (the common case). Hard delete is correct: a leaked plaintext
// caption of an encrypted item is the invariant-#5 at-rest violation and is
// losslessly regenerable from the sealed source, so invariant #6 does not attach.
func (h *Handler) PurgeSubtitlesForFingerprint(fingerprint string) error {
	if h.subtitleDir == "" || fingerprint == "" {
		return nil
	}
	// The match and the removal both run through ONE descriptor on the cache
	// directory. Globbing an absolute pattern and then removing each result names
	// the directory once per match, so a directory re-pointed mid-purge would send
	// the deletes somewhere the caller never inspected — and this purge deletes
	// unconditionally.
	root, err := os.OpenRoot(h.subtitleDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer root.Close()
	if err := verifyRootUnreplaced(root, h.subtitleDir); err != nil {
		return err
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	// Trailing "*" also catches subtitleSidecarPath's/hardenExtractedWebVTT's
	// in-flight ".vtt.staged"/".vtt.tmp" siblings (and any future derived
	// suffix) rather than enumerating suffixes the purge would otherwise go
	// blind to again.
	pattern := safeSubtitleFingerprint(fingerprint) + ".*.vtt*"
	var joined error
	for _, entry := range entries {
		matched, err := filepath.Match(pattern, entry.Name())
		if err != nil {
			return err
		}
		if !matched {
			continue
		}
		if err := root.Remove(entry.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

// SetHotSegmentCache enables a bounded RAM cache for precomputed HLS segment
// bytes while there is active fan-out for the same segment. Playlists stay
// disk-served; Stream Token and Reveal checks still run before cache lookup.
func (h *Handler) SetHotSegmentCache(maxBytes int64) {
	h.hotSegments = newHotSegmentCache(maxBytes)
}

// Assets serves Generated Asset bytes for a signed token URL:
// trickplay manifest and sheets, the preview clip, and Marker thumbnails.
// These are media bytes — Stream Tokens and the Reveal contract apply as-is
// (a restricted item's token requires the session to currently hold reveal),
// and the visible-file lookup re-verifies on every request like HLS.
func (h *Handler) Assets(w http.ResponseWriter, r *http.Request) {
	w = MarkStreamTokenResponseNonStorable(w)
	rest := strings.TrimPrefix(r.URL.Path, "/api/media/assets/")
	token, name, ok := strings.Cut(rest, "/")
	if !ok || token == "" || name == "" {
		http.Error(w, "missing stream token", http.StatusUnauthorized)
		return
	}
	grant, scope, err := h.verifyMediaGrant(r.Context(), token, streamtoken.TargetMediaAssets)
	if err != nil {
		http.Error(w, "invalid stream token", http.StatusUnauthorized)
		return
	}
	if h.assets == nil {
		http.Error(w, "unknown asset", http.StatusNotFound)
		return
	}
	media, err := h.mediaResolver(r.Context(), grant.MediaID, scope.PlaybackFilter())
	if err != nil || h.sealed != nil && h.sealed(media.LibraryID) {
		http.Error(w, "unknown asset", http.StatusNotFound)
		return
	}
	library, err := h.mediaLibrary(r.Context(), media.LibraryID, scope.PlaybackFilter())
	if err != nil {
		http.Error(w, "unknown asset", http.StatusNotFound)
		return
	}
	file, err := h.store.PrimaryMediaFile(r.Context(), grant.MediaID, scope.PlaybackFilter())
	if err != nil {
		http.Error(w, "unknown asset", http.StatusNotFound)
		return
	}
	path, err := h.assets.AssetPath(file.ContentFingerprint, name)
	if err != nil {
		http.Error(w, "unknown asset", http.StatusNotFound)
		return
	}
	contentType := "image/jpeg"
	switch filepath.Ext(name) {
	case ".json":
		contentType = "application/json"
	case ".mp4":
		contentType = "video/mp4"
	}
	// A generated asset's immutable identity is the persisted content
	// fingerprint of the media it derives from plus the asset name — never the
	// asset path. An encrypted asset without a fingerprint fails closed.
	if library.Encrypted && file.ContentFingerprint == "" {
		http.Error(w, "unknown asset", http.StatusNotFound)
		return
	}
	h.serveLibraryFile(w, r, library, path, name, contentType, "unknown asset",
		encryptedClassGeneratedAsset, file.ContentFingerprint+"/"+name)
}

// Subtitles serves a WebVTT subtitle sidecar for a signed token URL (ADR-0010
// §5). The path is /api/media/subtitle/<token>/<streamIndex>.vtt where
// streamIndex is the absolute ffprobe stream index of a muxed text subtitle.
// The sidecar bytes join Reveal exactly like the other media targets: the
// token is re-verified against the live session scope on every fetch, so a
// relock or device revocation stops serving the sidecar. The converted WebVTT
// is cached on disk keyed by content fingerprint + stream index, so repeat
// fetches (and seeks) skip the ffmpeg conversion.
func (h *Handler) Subtitles(w http.ResponseWriter, r *http.Request) {
	w = MarkStreamTokenResponseNonStorable(w)
	rest := strings.TrimPrefix(r.URL.Path, "/api/media/subtitle/")
	token, name, ok := strings.Cut(rest, "/")
	if !ok || token == "" || name == "" {
		http.Error(w, "missing stream token", http.StatusUnauthorized)
		return
	}
	streamIndex, ok := parseSubtitleName(name)
	if !ok {
		http.Error(w, "unknown subtitle", http.StatusNotFound)
		return
	}
	grant, scope, err := h.verifyMediaGrant(r.Context(), token, streamtoken.TargetMediaSubtitle)
	if err != nil {
		// A well-formed, unexpired grant whose session no longer resolves is a
		// playback that ended (ADR-0012 §7 revocation, relock). The track menu
		// keeps the URL for the whole runtime, so 410 tells the player the
		// track is gone for good instead of inviting a retry; a token that never
		// verified at all stays 401.
		http.Error(w, "stream ended", h.endedSubtitleStatus(r.Context(), token))
		return
	}
	if grant.SubtitleStreamIndex != 0 && grant.SubtitleStreamIndex != streamIndex+1 {
		http.Error(w, "unknown subtitle", http.StatusNotFound)
		return
	}
	if h.subtitles == nil || h.subtitleDir == "" {
		http.Error(w, "unknown subtitle", http.StatusNotFound)
		return
	}
	media, err := h.mediaResolver(r.Context(), grant.MediaID, scope.PlaybackFilter())
	if err != nil || h.sealed != nil && h.sealed(media.LibraryID) {
		http.Error(w, "media unavailable", http.StatusNotFound)
		return
	}
	var file store.MediaFile
	if grant.SubtitleFileFingerprint == "" {
		// Compatibility for tokens minted before exact-file binding: those grants
		// always addressed the primary file. Newly minted grants never take this
		// branch.
		file, err = h.store.PrimaryMediaFile(r.Context(), grant.MediaID, scope.PlaybackFilter())
	} else {
		files, filesErr := h.store.MediaFilesForMedia(r.Context(), grant.MediaID, scope.PlaybackFilter())
		if filesErr == nil {
			for _, candidate := range files {
				if candidate.ContentFingerprint == grant.SubtitleFileFingerprint {
					file = candidate
					break
				}
			}
		}
		if filesErr != nil || file.ContentFingerprint == "" {
			http.Error(w, "unknown subtitle", http.StatusNotFound)
			return
		}
	}
	if err != nil {
		http.Error(w, "unknown subtitle", http.StatusNotFound)
		return
	}
	// Encrypted Libraries fail closed: extracting a muxed subtitle
	// caches a plaintext WebVTT sidecar under subtitleDir — a plaintext derivative
	// of the encrypted Media. No encrypted-plane cache exists yet, so
	// refuse rather than write it, whether the Library is Sealed or Unsealed.
	library, err := h.mediaLibrary(r.Context(), media.LibraryID, scope.PlaybackFilter())
	if err != nil {
		http.Error(w, "media unavailable", http.StatusNotFound)
		return
	}
	if library.Encrypted {
		http.Error(w, "subtitle unavailable", http.StatusNotFound)
		return
	}
	cached, err := h.subtitleSidecarPath(r.Context(), file.ContentFingerprint, file.Path, streamIndex)
	if err != nil {
		slog.Error("subtitle convert failed", "error", err.Error())
		http.Error(w, "subtitle unavailable", http.StatusServiceUnavailable)
		return
	}
	// The cache member is re-derived from a descriptor on the subtitle cache
	// directory and bound to its directory entry, so the conversion that just ran
	// and the bytes now served name the same file even if the cache entry was
	// replaced in between.
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	if err := serveRooted(w, r, name, cached, time.Time{}); err != nil {
		http.Error(w, "unknown subtitle", rootScopeStatus(err))
		return
	}
}

// subtitleSidecarPath returns the cached WebVTT member for a (fingerprint,
// streamIndex), converting it on first request. The cache key embeds the
// content fingerprint, so two files that share content share the conversion
// and a re-scanned file with new content gets a fresh sidecar.
//
// It returns a rootedPath rather than a flattened path so the serve step reads
// the cache member through a descriptor on subtitleDir instead of re-walking the
// name: the directory this function published into and the directory the bytes
// come out of are then provably the same one.
//
// The extraction lands on a staging path and is hardened into place, so the
// cache file only ever exists in its sanitised form: nothing under subtitleDir
// is servable until it has passed the ingest boundary, including for the
// lock-free cache-hit above.
func (h *Handler) subtitleSidecarPath(ctx context.Context, fingerprint, srcPath string, streamIndex int) (rootedPath, error) {
	if fingerprint == "" {
		return rootedPath{}, errors.New("media file has no content fingerprint")
	}
	if err := os.MkdirAll(h.subtitleDir, 0o700); err != nil {
		return rootedPath{}, err
	}
	member, err := newRootedPath(h.subtitleDir, fmt.Sprintf("%s.%d.vtt", safeSubtitleFingerprint(fingerprint), streamIndex))
	if err != nil {
		return rootedPath{}, err
	}
	if rootedRegularFileExists(member) {
		return member, nil
	}
	h.subtitleMu.Lock()
	defer h.subtitleMu.Unlock()
	if rootedRegularFileExists(member) {
		return member, nil
	}
	dst := member.String()
	staged := dst + ".staged"
	defer os.Remove(staged)
	if err := h.subtitles.ExtractSubtitleToWebVTT(ctx, srcPath, streamIndex, staged); err != nil {
		return rootedPath{}, err
	}
	if err := hardenExtractedWebVTT(h.subtitleDir, staged, member); err != nil {
		return rootedPath{}, err
	}
	return member, nil
}

// endedSubtitleStatus maps a failed subtitle grant verification to its status:
// 410 when the token itself still authenticates (signature, expiry, target) but
// its session no longer resolves to a scope, 401 for every other failure.
func (h *Handler) endedSubtitleStatus(ctx context.Context, token string) int {
	if h.tokens == nil {
		return http.StatusUnauthorized
	}
	grant, err := h.tokens.UnverifiedGrant(token, streamtoken.TargetMediaSubtitle)
	if err != nil {
		return http.StatusUnauthorized
	}
	if _, err := h.auth.Scope(ctx, grant.SessionID); err == nil {
		return http.StatusUnauthorized
	}
	return http.StatusGone
}

// rootedRegularFileExists reports whether a rooted member currently resolves to
// a regular file through a descriptor on its root. It replaces a bare os.Stat
// cache probe: a stat that follows a symlink would report a cache HIT for an
// entry whose bytes live outside the cache directory entirely.
func rootedRegularFileExists(p rootedPath) bool {
	_, err := statRooted(p)
	return err == nil
}

// hardenExtractedWebVTT runs a freshly extracted muxed subtitle through the
// shared ingest boundary and publishes the result at dst with an atomic rename.
//
// A muxed track is not safer than an external sidecar. ffmpeg's `-c:s webvtt`
// re-emits the source's inline markup verbatim, so an ASS- or SRT-sourced muxed
// stream carries the same cue-injection surface into every renderer's DOM
// (ADR-0194 §7 makes the hardening pipeline blocking for any ingest path that
// ships). The pass is paid once, at cache-write time, rather than on every
// request — and a file that cannot be hardened is never published, so the
// failure is not cached and the next request retries the conversion.
//
// The publish is root-scoped: the sanitised bytes are written to a temp member
// and renamed onto the cache name through ONE descriptor held on the cache
// directory, so the write and the rename land in the directory that was checked
// rather than in whatever the name resolves to at each step. O_EXCL is what makes
// the final component fail closed — openat refuses an existing entry, symlink
// included, so a planted link is never written through.
func hardenExtractedWebVTT(cacheDir, staged string, dst rootedPath) error {
	raw, err := readGuardedSidecar(rootedFile(staged), string(subtitleingest.FormatVTT))
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(cacheDir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := verifyRootUnreplaced(root, cacheDir); err != nil {
		return err
	}
	tmp := dst.rel + ".tmp"
	if err := root.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(subtitleingest.SanitizeWebVTT(string(raw))); err != nil {
		_ = file.Close()
		_ = root.Remove(tmp)
		return err
	}
	if err := file.Close(); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	if err := verifyRootUnreplaced(root, cacheDir); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	if err := root.Rename(tmp, dst.rel); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}

// parseSubtitleName parses a "<streamIndex>.vtt" sidecar name into its stream
// index. Only the .vtt extension is accepted, and the index must be a
// non-negative integer.
func parseSubtitleName(name string) (int, bool) {
	base, ok := strings.CutSuffix(name, ".vtt")
	if !ok || base == "" {
		return 0, false
	}
	index, err := strconv.Atoi(base)
	if err != nil || index < 0 {
		return 0, false
	}
	return index, true
}

// parseSidecarName parses a "<index>.<ext>" sidecar request name into its
// 0-based sidecar index and requested extension. Two extensions are accepted:
// ".vtt" (the flattened WebVTT projection, the universal default) and ".ass"
// (the raw-ASS passthrough for a declared-`ass` libass profile, ADR-0194 §1 /
// SUBSYS-5). The index must be a non-negative integer.
func parseSidecarName(name string) (index int, ext string, ok bool) {
	for _, candidate := range []string{"vtt", "ass"} {
		if base, cut := strings.CutSuffix(name, "."+candidate); cut && base != "" {
			if i, err := strconv.Atoi(base); err == nil && i >= 0 {
				return i, candidate, true
			}
			return 0, "", false
		}
	}
	return 0, "", false
}

// Sidecars serves a sidecar caption track as WebVTT for a signed token URL.
// The path is /api/media/sidecar/<token>/<index>.vtt
// where index is the 0-based position of the sidecar in the media file's
// recorded sidecar list (ordered by relative path). Unlike Subtitles — which
// extracts a MUXED stream by ffprobe index — this serves an external .srt/.vtt
// file discovered next to the media file. The bytes join Reveal exactly like
// the muxed subtitle: the token is re-verified against the live session scope
// on every fetch, so a relock or device revocation stops serving the sidecar.
//
// Security (HARD CONSTRAINT): the served file is the recorded sidecar resolved
// against the media file's own directory and re-confined to that directory at
// serve time (sidecarFilePath) — a sidecar is never served outside the library
// root, and the stored rel_path can never traverse out (it is a sibling base
// name recorded by scan, and the join is guarded here regardless).
func (h *Handler) Sidecars(w http.ResponseWriter, r *http.Request) {
	w = MarkStreamTokenResponseNonStorable(w)
	rest := strings.TrimPrefix(r.URL.Path, "/api/media/sidecar/")
	token, name, ok := strings.Cut(rest, "/")
	if !ok || token == "" || name == "" {
		http.Error(w, "missing stream token", http.StatusUnauthorized)
		return
	}
	index, ext, ok := parseSidecarName(name)
	if !ok {
		http.Error(w, "unknown subtitle", http.StatusNotFound)
		return
	}
	grant, scope, err := h.verifyMediaGrant(r.Context(), token, streamtoken.TargetMediaSidecar)
	if err != nil {
		http.Error(w, "invalid stream token", http.StatusUnauthorized)
		return
	}
	if ext == "ass" && !grant.SidecarRawASS {
		http.Error(w, "unknown subtitle", http.StatusNotFound)
		return
	}
	if h.sealedMedia(r, grant.MediaID, scope) {
		http.Error(w, "media unavailable", http.StatusNotFound)
		return
	}
	file, err := h.store.PrimaryMediaFile(r.Context(), grant.MediaID, scope.PlaybackFilter())
	if err != nil {
		http.Error(w, "unknown subtitle", http.StatusNotFound)
		return
	}
	captions, err := h.store.MediaFileSidecarCaptions(r.Context(), file.ContentFingerprint)
	if err != nil {
		http.Error(w, "subtitle unavailable", http.StatusInternalServerError)
		return
	}
	if index >= len(captions) {
		http.Error(w, "unknown subtitle", http.StatusNotFound)
		return
	}
	caption := captions[index]
	path, ok := sidecarFilePath(file.Path, caption.RelPath)
	if !ok {
		// A rel_path that does not re-confine to the media file's directory is
		// served as a 404, indistinguishable from a missing sidecar — the path
		// escape never reaches the filesystem.
		http.Error(w, "unknown subtitle", http.StatusNotFound)
		return
	}
	// An interactive sidecar carries a plugin-defined payload
	// outside the subtitle vocabulary entirely, so it takes neither the WebVTT
	// transform nor the subtitle boundary. It streams straight off the open file:
	// nothing is buffered, so its size needs no ceiling of its own.
	if caption.Kind == store.SidecarKindInteractive {
		serveRawSidecarFile(w, r, name, path)
		return
	}
	// A bitmap subtitle sidecar (ADR-0194 §5 Tier 2: PGS .sup, VobSub .idx/.sub)
	// is served as RAW bytes — it is not text, and running it through the WebVTT
	// transform would corrupt its body. It reaches the native libmpv / burn-in
	// ladder, not the WebVTT rung. Raw delivery is not unchecked delivery: the
	// bytes pass the ingest boundary, which is where MaxBitmapBytes is enforced.
	// A text caption sidecar is read as WebVTT below (vtt verbatim, srt/ass
	// converted in-process).
	if isBitmapSubtitleFormat(caption.Format) {
		raw, err := readGuardedSidecar(path, caption.Format)
		if err != nil {
			writeSidecarReadError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(raw))
		return
	}
	// Raw-ASS passthrough: a ".ass" request for an
	// ass/ssa caption serves the authored bytes BYTE-IDENTICALLY, so a libass
	// engine renders the original styling. The URL is minted with the .ass
	// extension only for a declared-`ass` profile (captionTracksInfo); every
	// other client's URL ends in .vtt and takes the flattening path below. The
	// bytes still pass the ingest-hardening boundary (size + override-tag scope)
	// before serve — passthrough is byte-identical, not unchecked.
	if ext == "ass" {
		if caption.Format != "ass" && caption.Format != "ssa" {
			http.Error(w, "unknown subtitle", http.StatusNotFound)
			return
		}
		raw, err := readGuardedSidecar(path, caption.Format)
		if err != nil {
			writeSidecarReadError(w, err)
			return
		}
		// A persisted offset cannot be applied server-side here without rewriting
		// authored timestamps and violating this route's byte-identity contract.
		// ADR-0194 §4 assigns live offset for raw ASS to the libass engine's
		// sub-delay path; the server-applied shift below is for WebVTT projections.
		w.Header().Set("Content-Type", "text/x-ssa")
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(raw))
		return
	}
	body, fingerprint, err := readSidecarAsWebVTT(path, caption.Format)
	if err != nil {
		writeSidecarReadError(w, err)
		return
	}
	// Apply the viewer's persisted offset nudge server-side:
	// re-issuing the VTT with shifted cue timings carries the correction to every
	// client, including a cast receiver with no sub-delay control of its own.
	body = h.applySubtitleOffset(r.Context(), scope.ProfileID, fingerprint, body)
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	http.ServeContent(w, r, name, time.Time{}, strings.NewReader(body))
}

// applySubtitleOffset shifts a served WebVTT by the Profile's persisted nudge for
// this track fingerprint. It fails OPEN: an unreadable nudge serves
// the unshifted subtitle rather than failing playback, because a missing shift is
// a minor sync annoyance while a refused sidecar is a broken track.
func (h *Handler) applySubtitleOffset(ctx context.Context, profileID store.ProfileID, fingerprint, body string) string {
	if fingerprint == "" || profileID == "" {
		return body
	}
	offsetMS, err := h.store.SubtitleOffset(ctx, profileID, fingerprint)
	if err != nil || offsetMS == 0 {
		return body
	}
	return subtitleingest.ShiftWebVTT(body, offsetMS)
}

// sidecarFilePath binds a recorded sidecar rel_path to the media file's own
// directory as the containment root (the path-escape guard, HARD CONSTRAINT). It
// returns the (root, member) pair and true only when the member name is a
// non-escaping relative name; a rel_path that climbs out ("..") or is absolute
// returns false, so the byte route 404s without touching the filesystem.
//
// It deliberately does NOT resolve the name to an absolute path. The previous
// form checked containment with EvalSymlinks and then handed the RESOLVED string
// to os.Open — two independent walks of the same name, with the whole gap between
// them available for swapping any component. Returning the root instead means the
// only walk that ever happens is openRooted's, from a held descriptor, with the
// final component bound to the entry it checked.
func sidecarFilePath(mediaPath, relPath string) (rootedPath, bool) {
	dir := filepath.Dir(mediaPath)
	if dir == "" || dir == "." {
		return rootedPath{}, false
	}
	member, err := newRootedPath(dir, relPath)
	if err != nil {
		return rootedPath{}, false
	}
	return member, true
}

// readGuardedSidecar reads a sidecar caption file into memory bounded by its
// format's byte ceiling. The length is taken from the DIRECTORY ENTRY and
// checked before the allocation, and the read is capped at that same length, so
// an oversize file is refused without ever becoming resident. That is the
// difference between a size cap and a memory bound: a 4 GiB file carrying a
// ".srt" extension is rejected from its stat, not after 4 GiB has been allocated
// and the 8 MiB cap is finally consulted. The resident bytes then take the
// content half of the boundary — format whitelist, re-checked size, and the
// crafted-ASS scope limits.
func readGuardedSidecar(p rootedPath, format string) ([]byte, error) {
	f, info, err := openRooted(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := subtitleingest.CheckSize(format, info.Size()); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, info.Size()))
	if err != nil {
		return nil, err
	}
	if err := subtitleingest.Guard(format, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// readFingerprintedSidecar reads a sidecar through the ingest-hardening boundary
// and returns its raw bytes alongside their track fingerprint (ADR-0194 §4 /
// SUBSYS-12). It is the SINGLE derivation of that key: the serve path
// (readSidecarAsWebVTT) and the nudge write path (SetSubtitleOffset) both go
// through it, so a stored offset can never be keyed on a hash the serve path
// would not compute for the same track.
func readFingerprintedSidecar(p rootedPath, format string) (raw []byte, fingerprint string, err error) {
	raw, err = readGuardedSidecar(p, format)
	if err != nil {
		return nil, "", err
	}
	fingerprint, err = store.SubtitleTrackFingerprint(bytes.NewReader(raw))
	if err != nil {
		return nil, "", err
	}
	return raw, fingerprint, nil
}

// serveRawSidecarFile streams a sidecar's bytes straight off the open file. It
// never buffers the payload, so a large file costs no server memory and range
// requests are served from disk.
func serveRawSidecarFile(w http.ResponseWriter, r *http.Request, name string, p rootedPath) {
	w.Header().Set("Content-Type", "application/octet-stream")
	if err := serveRooted(w, r, name, p, time.Time{}); err != nil {
		writeSidecarReadError(w, err)
	}
}

// writeSidecarReadError maps a sidecar read failure onto its HTTP status: a
// missing file is indistinguishable from an unknown track (404), while a
// boundary rejection or I/O fault is an explicit refusal to serve (503).
func writeSidecarReadError(w http.ResponseWriter, err error) {
	// A root-scoped refusal joins the missing-file arm on purpose: a rel_path
	// pointing at a symlink, an escaped member, or a replaced directory must look
	// exactly like a sidecar that was never recorded.
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, errRootScope) {
		http.Error(w, "unknown subtitle", http.StatusNotFound)
		return
	}
	http.Error(w, "subtitle unavailable", http.StatusServiceUnavailable)
}

// readSidecarAsWebVTT reads a sidecar caption file and returns its WebVTT bytes.
// A "vtt" sidecar is already WebVTT and returned verbatim; an "srt" sidecar is
// converted in-process: SubRip and WebVTT share cue structure, so the lossless
// transform is a "WEBVTT" header plus the comma→dot decimal separator in cue
// timestamps. This keeps the sidecar path free of an ffmpeg dependency (the
// muxed-stream path still uses ffmpeg, where stream demuxing is unavoidable).
//
// Every served sidecar passes the shared ingest-hardening boundary (ADR-0194
// §7): the raw bytes are format+size guarded before the in-process parse, and
// the resulting WebVTT is markup-sanitised before it can reach the browser DOM.
// The boundary fails CLOSED — an oversize or unwhitelisted sidecar returns an
// error rather than bytes.
// It also returns the track's FINGERPRINT — the content hash of the raw sidecar
// bytes — which keys the viewer's persisted offset
// nudge. Hashing the source bytes (not the projection) is what makes the key
// survive a rename or remux and change only under a real content/timing edit.
func readSidecarAsWebVTT(p rootedPath, format string) (body string, fingerprint string, err error) {
	raw, fingerprint, err := readFingerprintedSidecar(p, format)
	if err != nil {
		return "", "", err
	}
	// Legacy sidecars are routinely CP1252/Latin-1; normalise to UTF-8 before
	// the WebVTT transform so accented text does not render as mojibake.
	//
	text := subtitleingest.DecodeText(raw)
	vtt, err := sidecarTextToWebVTT(text, format)
	if err != nil {
		return "", "", err
	}
	return subtitleingest.SanitizeWebVTT(vtt), fingerprint, nil
}

// sidecarTextToWebVTT projects a decoded text sidecar into WebVTT by format:
// a .vtt is already WebVTT, an .srt takes the lossless comma→dot
// transform, and an .ass/.ssa is flattened (styling dropped for non-libass
// clients; libass engines take the raw-passthrough route instead). A bitmap
// format never reaches here — the byte route serves it raw.
//
// There is deliberately no branch for the empty format, even though the column
// carries DEFAULT ”. Scan is the only writer of a caption row and always
// records a non-empty format from the closed extension vocabulary, and the
// INSERT has named the column since the table was created, so the default can
// never be taken. An empty format would in any case be rejected by the ingest
// boundary one step earlier — Guard fails closed on it — so a branch here could
// not run.
func sidecarTextToWebVTT(text, format string) (string, error) {
	switch format {
	case "vtt":
		return text, nil
	case "ass", "ssa":
		return subtitleingest.FlattenASS(text), nil
	case "srt":
		return srtToWebVTT(text), nil
	default:
		return "", fmt.Errorf("mediahttp: no WebVTT projection for sidecar format %q", format)
	}
}

// isBitmapSubtitleFormat reports whether a sidecar caption format is a bitmap
// subtitle (ADR-0194 §5 Tier 2) delivered as raw bytes to the native/burn-in
// ladder rather than flattened to WebVTT.
func isBitmapSubtitleFormat(format string) bool {
	switch format {
	case "sup", "idx", "sub":
		return true
	default:
		return false
	}
}

// srtToWebVTT converts SubRip cue text to WebVTT. WebVTT timestamps use a dot
// decimal separator where SubRip uses a comma; everything else (cue numbering,
// cue text) is valid WebVTT as-is. A leading BOM is stripped so the header is
// the first bytes.
func srtToWebVTT(srt string) string {
	srt = strings.TrimPrefix(srt, "\ufeff")
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for _, line := range strings.Split(srt, "\n") {
		if strings.Contains(line, "-->") {
			line = strings.ReplaceAll(line, ",", ".")
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// HLS serves transcode playlist and segment bytes for a signed token URL.
// Segment URLs inside the playlist are relative, so every fetch stays under
// the same token path and re-verifies the live session scope (revocation or
// relock stops the stream mid-segment, ADR-0010).
func (h *Handler) HLS(w http.ResponseWriter, r *http.Request) {
	w = MarkStreamTokenResponseNonStorable(w)
	rest := strings.TrimPrefix(r.URL.Path, "/api/media/hls/")
	token, name, ok := strings.Cut(rest, "/")
	if !ok || token == "" || name == "" {
		http.Error(w, "missing stream token", http.StatusUnauthorized)
		return
	}
	grant, scope, err := h.verifyMediaGrant(r.Context(), token, streamtoken.TargetMediaHLS)
	if err != nil {
		http.Error(w, "invalid stream token", http.StatusUnauthorized)
		return
	}
	if h.sealedMedia(r, grant.MediaID, scope) {
		http.Error(w, "media unavailable", http.StatusNotFound)
		return
	}
	media, err := h.store.Media(r.Context(), grant.MediaID, scope.PlaybackFilter())
	if err != nil {
		http.Error(w, "media unavailable", http.StatusNotFound)
		return
	}
	library, err := h.mediaLibrary(r.Context(), media.LibraryID, scope.PlaybackFilter())
	if err != nil {
		http.Error(w, "media unavailable", http.StatusNotFound)
		return
	}
	if grant.PlaybackRenditionID != "" {
		h.servePrecomputedHLS(w, r, grant, scope, library, name)
		return
	}
	if library.Encrypted {
		http.Error(w, "stream unavailable", http.StatusNotFound)
		return
	}
	file, err := h.store.PrimaryMediaFile(r.Context(), grant.MediaID, scope.PlaybackFilter())
	if err != nil {
		http.Error(w, "media unavailable", http.StatusNotFound)
		return
	}
	streams, err := h.store.MediaFileStreams(r.Context(), file.ContentFingerprint)
	if err != nil {
		http.Error(w, "media unavailable", http.StatusNotFound)
		return
	}
	key := playback.SessionKey{
		SessionID:         grant.SessionID,
		MediaID:           grant.MediaID,
		ProfileCapability: grant.ProfileCapability,
	}
	// The audio-only-transcode verdict (ADR-0010 §5: video copied, only audio
	// transcoded) rides the signed grant, captured at mint where the capability
	// decision runs. The grant is authoritative because the master.m3u8 URL is
	// shared with the standard transcode: a pure-HLS player that fetches the
	// master before any audio-copy/* artifact must still seed the single
	// audio-copy rung, which the requested rung name alone cannot decide. The
	// rung-name signal stays a secondary check so a per-rung fetch under an
	// older token (minted without the flag) still seeds correctly.
	audioCopy := grant.AudioCopy || playback.IsAudioCopyArtifact(name)
	// The capacity-degrade verdict (ADR-0010 §6 "drop to this quality"; PANEL-16)
	// rides the signed grant, decided at mint where party membership + admission
	// are known. The byte layer holds only the grant, so a degraded session is
	// created capped to the lowest rung and admitted over the transcode cap.
	degraded := grant.Degraded
	// Cross-Profile preemption authority is server-decided and signed. The
	// optional client hint may lower a foreground grant, but cannot raise a
	// normal grant into an eviction-capable rank.
	priority := foregroundPriority(grant, r)
	burnSubtitleIndex := grant.BurnSubtitleIndex - 1
	audioProfile := playback.HLSAudioProfileFromGrant(grant)
	caps := playback.LadderCapsFromGrant(grant)
	path, err := h.hls.FilePathPriorityWithBurnSubtitleAudioLadderMap(r.Context(), key, file.Path, media.DurationSeconds, media.VideoWidth, media.VideoHeight, audioCopy, degraded, caps, playback.VideoSignalFromStreams(streams), burnSubtitleIndex, audioProfile, nil, priority, name)
	if err != nil {
		// An evicted session is an expected teardown (ADR-0012 §7 revocation,
		// idle reap, preemption), not a server fault. 503 is retryable, which
		// is wrong here: a revoked device would keep asking for segments. 410
		// tells the player this stream is gone for good.
		if errors.Is(err, playback.ErrSessionEvicted) {
			http.Error(w, "stream ended", http.StatusGone)
			return
		}
		slog.Error("hls serve failed", "error", err.Error())
		http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
		return
	}
	if strings.HasSuffix(name, ".m3u8") {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	} else {
		// init.mp4 and the fMP4 .m4s media segments are both MP4 bytes.
		w.Header().Set("Content-Type", "video/mp4")
	}
	// The session engine hands back a member of its own work directory. Serving it
	// through the root-scoped opener binds the bytes to that directory entry, so a
	// segment replaced by a link between the engine's publish and this read is
	// refused instead of streamed.
	if err := serveRooted(w, r, name, rootedFile(path), time.Time{}); err != nil {
		http.Error(w, "stream unavailable", rootScopeStatus(err))
		return
	}
}

func (h *Handler) servePrecomputedHLS(w http.ResponseWriter, r *http.Request, grant streamtoken.Grant, scope auth.Scope, library store.Library, name string) {
	rendition, err := h.store.PlaybackRendition(r.Context(), grant.PlaybackRenditionID, scope.PlaybackFilter())
	if err != nil || rendition.MediaID != grant.MediaID || rendition.Status != store.PlaybackRenditionReady {
		http.Error(w, "stream unavailable", http.StatusNotFound)
		return
	}
	member, ok := precomputedHLSPath(rendition.Path, name)
	if !ok {
		http.Error(w, "stream unavailable", http.StatusNotFound)
		return
	}
	info, err := statRooted(member)
	if err != nil {
		http.Error(w, "stream unavailable", http.StatusNotFound)
		return
	}
	if err := h.store.RecordPlaybackRenditionUse(r.Context(), rendition.ID, time.Now().UTC()); err != nil {
		http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
		return
	}
	if strings.HasSuffix(name, ".m3u8") {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	} else {
		w.Header().Set("Content-Type", "video/mp4")
	}
	if library.Encrypted {
		// The rendition file's immutable identity is the persisted rendition row
		// id plus the member name within it — never the file path.
		h.serveLibraryFile(w, r, library, member.String(), filepath.Base(member.rel), w.Header().Get("Content-Type"), "stream unavailable",
			encryptedClassGeneratedAsset, "playback-rendition/"+string(rendition.ID)+"/"+name)
		return
	}
	if h.hotSegments != nil && !strings.HasSuffix(name, ".m3u8") {
		h.servePrecomputedHotSegment(w, r, member, rendition.ID, name, info.ModTime())
		return
	}
	if err := serveRooted(w, r, filepath.Base(member.rel), member, info.ModTime()); err != nil {
		http.Error(w, "stream unavailable", rootScopeStatus(err))
		return
	}
}

func (h *Handler) servePrecomputedHotSegment(w http.ResponseWriter, r *http.Request, member rootedPath, renditionID store.PlaybackRenditionID, name string, modTime time.Time) {
	key := string(renditionID) + "/" + name
	data, cachedModTime, hit, fanout, lease := h.hotSegments.begin(key)
	defer lease.done()
	served := filepath.Base(member.rel)
	if hit {
		http.ServeContent(w, r, served, cachedModTime, bytes.NewReader(data))
		return
	}
	if fanout {
		// The cache is filled from the bound descriptor, never from a second walk
		// of the name: a segment swapped after the stat above cannot be the thing
		// that gets memoised and then handed to every other waiter in the fan-out.
		if file, _, err := openRooted(member); err == nil {
			data, readErr := io.ReadAll(file)
			_ = file.Close()
			if readErr == nil && h.hotSegments.store(key, data, modTime) {
				http.ServeContent(w, r, served, modTime, bytes.NewReader(data))
				return
			}
		}
	}
	if err := serveRooted(w, r, served, member, modTime); err != nil {
		http.Error(w, "stream unavailable", rootScopeStatus(err))
		return
	}
}

// precomputedHLSPath binds a requested rendition member name to the rendition
// directory as its containment root. The lexical checks below are a prefilter
// only — they reject the obviously hostile name before any syscall — while the
// containment itself is openRooted's descriptor-relative resolution of the
// returned pair. Returning the root instead of a joined string is what keeps the
// two from drifting: nothing downstream can re-derive the member any other way.
func precomputedHLSPath(root, name string) (rootedPath, bool) {
	member, err := newRootedPath(root, name)
	if err != nil {
		return rootedPath{}, false
	}
	return member, true
}

// ForegroundHeader is an advisory client hint. An explicit false value may
// lower a signed foreground grant when the playhead is no longer interactive;
// no value can raise a grant that the server minted as normal.
const ForegroundHeader = "X-Beamfall-Foreground"

func foregroundPriority(grant streamtoken.Grant, r *http.Request) playback.Priority {
	if !grant.Foreground {
		return playback.PriorityNormal
	}
	switch strings.ToLower(strings.TrimSpace(r.Header.Get(ForegroundHeader))) {
	case "0", "false":
		return playback.PriorityNormal
	default:
		return playback.PriorityForeground
	}
}

// Artwork serves an artwork derivative for a visible Media item. Unlike Direct
// it authenticates with the standard credential headers — clients control
// their image fetches, so no URL-carried token is needed, and the Media
// visibility check reveal-filters restricted artwork by construction.
// A `bucket` query parameter selects a size-bucketed derivative (ADR-0014
// vocabulary: w320/w640/w1280/original); absent means full size, so existing
// URLs are untouched. Derivatives are generated lazily on first request and
// cached; generation failures fall back to the full-size file.
func (h *Handler) Artwork(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/media/artwork/")
	mediaID, slot, ok := strings.Cut(rest, "/")
	bucket := r.URL.Query().Get("bucket")
	if !ok || mediaID == "" || !artwork.ValidSlot(slot) || !artwork.ValidBucket(bucket) {
		http.Error(w, "unknown artwork", http.StatusNotFound)
		return
	}
	scope, err := h.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	artMedia, err := h.store.Media(r.Context(), store.MediaID(mediaID), scope.PlaybackFilter())
	if err != nil {
		http.Error(w, "unknown artwork", http.StatusNotFound)
		return
	}
	// A Sealed Library serves no artwork (ADR-0098 area 2).
	if h.sealed != nil && h.sealed(artMedia.LibraryID) {
		http.Error(w, "unknown artwork", http.StatusNotFound)
		return
	}
	// Per-slot fallback resolves here, at the endpoint: a slot
	// with no stored file serves its fallback (poster) so clients never
	// re-implement the chain. The served slot also keys the derivative + the
	// content-addressed version below, so the ?v token matches the bytes sent.
	served, ok := h.artwork.ResolveSlot(store.MediaID(mediaID), slot)
	if !ok {
		http.Error(w, "unknown artwork", http.StatusNotFound)
		return
	}
	path, err := h.artwork.ArtworkDerivative(r.Context(), store.MediaID(mediaID), served, bucket)
	if err != nil {
		http.Error(w, "unknown artwork", http.StatusNotFound)
		return
	}
	version, versionOK := h.artwork.ArtworkVersion(store.MediaID(mediaID), served)
	if artworkNotModified(w, r, version, versionOK) {
		return
	}
	library, err := h.mediaLibrary(r.Context(), artMedia.LibraryID, scope.PlaybackFilter())
	if err != nil {
		http.Error(w, "unknown artwork", http.StatusNotFound)
		return
	}
	h.serveArtworkFile(w, r, library, path, "artwork/media/"+mediaID+"/"+served+"/"+bucket)
}

// EntityArtwork serves an artwork derivative for a visible Entity, mirroring
// Artwork exactly: credential-header auth, the Entity visibility check
// reveal-filters restricted artwork (a hidden Entity 404s indistinguishably
// from a missing one), and the same bucket vocabulary.
func (h *Handler) EntityArtwork(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/entity/artwork/")
	entityID, slot, ok := strings.Cut(rest, "/")
	bucket := r.URL.Query().Get("bucket")
	if !ok || entityID == "" || !artwork.ValidSlot(slot) || !artwork.ValidBucket(bucket) {
		http.Error(w, "unknown artwork", http.StatusNotFound)
		return
	}
	scope, err := h.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	entity, err := h.store.Entity(r.Context(), store.EntityID(entityID), scope.PlaybackFilter())
	if err != nil {
		http.Error(w, "unknown artwork", http.StatusNotFound)
		return
	}
	if h.sealed != nil && h.sealed(entity.LibraryID) {
		http.Error(w, "unknown artwork", http.StatusNotFound)
		return
	}
	served, ok := h.artwork.ResolveEntitySlot(store.EntityID(entityID), slot)
	if !ok {
		http.Error(w, "unknown artwork", http.StatusNotFound)
		return
	}
	path, err := h.artwork.EntityArtworkDerivative(r.Context(), store.EntityID(entityID), served, bucket)
	if err != nil {
		http.Error(w, "unknown artwork", http.StatusNotFound)
		return
	}
	version, versionOK := h.artwork.EntityArtworkVersion(store.EntityID(entityID), served)
	if artworkNotModified(w, r, version, versionOK) {
		return
	}
	library, err := h.mediaLibrary(r.Context(), entity.LibraryID, scope.PlaybackFilter())
	if err != nil {
		http.Error(w, "unknown artwork", http.StatusNotFound)
		return
	}
	h.serveArtworkFile(w, r, library, path, "artwork/entity/"+entityID+"/"+served+"/"+bucket)
}

// ArtworkCandidate serves a token-gated candidate preview. The raw candidate
// source URL is never redirected or echoed; it is fetched server-side through
// the wired hardened fetcher and cached under the candidate ID.
func (h *Handler) ArtworkCandidate(w http.ResponseWriter, r *http.Request) {
	w = MarkStreamTokenResponseNonStorable(w)
	rest := strings.TrimPrefix(r.URL.Path, "/api/media/artwork-candidate/")
	token, candidateID, ok := strings.Cut(rest, "/")
	bucket := r.URL.Query().Get("bucket")
	if !ok || token == "" || candidateID == "" {
		http.Error(w, "missing stream token", http.StatusUnauthorized)
		return
	}
	if !artwork.ValidBucket(bucket) {
		http.Error(w, "unknown artwork", http.StatusNotFound)
		return
	}
	grant, scope, err := h.verifyMediaGrant(r.Context(), token, streamtoken.TargetArtworkCandidate)
	if err != nil {
		http.Error(w, "invalid stream token", http.StatusUnauthorized)
		return
	}
	if h.sealedMedia(r, grant.MediaID, scope) {
		http.Error(w, "unknown artwork", http.StatusNotFound)
		return
	}
	media, err := h.store.Media(r.Context(), grant.MediaID, scope.PlaybackFilter())
	if err != nil {
		http.Error(w, "unknown artwork", http.StatusNotFound)
		return
	}
	library, err := h.mediaLibrary(r.Context(), media.LibraryID, scope.PlaybackFilter())
	if err != nil {
		http.Error(w, "unknown artwork", http.StatusNotFound)
		return
	}
	candidate, err := h.store.ArtworkCandidate(r.Context(), candidateID, scope.LibraryFilter())
	if err != nil || candidate.MediaID != grant.MediaID {
		http.Error(w, "unknown artwork", http.StatusNotFound)
		return
	}
	path, err := h.artworkCandidatePreviewPath(r.Context(), candidate, bucket)
	if err != nil {
		http.Error(w, "unknown artwork", http.StatusNotFound)
		return
	}
	h.serveArtworkFile(w, r, library, path, "artwork-candidate/"+candidate.ID+"/"+bucket)
}

func (h *Handler) artworkCandidatePreviewPath(ctx context.Context, candidate store.ArtworkCandidate, bucket string) (string, error) {
	cacheID := artwork.CandidateCacheID(candidate.ID)
	source := h.artwork.ArtworkPath(cacheID, artwork.SlotPoster)
	if !regularFileExists(source) {
		if h.artworkCandidateFetch == nil {
			return "", errors.New("artwork candidate fetcher unavailable")
		}
		data, err := h.artworkCandidateFetch.FetchResource(ctx, candidate.SourceURL)
		if err != nil {
			return "", err
		}
		if err := h.artwork.ImportCandidateArtwork(ctx, cacheID, data); err != nil {
			return "", err
		}
	}
	return h.artwork.ArtworkDerivative(ctx, cacheID, artwork.SlotPoster, bucket)
}

// artworkNotModified applies the content-addressed cache contract for an
// artwork response. The `v` query param is the wire's mtime
// version token; it is advisory — the path+slot still identify the file, so a
// missing or mismatched `v` never 404s, it just forgoes the long-cache header.
// When `v` matches the current slot version the response is immutable for a
// year (the URL changes when the art does) and carries an ETag; a matching
// If-None-Match short-circuits to 304 (reported via the bool so the caller
// skips serving bytes).
//
// A compatibility-bound request narrows that contract: the validator
// also covers the decision generation and the subject, under a revalidating
// directive, so a response authorised under one decision never satisfies a
// request made under another. A bound request whose decision cannot be
// established fails closed to no-store instead of the immutable default.
func artworkNotModified(w http.ResponseWriter, r *http.Request, currentVersion string, ok bool) bool {
	if artworkCacheUnestablished(r) {
		w.Header().Set("Cache-Control", "no-store")
		return false
	}
	v := r.URL.Query().Get("v")
	if v == "" || !ok || v != currentVersion {
		return false
	}
	validator, cacheControl := artworkCacheDirectives(r, v)
	etag := `"` + validator + `"`
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("ETag", etag)
	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}

// serveArtworkFile serves one artwork derivative with the shared 404/500
// semantics — "doesn't exist" and "restricted" must stay observably identical
// upstream of this point. immutableObjectID is the caller-derived identity
// (persisted media/entity/candidate ids + slot/bucket — never the path) used
// when the Library is Encrypted.
func (h *Handler) serveArtworkFile(w http.ResponseWriter, r *http.Request, library store.Library, path, immutableObjectID string) {
	h.serveLibraryFile(w, r, library, path, filepath.Base(path), "image/jpeg", "unknown artwork", encryptedClassGeneratedAsset, immutableObjectID)
}

// serveLibraryFile serves one server-derived library/asset member. The member is
// resolved root-scoped against the directory it was derived in, so the entry that
// is described (size, mod time, encrypted-plane lookup) and the entry whose bytes
// go out are bound to the same inode. A member whose final component is a symlink
// is refused here rather than followed — a generated-asset or artwork directory is
// a server-owned tree, and a link appearing in one is a swap, not a layout.
func (h *Handler) serveLibraryFile(w http.ResponseWriter, r *http.Request, library store.Library, path, name, contentType, notFoundMessage, objectClass, immutableObjectID string) {
	member := rootedFile(path)
	info, err := statRooted(member)
	if err != nil {
		http.Error(w, notFoundMessage, http.StatusNotFound)
		return
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if library.Encrypted {
		if h.encryptedBytes == nil {
			http.Error(w, notFoundMessage, http.StatusNotFound)
			return
		}
		plain, err := h.encryptedBytes.DecryptBytes(library.ID, member.String(), objectClass, immutableObjectID)
		if err != nil {
			http.Error(w, notFoundMessage, http.StatusNotFound)
			return
		}
		http.ServeContent(w, r, name, info.ModTime(), bytes.NewReader(plain))
		return
	}
	if err := serveRooted(w, r, name, member, info.ModTime()); err != nil {
		http.Error(w, notFoundMessage, http.StatusNotFound)
		return
	}
}

// regularFileExists reports whether path names a regular file, resolved through a
// descriptor on its own directory rather than by re-walking the name. A cache
// probe that followed a symlink would report a hit for content the server never
// generated.
func regularFileExists(path string) bool {
	return rootedRegularFileExists(rootedFile(path))
}

// Direct serves direct Media bytes for a signed token URL.
func (h *Handler) Direct(w http.ResponseWriter, r *http.Request) {
	w = MarkStreamTokenResponseNonStorable(w)
	token := strings.TrimPrefix(r.URL.Path, "/api/media/direct/")
	if token == "" || token == r.URL.Path {
		http.Error(w, "missing stream token", http.StatusUnauthorized)
		return
	}
	grant, scope, err := h.verifyMediaGrant(r.Context(), token, streamtoken.TargetMediaDirect)
	mediaID := grant.MediaID
	filter := scope.PlaybackFilter()
	if err != nil {
		if h.tokens == nil {
			http.Error(w, "invalid stream token", http.StatusUnauthorized)
			return
		}
		leaseContext, cancel, circleGrant, leaseErr := h.tokens.GuardCircleMediaLease(r.Context(), token, time.Second)
		if leaseErr != nil {
			http.Error(w, "invalid stream token", http.StatusUnauthorized)
			return
		}
		defer cancel(context.Canceled)
		r = r.WithContext(leaseContext)
		defer interruptResponseWritesOnContextDone(r.Context(), w)()
		mediaID = circleGrant.MediaID
		// CircleService already applied the shadow principal's full owner-side
		// policy before signing this exact Media subject. The live lease guard,
		// sealed check, and signed Media binding remain the byte-route gates.
		filter = store.LibraryFilter{AllLibraries: true, Revealed: true, PlaybackGate: true}
	}
	media, err := h.store.Media(r.Context(), mediaID, filter)
	if err != nil {
		http.Error(w, "media unavailable", http.StatusNotFound)
		return
	}
	if h.sealed != nil && h.sealed(media.LibraryID) {
		http.Error(w, "media unavailable", http.StatusNotFound)
		return
	}
	file, err := h.store.PrimaryMediaFile(r.Context(), mediaID, filter)
	if err != nil {
		http.Error(w, "media unavailable", http.StatusNotFound)
		return
	}
	library, err := h.mediaLibrary(r.Context(), media.LibraryID, filter)
	if err != nil {
		http.Error(w, "media unavailable", http.StatusNotFound)
		return
	}
	if library.Encrypted {
		if h.encryptedBytes == nil {
			http.Error(w, "media unavailable", http.StatusNotFound)
			return
		}
		// The object identity is the file's persisted content fingerprint —
		// never the path. A file without one has no immutable identity and
		// fails closed.
		if file.ContentFingerprint == "" {
			http.Error(w, "media unavailable", http.StatusNotFound)
			return
		}
		if streaming, ok := h.encryptedBytes.(EncryptedByteReadSeeker); ok {
			plain, err := streaming.OpenDecryptReader(library.ID, file.Path, encryptedClassMediaBytes, file.ContentFingerprint)
			if err != nil {
				http.Error(w, "media unavailable", http.StatusNotFound)
				return
			}
			defer plain.Close()
			http.ServeContent(w, r, filepath.Base(file.Path), file.ModTime, requestContextReadSeeker{ctx: r.Context(), ReadSeeker: plain})
			return
		}
		if r.Header.Get("Range") != "" {
			http.Error(w, "encrypted range requests unavailable", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		plain, err := h.encryptedBytes.DecryptBytes(library.ID, file.Path, encryptedClassMediaBytes, file.ContentFingerprint)
		if err != nil {
			http.Error(w, "media unavailable", http.StatusNotFound)
			return
		}
		http.ServeContent(w, r, filepath.Base(file.Path), file.ModTime, requestContextReadSeeker{ctx: r.Context(), ReadSeeker: bytes.NewReader(plain)})
		return
	}
	// The cataloged path is opened root-scoped against its own directory and bound
	// to that directory entry, so the file the catalog authorised and the file
	// whose bytes leave the process are the same inode — a media name replaced by
	// a link to an unauthorised file after the catalog lookup is refused.
	f, info, err := openRooted(rootedFile(file.Path))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, errRootScope) {
			status = http.StatusNotFound
		}
		if status >= http.StatusInternalServerError {
			slog.Error("media byte serve failed", "status", status)
			slog.Debug("media byte serve failure detail", "error", redactMediaPathError(err, file.Path))
		}
		http.Error(w, "media file unavailable", status)
		return
	}
	defer f.Close()

	http.ServeContent(w, r, info.Name(), info.ModTime(), requestContextReadSeeker{ctx: r.Context(), ReadSeeker: f})
}

type requestContextReadSeeker struct {
	ctx context.Context
	io.ReadSeeker
}

func (r requestContextReadSeeker) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, context.Cause(r.ctx)
	default:
		return r.ReadSeeker.Read(p)
	}
}

func interruptResponseWritesOnContextDone(ctx context.Context, w http.ResponseWriter) func() {
	finished := make(chan struct{})
	stopped := make(chan struct{})
	controller := http.NewResponseController(w)
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			_ = controller.SetWriteDeadline(time.Now())
		case <-finished:
		}
	}()
	return func() {
		close(finished)
		<-stopped
		_ = controller.SetWriteDeadline(time.Time{})
	}
}

func redactMediaPathError(err error, path string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if path != "" {
		msg = strings.ReplaceAll(msg, path, "[media-path]")
	}
	return msg
}

func (h *Handler) mediaLibrary(ctx context.Context, libraryID store.LibraryID, filter store.LibraryFilter) (store.Library, error) {
	libraries, err := h.store.Libraries(ctx, filter)
	if err != nil {
		return store.Library{}, err
	}
	for _, library := range libraries {
		if library.ID == libraryID {
			return library, nil
		}
	}
	return store.Library{}, errors.New("library unavailable")
}
