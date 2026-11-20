// Package proxytee streams an origin enclosure through a Stream-Token'd proxy
// to the client while teeing the same bytes to disk, then promotes the teed
// copy from provisional to archived in place (ADR-0052 §2.1-2.2).
//
// The thesis: tapping a provisional item plays it NOW — the server
// ranges the origin enclosure through a Stream-Token'd endpoint to the client
// for instant playback AND writes the same bytes to disk, so by the time it has
// been listened to, it is archived. The streaming path delivers the archive
// rather than gating playback behind a full download.
//
// Invariants honoured here:
//   - The Stream Token fronts the PROXY, never the origin URL (the client never
//     sees the enclosure URL) — ADR-0010 / invariant on Reveal.
//   - The origin fetch goes through the SAME ADR-0012 §9 SSRF gate as every
//     other server-side fetch (the OriginOpener is *scrape.Engine).
//   - The origin URL is a server-only secret: held in memory, never persisted,
//     never logged (invariant #5/#7).
//   - An abandoned mid-stream partial is NEVER auto-promoted:
//     promotion runs only on a full, length-verified body.
//   - Domain-agnostic: "provisional Media", "origin enclosure" are generic; no
//     podcast vocabulary (invariant #1).
package proxytee

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

// OriginOpener opens a vetted origin enclosure for streaming, forwarding the
// client's Range header. Satisfied by *scrape.Engine (its OpenOriginRange runs
// the ADR-0012 §9 SSRF gate). Kept as an interface so tests can supply a local
// httptest origin without the live HTTP client.
type OriginOpener interface {
	OpenOriginRange(ctx context.Context, rawURL, rangeHeader string) (scrape.OriginResponse, error)
}

// Stream is the server-only record of one provisional play: the origin URL to
// range (never persisted, never client-visible), the in-progress tee path, and
// the final archive path promotion moves the completed copy into. archiveDir is
// derived once at Register time.
type stream struct {
	originURL       string
	teePath         string
	teeRelative     string
	archivePath     string
	archiveRelative string
	generation      uint64
}

type streamSlot struct {
	current     *stream
	owner       bool
	registering bool
	changed     chan struct{}
}

// Service serves the proxy-tee endpoint and owns the provisional-stream
// registry. Construct with New and wire into mediahttp's route table.
type Service struct {
	store               *store.Store
	auth                *auth.Service
	tokens              *streamtoken.Service
	origin              OriginOpener
	archiveDir          string
	archiveRoot         *os.Root
	archiveInfo         os.FileInfo
	closeOnce           sync.Once
	closeErr            error
	beforeCAS           func()
	afterCAS            func()
	registerProvisional func(context.Context, store.ProvisionalMedia) (store.MediaID, string, error)

	mu    sync.Mutex
	slots map[store.MediaID]*streamSlot
}

var closeTeeFile = func(f *os.File) error { return f.Close() }

var errArchiveRootChanged = errors.New("configured archive root changed")

const registrationOwnerWait = 500 * time.Millisecond

// RegistrationBusyError means a promotion or registration still owns the
// Media's slot after the bounded refresh wait.
type RegistrationBusyError struct {
	MediaID store.MediaID
}

func (e *RegistrationBusyError) Error() string {
	return "provisional registration busy: " + string(e.MediaID)
}

// New builds a proxy-tee service. archiveDir is the sole root under which
// partial and completed copies may be created or promoted.
func New(st *store.Store, authService *auth.Service, tokens *streamtoken.Service, origin OriginOpener, archiveDir string) (*Service, error) {
	absoluteRoot, err := filepath.Abs(archiveDir)
	if err != nil || archiveDir == "" {
		return nil, errors.New("archive root is required")
	}
	parent, err := os.OpenRoot(filepath.Dir(absoluteRoot))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	rootName := filepath.Base(absoluteRoot)
	if err := parent.MkdirAll(rootName, 0o700); err != nil {
		return nil, err
	}
	pathInfo, err := parent.Lstat(rootName)
	if err != nil {
		return nil, err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.IsDir() {
		return nil, errors.New("archive root must be a non-symlink directory")
	}
	archiveRoot, err := parent.OpenRoot(rootName)
	if err != nil {
		return nil, err
	}
	archiveInfo, err := archiveRoot.Stat(".")
	if err != nil {
		_ = archiveRoot.Close()
		return nil, err
	}
	pathInfo, err = os.Lstat(absoluteRoot)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(archiveInfo, pathInfo) {
		_ = archiveRoot.Close()
		return nil, errArchiveRootChanged
	}
	return &Service{
		store:               st,
		auth:                authService,
		tokens:              tokens,
		origin:              origin,
		archiveDir:          absoluteRoot,
		archiveRoot:         archiveRoot,
		archiveInfo:         archiveInfo,
		slots:               map[store.MediaID]*streamSlot{},
		registerProvisional: st.RegisterProvisionalMedia,
	}, nil
}

func (s *Service) checkArchiveRoot() error {
	pathInfo, err := os.Lstat(s.archiveDir)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.IsDir() || !os.SameFile(s.archiveInfo, pathInfo) {
		return errArchiveRootChanged
	}
	heldInfo, err := s.archiveRoot.Stat(".")
	if err != nil || !os.SameFile(s.archiveInfo, heldInfo) {
		return errArchiveRootChanged
	}
	return nil
}

// Close releases the archive root descriptor. It is safe to call repeatedly.
func (s *Service) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.archiveRoot.Close() })
	return s.closeErr
}

