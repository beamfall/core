// Package genassets generates and locates Generated Assets: the
// second asset family — trickplay sprite sheets, the per-Media preview clip,
// and Marker thumbnails. The family rule: Artwork is chosen; Generated Assets
// are derived. No candidates, no upload, no fallback chain; everything here is
// keyed by the backing file's content fingerprint (a quality-upgrade
// replacement regenerates, metadata edits never do) and served only under
// Stream Tokens.
package genassets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"

	"github.com/beamfall/core/internal/store"
)

// Trickplay sprite spec. Only the manifest shape and "JPEG
// sheets" are client commitments; interval, cap, and grid are server tunables.
const (
	// TileWidth is the sprite tile width in pixels (aspect-preserved height).
	TileWidth = 320
	// SheetColumns × SheetRows is the per-sheet tile grid.
	SheetColumns = 10
	SheetRows    = 10
	// defaultIntervalSeconds is the sampling interval (the Roku BIF default).
	defaultIntervalSeconds = 10
	// frameCap bounds total frames: on ultra-long files the interval widens
	// rather than the sheet count exploding.
	frameCap = 1200
)

// Preview clip spec: silent H.264 MP4, single 640px derivative,
// capped at 24fps; ~12 segments × ~0.8s evenly spaced across the runtime,
// skipping the first and last 5%. Media shorter than ~30s gets one contiguous
// clip — the recipe degrades gracefully rather than gating.
const (
	previewWidth          = 640
	previewMaxFPS         = 24
	previewSegments       = 12
	previewSegmentSeconds = 0.8
	previewEdgeSkip       = 0.05
	previewShortSeconds   = 30
)

// markerThumbWidth sizes Marker thumbnails like the w320 artwork bucket — a
// marker wall renders cards, not heroes.
const markerThumbWidth = 320

// Asset file names within a fingerprint's directory. ManifestName and
// PreviewClipName are wire vocabulary; sheet and marker names follow the
// patterns validAssetName accepts.
const (
	ManifestName    = "trickplay.json"
	PreviewClipName = "preview.mp4"
	sheetPattern    = "sheet-%04d.jpg"
)

// TrickplayManifest is the JSON index clients consume: interval,
// tile dimensions, grid, and sheet names — not WebVTT storyboards.
type TrickplayManifest struct {
	Version         int      `json:"version"`
	IntervalSeconds int      `json:"intervalSeconds"`
	TileWidth       int      `json:"tileWidth"`
	TileHeight      int      `json:"tileHeight"`
	Columns         int      `json:"columns"`
	Rows            int      `json:"rows"`
	FrameCount      int      `json:"frameCount"`
	Sheets          []string `json:"sheets"`
}

// Generator is genassets' slice of the FFmpeg seam (interface at the
// consumer; the real *ffmpeg.Runner implements it).
type Generator interface {
	SpriteSheets(ctx context.Context, src string, intervalSeconds, tileWidth, columns, rows int, dstPattern string) error
	SampleClip(ctx context.Context, src string, offsets []float64, segmentSeconds float64, width, maxFPS int, dst string) error
	FrameGrab(ctx context.Context, src string, atSeconds float64, width int, dst string) error
	// WaveformAmplitudes returns one non-negative amplitude per analysis window
	// across src's full duration. The window count is the FFmpeg
	// analysis resolution, not the served point count — genassets buckets and
	// normalises it. Values need not be pre-normalised; normalizeWaveform owns
	// the [0,1] scaling.
	WaveformAmplitudes(ctx context.Context, src string) ([]float64, error)
	// SilenceSpans returns src's quiet regions as stream-relative [start,end]
	// second pairs (ADR-0052 §2.4(a)). The pair shape (not a shared struct) keeps
	// the ffmpeg boundary out of genassets' import set; genassets maps the pairs
	// into the wire SilenceSpan. The detection threshold/duration are owned by the
	// runner; genassets persists the returned spans verbatim.
	SilenceSpans(ctx context.Context, src string) ([][2]float64, error)
	// LoudnessMeasure returns src's EBU R128 loudness as a flat tuple
	// (integratedLUFS, loudnessRangeLU, truePeakDBTP, fromTags) (ADR-0098 area 3):
	// recovered from trusted ReplayGain/R128 container tags when present (fromTags
	// true, no decode), else computed by an ebur128 analysis pass. The primitive
	// return keeps the ffmpeg boundary out of genassets' import set — the loudness
	// analogue of SilenceSpans' primitive-pair seam. genassets owns the wire
	// LoudnessAnalysis struct and the normalization-gain math.
	LoudnessMeasure(ctx context.Context, src string) (integratedLUFS, loudnessRangeLU, truePeakDBTP float64, fromTags bool, err error)
}

