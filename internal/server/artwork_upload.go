package server

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg" // register JPEG config and full decoders (matches v1.go's backup-upload pattern)
	_ "image/png"  // register PNG config and full decoders
	"io"
	"net/http"
	"strings"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// artworkUploadPattern is the multipart create-Media artwork upload route.
// It is referenced from both the route table and the
// JSON-Content-Type exclusion in handle() below, mirroring how
// "/v1/backups/media" is excluded — a multipart body is never
// application/json.
const artworkUploadPattern = "/api/media/artwork/upload"

// Upload limits mirror the fail-closed sniff-then-cap shape already
// established by the backup-upload route (v1.go maxBackupUploadBody /
// maxBackupMultipartMemory / maxBackupImageSniffBytes / maxBackupImagePixels)
// — the only existing binary-upload boundary in this codebase — sized down
// for a single artwork image rather than a full backup payload.
const (
	maxArtworkSlotUploadBody  = 16 << 20    // 16 MiB overall body cap
	maxArtworkMultipartMemory = 1 << 20     // 1 MiB in-memory multipart threshold
	maxArtworkImageSniffBytes = 1 << 20     // 1 MiB read for image.DecodeConfig sniffing
	maxArtworkImagePixels     = 100_000_000 // decompression-bomb ceiling, same as the backup route
)

// ArtworkImporter prepares a validated upload against the visible Media. The
// GraphQL endpoint is wired in as an opaque http.Handler, so New asserts this
// capability structurally rather than importing internal/graph.
type ArtworkImporter interface {
	PrepareUploadedArtwork(
		ctx context.Context,
		scope auth.Scope,
		mediaID store.MediaID,
		slot string,
		data []byte,
	) (store.ArtworkCandidateApply, error)
}

type artworkApplyStore interface {
	ApplyArtworkCandidate(context.Context, store.ArtworkCandidateApply) error
}

// uploadArtwork handles the create-Media artwork upload for a hand-authored,
// no-match Media. It is session+CSRF+
// manager-gated like the other web-Console write routes, validates the
// payload fail-closed (multipart required, real decodable image, bounded
// pixels) before any bytes reach storage, and then hands the already-read
// bytes to the ArtworkImporter — the same import + candidate-apply mechanism
// pinArtworkCandidate uses for a fetched scrape candidate.
func (s *Server) uploadArtwork(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxArtworkSlotUploadBody)
	if r.Header.Get(auth.CSRFHeader) == "" {
		writeError(w, r, http.StatusForbidden, "missing CSRF header")
		return
	}
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}
	// Manager-gated, session-bound only — matches pinArtworkCandidate's
	// roleSession|roleManager gate (an admin token or scoped API key is
	// rejected the same way moderationScope rejects them above).
	if scope.APITokenID != "" || scope.APIKeyID != "" || scope.DeviceRole != store.DeviceRoleManager {
		writeError(w, r, http.StatusForbidden, "manager session required")
		return
	}
	if s.artworkImport == nil || s.artworkApply == nil {
		writeError(w, r, http.StatusServiceUnavailable, "artwork upload unavailable")
		return
	}
	if err := r.ParseMultipartForm(maxArtworkMultipartMemory); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	defer r.MultipartForm.RemoveAll()
	mediaID := strings.TrimSpace(r.FormValue("mediaID"))
	slot := strings.TrimSpace(r.FormValue("slot"))
	// The slot vocabulary (artwork.ValidSlot) is owned by internal/graph, not
	// here: internal/server may not import internal/artwork (ARCHITECTURE.md
	// §4), so an unknown slot is left to ImportUploadedArtwork's own check
	// below, which returns the same store.ValidationError -> 400 either way.
	if mediaID == "" || slot == "" {
		writeError(w, r, http.StatusBadRequest, "mediaID and a known artwork slot are required")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	if header.Size <= 0 {
		writeError(w, r, http.StatusBadRequest, "file is empty")
		return
	}
	data, err := readAndValidateArtworkImage(file)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	apply, err := s.artworkImport.PrepareUploadedArtwork(
		r.Context(), scope, store.MediaID(mediaID), slot, data,
	)
	if err == nil {
		err = s.artworkApply.ApplyArtworkCandidate(r.Context(), apply)
	}
	if err != nil {
		var verr store.ValidationError
		if errors.As(err, &verr) {
			writeError(w, r, http.StatusBadRequest, string(verr))
			return
		}
		writeErrorCause(w, r, http.StatusInternalServerError, "artwork upload failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"uploaded": true})
}

// readAndValidateArtworkImage sniffs the upload with image.DecodeConfig,
// rejects unsupported formats and a decompression-bomb pixel count, then
// fully decodes the reassembled payload before returning its bytes. The full
// decoder must agree with the config decoder, so a valid header followed by
// truncated or mismatched image data never reaches storage.
func readAndValidateArtworkImage(file io.Reader) ([]byte, error) {
	prefix := make([]byte, maxArtworkImageSniffBytes)
	n, err := io.ReadFull(file, prefix)
	switch {
	case err == nil, errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		prefix = prefix[:n]
	default:
		return nil, errors.New("unable to read upload")
	}
	cfg, configFormat, err := image.DecodeConfig(bytes.NewReader(prefix))
	if err != nil {
		return nil, errors.New("unsupported image")
	}
	if configFormat != "jpeg" && configFormat != "png" {
		return nil, errors.New("unsupported image")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxArtworkImagePixels {
		return nil, errors.New("image dimensions exceed upload limit")
	}
	data, err := io.ReadAll(io.MultiReader(bytes.NewReader(prefix), file))
	if err != nil {
		return nil, errors.New("unable to read upload")
	}
	_, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("unsupported image")
	}
	if decodedFormat != configFormat {
		return nil, errors.New("unsupported image")
	}
	return data, nil
}