func (s *Service) relativeArchivePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(s.archiveDir, absolute)
	if err != nil || relative == "." || !filepath.IsLocal(relative) {
		return "", errors.New("archive path must be beneath the configured archive root")
	}
	return relative, nil
}

// Registration describes a first-play provisional to stream-and-archive.
type Registration struct {
	LibraryID store.LibraryID
	// GUID is the feed entry's identity External ID; it keys the
	// durable Media ID, preserved across promotion.
	GUID string
	// Title seeds the provisional Media row.
	Title string
	// Container/Codec/Class seed playback before the real probe; "" defaults to
	// the audio enclosure common case in the store writer.
	Container string
	Codec     string
	Class     store.MediaClass
	// OriginURL is the enclosure to range. Server-only secret: never persisted,
	// never returned to a client, never logged.
	OriginURL string
	// ArchivePath is where the completed copy is promoted in place; the tee
	// writes to "<ArchivePath>.partial" until completion.
	ArchivePath string
}

// Register materializes the provisional Media (store.CreateProvisionalMedia)
// and records the origin URL + paths in the in-memory registry so a subsequent
// proxy fetch for this Media can range the origin and tee to disk. It returns
// the durable Media ID, which a Stream Token then fronts. Idempotent on the
// (LibraryID, GUID)-derived Media ID.
func (s *Service) Register(ctx context.Context, reg Registration) (store.MediaID, error) {
	if reg.OriginURL == "" {
		return "", errors.New("origin URL is required")
	}
	if reg.ArchivePath == "" {
		return "", errors.New("archive path is required")
	}
	archiveRelative, err := s.relativeArchivePath(reg.ArchivePath)
	if err != nil {
		return "", err
	}
	if err := s.checkArchiveRoot(); err != nil {
		return "", err
	}
	if err := s.archiveRoot.MkdirAll(filepath.Dir(archiveRelative), 0o700); err != nil {
		return "", err
	}
	archivePath := filepath.Join(s.archiveDir, archiveRelative)
	teeRelative := archiveRelative + ".partial"
	teePath := archivePath + ".partial"
	stableID := store.ProvisionalMediaID(reg.LibraryID, reg.GUID)

	// A promotion owner pins its generation until the store CAS completes. Wait
	// briefly for the common completion case, but never for the whole stream.
	// Re-read the map after every wake because promotion can delete the old slot.
	wait := time.NewTimer(registrationOwnerWait)
	defer wait.Stop()
	var slot *streamSlot
	var observed *stream
	for {
		s.mu.Lock()
		slot = s.slots[stableID]
		if slot == nil {
			slot = &streamSlot{changed: make(chan struct{})}
			s.slots[stableID] = slot
		}
		if !slot.owner && !slot.registering {
			slot.registering = true
			observed = slot.current
			s.mu.Unlock()
			break
		}
		changed := slot.changed
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-wait.C:
			return "", &RegistrationBusyError{MediaID: stableID}
		case <-changed:
		}
	}
	if err := s.checkArchiveRoot(); err != nil {
		s.releaseRegistration(stableID, slot)
		return "", err
	}
	mediaID, previousPath, err := s.registerProvisional(ctx, store.ProvisionalMedia{
		LibraryID: reg.LibraryID,
		GUID:      reg.GUID,
		Title:     reg.Title,
		Container: reg.Container,
		Codec:     reg.Codec,
		Class:     reg.Class,
		TeePath:   teePath,
	})
	if err != nil {
		s.releaseRegistration(stableID, slot)
		return "", err
	}
	if err := ctx.Err(); err != nil {
		compensateErr := s.compensateRegistration(ctx, mediaID, teePath, previousPath)
		s.releaseRegistration(stableID, slot)
		return "", errors.Join(err, compensateErr)
	}

	s.mu.Lock()
	live := s.slots[stableID]
	if live != slot || !slot.registering || slot.current != observed {
		s.mu.Unlock()
		compensateErr := s.compensateRegistration(ctx, mediaID, teePath, previousPath)
		s.releaseRegistration(stableID, slot)
		return "", errors.Join(&RegistrationBusyError{MediaID: stableID}, compensateErr)
	}
	generation := uint64(1)
	if slot.current != nil {
		generation = slot.current.generation + 1
	}
	slot.current = &stream{
		originURL:       reg.OriginURL,
		teePath:         teePath,
		teeRelative:     teeRelative,
		archivePath:     archivePath,
		archiveRelative: archiveRelative,
		generation:      generation,
	}
	slot.registering = false
	close(slot.changed)
	slot.changed = make(chan struct{})
	s.mu.Unlock()
	if previousPath != "" && previousPath != teePath {
		if previousRelative, relativeErr := s.relativeArchivePath(previousPath); relativeErr == nil && s.checkArchiveRoot() == nil {
			_ = s.archiveRoot.Remove(previousRelative)
		}
	}
	return mediaID, nil
}