// Service generates and locates Generated Asset files under dataDir/generated
// — a parallel storage area to artwork, never mixed into it.
type Service struct {
	store      *store.Store
	gen        Generator
	dir        string
	sealed     SealedPredicate
	encrypted  EncryptedPredicate
	trickplay  sync.Mutex
	publish    sync.Mutex
	publishErr error
}

// SealedPredicate reports whether a Library is currently Sealed (ADR-0098 area
// 2). Generated Asset generation skips Media in Sealed Libraries entirely.
// Composition wires it off libencrypt.Manager.Sealed; nil (the default) treats no
// Library as sealed.
type SealedPredicate func(libraryID store.LibraryID) bool

// EncryptedPredicate reports whether a Library is Encrypted (provisioned),
// whether it is currently Sealed OR Unsealed. A Generated Asset
// is a plaintext derivative of the Media, and the encrypted-plane write path is
// not built yet, so generation fails closed for every Encrypted
// Library rather than dropping a plaintext derivative on the shared unencrypted
// plane while the Library happens to be Unsealed. Composition wires it off
// libencrypt.Manager.Provisioned; nil (the default) treats no Library as
// encrypted.
type EncryptedPredicate func(libraryID store.LibraryID) bool

// NewService creates a Generated Assets service rooted at dataDir/generated.
func NewService(st *store.Store, gen Generator, dataDir string) *Service {
	service := &Service{store: st, gen: gen, dir: filepath.Join(dataDir, "generated")}
	_, service.publishErr = service.SweepPublicationResidue()
	return service
}

// SetSealed wires the Sealed Library predicate (ADR-0098 area 2). Nil disables it.
func (s *Service) SetSealed(sealed SealedPredicate) {
	s.sealed = sealed
}

// SetEncrypted wires the Encrypted Library predicate. Nil disables it.
func (s *Service) SetEncrypted(encrypted EncryptedPredicate) {
	s.encrypted = encrypted
}

// classGate reports whether a Media is the right class for an asset and has a
// usable runtime. videoSource and audioSource differ only by this gate.
type classGate func(store.Media) bool

// selectSource is the shared source picker for every Generated Asset (ADR-0098
// area 2). It loads the Media, applies the asset's class gate, SKIPS Sealed
// Libraries, and then deterministically picks an eligible Version primary.
// Later Parts are never substitutes: deriving a preview or waveform from the
// middle of a multipart timeline would silently change the Media's semantics.
// ok=false means "nothing to generate", which is not an error.
func (s *Service) selectSource(ctx context.Context, id store.MediaID, gate classGate) (store.Media, store.MediaFile, bool, error) {
	media, err := s.store.Media(ctx, id, serverFilter)
	if err != nil {
		return store.Media{}, store.MediaFile{}, false, fmt.Errorf("loading media %s: %w", id, err)
	}
	if !gate(media) {
		return store.Media{}, store.MediaFile{}, false, nil
	}
	// Sealed Libraries are skipped during sweep/generation entirely (the whole
	// Media, every Version/Part) — a Sealed Library gets no generated asset.
	if s.sealed != nil && s.sealed(media.LibraryID) {
		return store.Media{}, store.MediaFile{}, false, nil
	}
	// Encrypted Libraries fail closed the same way even while Unsealed (ADR-0045
	// §2.3): a Generated Asset is a plaintext derivative and there is no encrypted
	// -plane write path yet, so refuse rather than leak plaintext to the shared plane.
	if s.encrypted != nil && s.encrypted(media.LibraryID) {
		return store.Media{}, store.MediaFile{}, false, nil
	}
	files, err := s.store.MediaFilesForMedia(ctx, id, serverFilter)
	if err != nil {
		return store.Media{}, store.MediaFile{}, false, nil
	}
	for _, file := range rankedGeneratedSources(files) {
		info, err := os.Lstat(file.Path)
		if err == nil && info.Mode().IsRegular() {
			return media, file, true, nil
		}
	}
	return store.Media{}, store.MediaFile{}, false, nil
}