func (s *Service) releaseRegistration(mediaID store.MediaID, slot *streamSlot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slots[mediaID] != slot || !slot.registering {
		return
	}
	slot.registering = false
	close(slot.changed)
	if slot.current == nil {
		delete(s.slots, mediaID)
		return
	}
	slot.changed = make(chan struct{})
}

func (s *Service) compensateRegistration(ctx context.Context, mediaID store.MediaID, registeredPath, previousPath string) error {
	compensated, err := s.store.CompensateProvisionalMediaRegistration(context.WithoutCancel(ctx), mediaID, registeredPath, previousPath)
	if err != nil || compensated {
		return err
	}
	return errors.New("provisional registration compensation lost ownership")
}

func (s *Service) lookup(mediaID store.MediaID) (*stream, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	slot := s.slots[mediaID]
	if slot == nil || slot.current == nil {
		return nil, false
	}
	return slot.current, true
}

func (s *Service) acquirePromotion(mediaID store.MediaID) (*stream, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	slot := s.slots[mediaID]
	if slot == nil || slot.current == nil || slot.owner || slot.registering {
		return nil, false
	}
	slot.owner = true
	return slot.current, true
}

func (s *Service) releasePromotion(mediaID store.MediaID, generation uint64, promoted bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	slot := s.slots[mediaID]
	if slot == nil || slot.current == nil || slot.current.generation != generation {
		return
	}
	if promoted {
		// Wake registrations waiting on this generation before removing its slot;
		// each waiter re-reads the map after the channel wake.
		slot.owner = false
		close(slot.changed)
		delete(s.slots, mediaID)
		return
	}
	slot.owner = false
	close(slot.changed)
	slot.changed = make(chan struct{})
}

// ServeProxy is the Stream-Token'd proxy endpoint at /api/media/proxy/<token>.
// It verifies the token against the live session scope (the token fronts the
// PROXY — the origin URL is resolved server-side from the registry, never from
// the token or the client), opens the origin enclosure with the client's Range
// forwarded, streams the bytes to the client, and tees them to disk. On a full,
// length-verified body it fingerprints the teed file and promotes the Media in
// place (MediaID preserved, provisional cleared). On a partial (client
// disconnect, truncated origin) it keeps resumable bytes and never promotes.
func (s *Service) ServeProxy(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/api/media/proxy/")
	if token == "" || token == r.URL.Path {
		http.Error(w, "missing stream token", http.StatusUnauthorized)
		return
	}
	authenticated, err := s.tokens.Authenticate(token, streamtoken.TargetMediaProxy)
	if err != nil {
		http.Error(w, "invalid stream token", http.StatusUnauthorized)
		return
	}
	grant := authenticated.Grant()
	scope, err := s.auth.Scope(r.Context(), grant.SessionID)
	if err != nil {
		http.Error(w, "invalid stream token", http.StatusUnauthorized)
		return
	}
	grant, err = s.tokens.VerifyAuthenticated(authenticated, scope)
	if err != nil {
		http.Error(w, "invalid stream token", http.StatusUnauthorized)
		return
	}
	// The Media must be visible to the verifying scope (reveal-filtered) before
	// any origin byte is fetched — a restricted provisional never streams to an
	// unrevealed session.
	if _, err := s.store.Media(r.Context(), grant.MediaID, scope.PlaybackFilter()); err != nil {
		http.Error(w, "media unavailable", http.StatusNotFound)
		return
	}
	st, ok := s.lookup(grant.MediaID)
	if !ok {
		// No active provisional stream for this Media (already archived, or the
		// registry entry was lost across a restart). The entry stays replayable
		// — a higher layer re-registers it; the proxy itself just reports gone.
		http.Error(w, "stream unavailable", http.StatusNotFound)
		return
	}

	rangeReq := strings.TrimSpace(r.Header.Get("Range")) != ""
	owner := false
	if !rangeReq {
		if owned, acquired := s.acquirePromotion(grant.MediaID); acquired {
			st, owner = owned, true
		}
	}
	promoted := false
	if owner {
		defer func() { s.releasePromotion(grant.MediaID, st.generation, promoted) }()
	}

	origin, err := s.origin.OpenOriginRange(r.Context(), st.originURL, r.Header.Get("Range"))
	if err != nil {
		// Never echo the origin URL or its error verbatim (it can carry feed
		// credentials) — OpenOriginRange already sanitised it, but keep the
		// client-facing message generic.
		http.Error(w, "origin unavailable", http.StatusBadGateway)
		return
	}
	defer origin.Body.Close()

	// Exactly one full-resource request owns the archive writer. Range requests
	// and concurrent peers remain client-only, so they cannot corrupt .partial.
	var teeFile *os.File
	var teeErr error
	if owner {
		teeFile, teeErr = s.openTee(st.teeRelative)
	}
	// A tee open failure must not break playback: serve the client anyway,
	// just without archiving this pass (the entry stays replayable).
	var teeWriter io.Writer
	teeClosed := false
	if owner && teeErr == nil {
		teeWriter = teeFile
		defer func() {
			if !teeClosed {
				_ = closeTeeFile(teeFile)
			}
		}()
	}

	// Forward the origin's range/type headers so the client gets a faithful
	// proxy (seek works, content type is preserved).
	if origin.ContentType != "" {
		w.Header().Set("Content-Type", origin.ContentType)
	}
	if origin.AcceptRanges != "" {
		w.Header().Set("Accept-Ranges", origin.AcceptRanges)
	}
	if origin.ContentRange != "" {
		w.Header().Set("Content-Range", origin.ContentRange)
	}
	w.WriteHeader(origin.StatusCode)

	// Stream to the client and tee to disk in one pass. io.MultiWriter fans the
	// origin body to both; a client disconnect surfaces as a write error and
	// stops the copy with a partial on disk.
	var sink io.Writer = w
	if teeWriter != nil {
		sink = io.MultiWriter(w, teeWriter)
	}
	written, copyErr := io.Copy(sink, origin.Body)

	// Promotion gate: only a full, length-verified, full-resource (not a ranged
	// sub-request) body is promoted. A ranged request, a short read, or a copy
	// error (client gone / origin truncated) leaves the provisional in place and
	// keeps this pass's resumable bytes — never an auto-promotion of a partial.
	full := copyErr == nil &&
		owner &&
		teeWriter != nil &&
		origin.StatusCode == http.StatusOK &&
		origin.TotalSize > 0 &&
		written == origin.TotalSize
	if !full {
		return
	}
	teeInfo, err := teeFile.Stat()
	if err != nil || !teeInfo.Mode().IsRegular() || teeInfo.Size() != origin.TotalSize {
		return
	}
	// Close the tee before fingerprinting + moving so the bytes are flushed.
	if err := closeTeeFile(teeFile); err != nil {
		return
	}
	teeClosed = true
	if err := s.promote(r.Context(), grant.MediaID, st, teeInfo); err != nil {
		// Promotion failed (hash/move/store error): leave the provisional intact
		// and the partial on disk so a retry can promote it. Do not surface to
		// the client — its bytes already arrived.
		return
	}
	promoted = true
}