// rankedGeneratedSources is the one eligibility and precedence policy for
// Generated Asset inputs. A candidate must be an addressable, fingerprinted
// first Part. The default Version wins, then labels/fingerprints/paths provide a
// total order independent of database or caller iteration order.
func rankedGeneratedSources(files []store.MediaFile) []store.MediaFile {
	candidates := make([]store.MediaFile, 0, len(files))
	for _, file := range files {
		if file.PartOrdinal != 0 || file.Path == "" || file.ContentFingerprint == "" {
			continue
		}
		candidates = append(candidates, file)
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if (a.VersionLabel == "") != (b.VersionLabel == "") {
			return a.VersionLabel == ""
		}
		if a.VersionLabel != b.VersionLabel {
			return a.VersionLabel < b.VersionLabel
		}
		if a.ContentFingerprint != b.ContentFingerprint {
			return a.ContentFingerprint < b.ContentFingerprint
		}
		return a.Path < b.Path
	})
	return candidates
}

// assetDir is the on-disk directory for one content fingerprint. The
// fingerprint ("sha256:<hex>") is sanitized into a single path element, so the
// layout stays inside the generated dir by construction. An unchanged
// fingerprint resolves to the same directory — the regeneration key.
func (s *Service) assetDir(fingerprint string) string {
	return filepath.Join(s.dir, assetFingerprintDir(fingerprint))
}

// PurgeFingerprint removes a content fingerprint's entire generated-asset
// directory — trickplay manifest + sprite sheets, preview clip, marker thumbs,
// waveform/loudness/silence/beatgrid/sync-lanes analyses, and image-enrich JSON —
// through the same assetDir keying every generator writes to, so the purge cannot
// drift from the layout. It is the reconcile hook for the Encrypted-Library
// crash-recovery sweep (ADR-0045 §reconcile, ENCPRIV-10d): generation is
// fail-closed for an Encrypted Library, so any directory found for
// its media's fingerprints is a crash-stranded plaintext leak. A missing
// directory is fine (the common case). Hard delete is correct: leaked plaintext
// derivatives of an encrypted item are the invariant-#5 at-rest violation, and
// they are losslessly regenerable from the sealed original, so invariant #6
// recoverability does not attach to them.
func (s *Service) PurgeFingerprint(fingerprint string) error {
	if fingerprint == "" {
		return nil
	}
	s.publish.Lock()
	defer s.publish.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := ensureRootDir(root, assetGenerationDir); err != nil {
		return err
	}
	if err := root.RemoveAll(assetFingerprintDir(fingerprint)); err != nil {
		return err
	}
	_, err = collectUnreferencedGenerations(root)
	return err
}

// ManifestPath returns where a fingerprint's trickplay manifest lives
// (whether or not it exists yet).
func (s *Service) ManifestPath(fingerprint string) string {
	return filepath.Join(s.assetDir(fingerprint), ManifestName)
}

// PreviewClipPath returns where a fingerprint's preview clip lives.
func (s *Service) PreviewClipPath(fingerprint string) string {
	return filepath.Join(s.assetDir(fingerprint), PreviewClipName)
}

// MarkerThumbName derives the thumbnail file name for a Marker timestamp.
// The name carries the timestamp (milliseconds), so the asset key is
// fingerprint+timestamp: editing a Marker's time targets a new file and the
// sweep regenerates it.
func MarkerThumbName(startSeconds float64) string {
	return fmt.Sprintf("marker-%d.jpg", int64(math.Round(startSeconds*1000)))
}