// promote fingerprints the completed tee file, moves it into the archive path,
// and flips the Media's media_file from provisional to archived in place
// (MediaID preserved). The fingerprint is the SAME content hash a scan computes
// (store.HashMediaFileContent), so a streamed-then-archived file is identity-
// equivalent to a downloaded or scanned one.
func (s *Service) promote(ctx context.Context, mediaID store.MediaID, st *stream, expected os.FileInfo) error {
	if err := s.checkArchiveRoot(); err != nil {
		return err
	}
	if err := s.archiveRoot.MkdirAll(filepath.Dir(st.archiveRelative), 0o700); err != nil {
		return err
	}
	sourceInfo, err := s.archiveRoot.Lstat(st.teeRelative)
	if err != nil || !sourceInfo.Mode().IsRegular() || !os.SameFile(expected, sourceInfo) {
		return errors.New("archive partial identity changed")
	}
	if err := s.archiveRoot.Rename(st.teeRelative, st.archiveRelative); err != nil {
		return err
	}
	promoted, err := s.openPromotedRegular(st.archiveRelative, expected)
	if err != nil {
		_ = s.discardChangedPromotion(st.archiveRelative)
		return err
	}
	defer promoted.Close()
	fingerprint, err := hashOpenFile(promoted)
	if err != nil {
		return errors.Join(err, s.rollbackPromotion(st, expected))
	}
	if err := s.checkArchiveRoot(); err != nil {
		return errors.Join(err, s.rollbackPromotion(st, expected))
	}
	if err := s.checkRegularIdentity(st.archiveRelative, expected); err != nil {
		_ = s.discardChangedPromotion(st.archiveRelative)
		return err
	}
	if s.beforeCAS != nil {
		s.beforeCAS()
	}
	updated, err := s.store.PromoteProvisionalMediaGeneration(ctx, mediaID, st.teePath, fingerprint, st.archivePath)
	if err != nil {
		return errors.Join(err, s.rollbackPromotion(st, expected))
	}
	if !updated {
		return errors.Join(store.ErrProvisionalRegistrationConflict, s.rollbackPromotion(st, expected))
	}
	if s.afterCAS != nil {
		s.afterCAS()
	}
	if err := s.validatePublication(st.archiveRelative, expected, promoted); err != nil {
		compensated, compensationErr := s.store.CompensateProvisionalMediaPromotion(
			context.WithoutCancel(ctx), mediaID, fingerprint, st.archivePath, st.teePath,
		)
		if compensationErr != nil {
			return errors.Join(err, compensationErr)
		}
		if !compensated {
			return errors.Join(err, store.ErrProvisionalRegistrationConflict)
		}
		if rootErr := s.checkArchiveRoot(); rootErr != nil {
			closeErr := promoted.Close()
			return errors.Join(err, rootErr, closeErr, s.discardChangedPromotion(st.archiveRelative))
		}
		return errors.Join(err, s.rollbackPromotion(st, expected))
	}
	return nil
}

func (s *Service) validatePublication(relative string, expected os.FileInfo, promoted *os.File) error {
	if err := s.checkArchiveRoot(); err != nil {
		return err
	}
	fileInfo, err := promoted.Stat()
	if err != nil || !fileInfo.Mode().IsRegular() || !os.SameFile(expected, fileInfo) {
		return errors.New("published archive no longer matches hashed file")
	}
	return s.checkRegularIdentity(relative, fileInfo)
}

func (s *Service) openPromotedRegular(relative string, expected os.FileInfo) (*os.File, error) {
	if err := s.checkRegularIdentity(relative, expected); err != nil {
		return nil, err
	}
	file, err := s.archiveRoot.Open(relative)
	if err != nil {
		return nil, err
	}
	fileInfo, err := file.Stat()
	if err != nil || !fileInfo.Mode().IsRegular() || !os.SameFile(expected, fileInfo) {
		_ = file.Close()
		return nil, errors.New("promoted archive is not the completed regular file")
	}
	if err := s.checkRegularIdentity(relative, fileInfo); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func (s *Service) checkRegularIdentity(relative string, expected os.FileInfo) error {
	info, err := s.archiveRoot.Lstat(relative)
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(expected, info) {
		return errors.New("promoted archive identity changed")
	}
	return nil
}

func (s *Service) rollbackPromotion(st *stream, expected os.FileInfo) error {
	if err := s.checkArchiveRoot(); err != nil {
		return errors.Join(err, s.discardChangedPromotion(st.archiveRelative))
	}
	if err := s.checkRegularIdentity(st.archiveRelative, expected); err != nil {
		return errors.Join(err, s.discardChangedPromotion(st.archiveRelative))
	}
	return s.archiveRoot.Rename(st.archiveRelative, st.teeRelative)
}

func (s *Service) discardChangedPromotion(relative string) error {
	err := s.archiveRoot.Remove(relative)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func hashOpenFile(file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// openTee replaces an abandoned partial and then exclusively creates the sole
// full-response tee target. Root confinement prevents ancestor symlink escape;
// O_EXCL prevents a final-component symlink insertion race after removal.
func (s *Service) openTee(relative string) (*os.File, error) {
	if err := s.checkArchiveRoot(); err != nil {
		return nil, err
	}
	if err := s.archiveRoot.MkdirAll(filepath.Dir(relative), 0o700); err != nil {
		return nil, err
	}
	if err := s.archiveRoot.Remove(relative); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return s.archiveRoot.OpenFile(relative, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
}