// MarkerThumbPath returns where the thumbnail for a Marker timestamp lives.
func (s *Service) MarkerThumbPath(fingerprint string, startSeconds float64) string {
	return filepath.Join(s.assetDir(fingerprint), MarkerThumbName(startSeconds))
}

// TrickplayAsset reports the trickplay manifest name when sprites exist for
// the fingerprint (playback's availability seam).
func (s *Service) TrickplayAsset(fingerprint string) (string, bool) {
	if fingerprint == "" || !regularFileExists(s.ManifestPath(fingerprint)) {
		return "", false
	}
	return ManifestName, true
}

// PreviewClipAsset reports the preview clip name when one exists for the
// fingerprint.
func (s *Service) PreviewClipAsset(fingerprint string) (string, bool) {
	if fingerprint == "" || !regularFileExists(s.PreviewClipPath(fingerprint)) {
		return "", false
	}
	return PreviewClipName, true
}

var (
	sheetNameRe  = regexp.MustCompile(`^sheet-[0-9]{4}\.jpg$`)
	markerNameRe = regexp.MustCompile(`^marker-[0-9]+\.jpg$`)
)

// validAssetName is the closed name vocabulary the serving route accepts —
// anything else (including traversal attempts) is an unknown asset.
func validAssetName(name string) bool {
	return name == ManifestName || name == PreviewClipName || name == WaveformName ||
		name == SilenceMapName || name == BeatGridName || name == SyncLanesName || name == ImageEnrichName ||
		name == LoudnessName ||
		sheetNameRe.MatchString(name) || markerNameRe.MatchString(name)
}

// ErrUnknownAsset means the requested asset name is outside the vocabulary or
// the file does not exist — indistinguishable on purpose.
var ErrUnknownAsset = errors.New("unknown asset")

// AssetPath resolves a requested asset name within a fingerprint's directory.
func (s *Service) AssetPath(fingerprint, name string) (string, error) {
	if fingerprint == "" || !validAssetName(name) {
		return "", ErrUnknownAsset
	}
	path := filepath.Join(s.assetDir(fingerprint), name)
	if !regularFileExists(path) {
		return "", ErrUnknownAsset
	}
	return path, nil
}

// RewrapEncryptedLibraryAssets rewrites every currently durable Generated Asset
// file for the given encrypted-Library media files. Assets are keyed by content
// fingerprint, so the media-file inventory is the artifact enumerator. The
// rewrap callback receives each asset's persisted identity (content fingerprint
// + asset name — never the path) so the caller can bind the rewritten object to
// its immutable ObjectContext.
func (s *Service) RewrapEncryptedLibraryAssets(ctx context.Context, files []store.MediaFile, rewrap func(path, contentFingerprint, assetName string) error) error {
	if rewrap == nil {
		return errors.New("generated-asset rewrap is required")
	}
	seen := map[string]bool{}
	for _, file := range files {
		fp := file.ContentFingerprint
		if fp == "" || seen[fp] {
			continue
		}
		seen[fp] = true
		dir := s.assetDir(fp)
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if entry.IsDir() || !validAssetName(entry.Name()) {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			if err := rewrap(filepath.Join(dir, entry.Name()), fp, entry.Name()); err != nil {
				return err
			}
		}
	}
	return nil
}

// trickplayPlan derives the sampling plan for a runtime: the default interval
// widens on ultra-long files so the total frame count never exceeds the cap.
func trickplayPlan(durationSeconds int) (intervalSeconds, frameCount, sheetCount int) {
	interval := defaultIntervalSeconds
	if durationSeconds/interval > frameCap {
		interval = (durationSeconds + frameCap - 1) / frameCap
	}
	frames := durationSeconds / interval
	if frames < 1 {
		frames = 1
	}
	perSheet := SheetColumns * SheetRows
	return interval, frames, (frames + perSheet - 1) / perSheet
}

// previewOffsets derives the preview clip's segment start offsets and segment
// length: evenly spaced inside the middle 90% of the runtime;
// short media collapses to one contiguous clip from the start.
func previewOffsets(durationSeconds int) (offsets []float64, segmentSeconds float64) {
	duration := float64(durationSeconds)
	total := previewSegments * previewSegmentSeconds
	if duration < previewShortSeconds {
		if duration < total {
			total = duration
		}
		return []float64{0}, total
	}
	lo := duration * previewEdgeSkip
	hi := duration*(1-previewEdgeSkip) - previewSegmentSeconds
	step := (hi - lo) / float64(previewSegments-1)
	offsets = make([]float64, previewSegments)
	for i := range offsets {
		offsets[i] = lo + float64(i)*step
	}
	return offsets, previewSegmentSeconds
}

// serverFilter is the server-internal read scope: generation sees every
// Library; reveal filtering happens at the serving boundary (like artwork).
var serverFilter = store.LibraryFilter{Revealed: true, AllLibraries: true}

// videoSource loads the Media row and a source file when the item is
// video-class with a known runtime and a content fingerprint, skipping Sealed
// Libraries and falling back to a non-primary unsealed source (ADR-0098 area 2).
// ok=false means "nothing to generate", which is not an error.
func (s *Service) videoSource(ctx context.Context, id store.MediaID) (store.Media, store.MediaFile, bool, error) {
	return s.selectSource(ctx, id, func(m store.Media) bool {
		return m.Class == store.MediaClassVideo && m.DurationSeconds > 0
	})
}

// GenerateTrickplay produces the sprite sheets and manifest for one Media
// item's primary file. An existing manifest for the same fingerprint means
// nothing to do (content-fingerprint-keyed regeneration). The manifest is
// written last, atomically — it is the publication marker.
func (s *Service) GenerateTrickplay(ctx context.Context, id store.MediaID) error {
	if s.gen == nil {
		return errors.New("no generator configured")
	}
	media, file, ok, err := s.videoSource(ctx, id)
	if err != nil || !ok {
		return err
	}
	s.trickplay.Lock()
	defer s.trickplay.Unlock()
	manifestPath := s.ManifestPath(file.ContentFingerprint)
	if regularFileExists(manifestPath) {
		return nil
	}
	interval, frames, _ := trickplayPlan(media.DurationSeconds)
	contract := fmt.Sprintf("trickplay:v1:interval=%d:tile=%d:grid=%dx%d", interval, TileWidth, SheetColumns, SheetRows)
	publication, err := s.beginPublication(string(id), file.ContentFingerprint, contract, ManifestName)
	if err != nil {
		return err
	}
	defer publication.close()
	stagePath := filepath.Join(publication.root.Name(), publication.stageDir)
	if err := s.gen.SpriteSheets(ctx, file.Path, interval, TileWidth, SheetColumns, SheetRows, filepath.Join(stagePath, sheetPattern)); err != nil {
		return err
	}
	sheets, err := filepath.Glob(filepath.Join(stagePath, "sheet-*.jpg"))
	if err != nil {
		return err
	}
	if len(sheets) == 0 {
		return fmt.Errorf("sprite generation for %s produced no sheets", id)
	}
	sort.Strings(sheets)
	names := make([]string, len(sheets))
	for i, sheet := range sheets {
		names[i] = filepath.Base(sheet)
	}
	tileHeight, err := tileHeightFromSheet(sheets[0])
	if err != nil {
		return fmt.Errorf("reading sheet dimensions for %s: %w", id, err)
	}
	manifest := TrickplayManifest{
		Version:         1,
		IntervalSeconds: interval,
		TileWidth:       TileWidth,
		TileHeight:      tileHeight,
		Columns:         SheetColumns,
		Rows:            SheetRows,
		FrameCount:      frames,
		Sheets:          names,
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	stageManifest := filepath.Join(publication.stageDir, ManifestName)
	if err := publication.root.WriteFile(stageManifest, data, 0o600); err != nil {
		return err
	}
	for _, name := range names {
		publicName := filepath.Join(assetFingerprintDir(file.ContentFingerprint), name)
		if err := publication.commit(name, publicName); err != nil {
			return err
		}
	}
	if err := publication.commit(ManifestName, publication.publicName); err != nil {
		return err
	}
	return s.removeOrphanTrickplaySheets(file.ContentFingerprint, names)
}

func (s *Service) removeOrphanTrickplaySheets(fingerprint string, expected []string) error {
	keep := make(map[string]bool, len(expected))
	for _, name := range expected {
		keep[name] = true
	}
	return s.removeOrphanAssets(fingerprint, keep, sheetNameRe.MatchString)
}

// tileHeightFromSheet derives the per-tile height from a sheet's JPEG header
// (sheet height ÷ grid rows) — the scale filter preserves aspect, so the
// height is only known after generation.
func tileHeightFromSheet(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	cfg, err := jpeg.DecodeConfig(f)
	if err != nil {
		return 0, err
	}
	return cfg.Height / SheetRows, nil
}

// GeneratePreviewClip produces the silent sampled preview clip for one Media
// item, honoring the per-Library toggle (default ON; audio-class Media never
// gets clips). An existing clip for the same fingerprint means nothing to do.
func (s *Service) GeneratePreviewClip(ctx context.Context, id store.MediaID) error {
	if s.gen == nil {
		return errors.New("no generator configured")
	}
	media, file, ok, err := s.videoSource(ctx, id)
	if err != nil || !ok {
		return err
	}
	enabled, err := s.store.LibraryPreviewClips(ctx, media.LibraryID)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	dst := s.PreviewClipPath(file.ContentFingerprint)
	if regularFileExists(dst) {
		return nil
	}
	offsets, segmentSeconds := previewOffsets(media.DurationSeconds)
	contract := fmt.Sprintf("preview:v1:width=%d:fps=%d:segments=%d", previewWidth, previewMaxFPS, previewSegments)
	return s.publishGeneratedFile(string(id), file.ContentFingerprint, contract, PreviewClipName, func(path string) error {
		return s.gen.SampleClip(ctx, file.Path, offsets, segmentSeconds, previewWidth, previewMaxFPS, path)
	})
}

// GenerateMarkerThumbs grabs the missing thumbnail frames for one Media
// item's Markers — always on, no toggle. A range Marker's grab
// is at its start time; the fingerprint+timestamp file key means a Marker
// whose time changed gets a fresh grab on the next pass.
func (s *Service) GenerateMarkerThumbs(ctx context.Context, id store.MediaID) error {
	if s.gen == nil {
		return errors.New("no generator configured")
	}
	_, file, ok, err := s.videoSource(ctx, id)
	if err != nil || !ok {
		return err
	}
	markers, err := s.store.MarkersForMedia(ctx, id, serverFilter)
	if err != nil {
		return err
	}
	var errs []error
	expected := map[string]bool{}
	for _, marker := range markers {
		if ctx.Err() != nil {
			break
		}
		name := MarkerThumbName(marker.StartSeconds)
		expected[name] = true
		dst := filepath.Join(s.assetDir(file.ContentFingerprint), name)
		if regularFileExists(dst) {
			continue
		}
		contract := fmt.Sprintf("marker-thumb:v1:width=%d:millis=%d", markerThumbWidth, int64(math.Round(marker.StartSeconds*1000)))
		if err := s.publishGeneratedFile(string(id), file.ContentFingerprint, contract, name, func(path string) error {
			return s.gen.FrameGrab(ctx, file.Path, marker.StartSeconds, markerThumbWidth, path)
		}); err != nil {
			errs = append(errs, fmt.Errorf("marker %s: %w", marker.ID, err))
		}
	}
	if err := s.removeOrphanMarkerThumbs(file.ContentFingerprint, expected); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (s *Service) removeOrphanMarkerThumbs(fingerprint string, expected map[string]bool) error {
	return s.removeOrphanAssets(fingerprint, expected, markerNameRe.MatchString)
}

func regularFileExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}
