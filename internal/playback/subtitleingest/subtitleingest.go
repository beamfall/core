// Package subtitleingest is the hardening boundary subtitle bytes pass before
// any parse or serve. It fails CLOSED: malformed, hostile, or
// oversize input is rejected here so downstream parsers (the in-process
// SRT→WebVTT transform, ffmpeg's webvtt muxer, libass on the client rungs) only
// ever see validated bytes. This is the atomicity boundary the rest of the
// subtitle stack assumes: happy-path code below it never re-checks size, format,
// decompression, or tag scope.
//
// WHAT ACTUALLY ROUTES THROUGH IT TODAY (the honest inventory — ADR-0194 §7
// wants one boundary shared by every ingest path, and the tree is not there
// yet): mediahttp's three subtitle byte routes — the sidecar text route, the
// raw-ASS passthrough route, and the bitmap sidecar route — plus the cached
// muxed WebVTT written by the ffmpeg extraction route. Provider fetch and
// operator upload have no ingest path in-tree at all, so the archive guards
// below (ValidateZipEntryName, CheckDecompressionRatio, InspectArchive) are
// tested but DORMANT: they are ready for a wirer, not currently on any live
// path. Scan classifies sidecars by extension against the same vocabulary but
// never reads their bytes, so it does not Guard.
//
// This package is stdlib-only and imports nothing from the rest of the tree, so
// scan, mediahttp, scrape, and ffmpeg can all depend on it without a cycle.
package subtitleingest

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// Format is the closed, hardened subtitle-ingest vocabulary. It
// is deliberately narrower-by-shape than "any subtitle file": a format admitted
// here has a recognisable signature or a stable structure the downstream ladder
// can serve. MicroDVD and bare .txt are rejected by omission — framerate-
// relative timing with no format signature is unsafe to auto-attach.
type Format string

const (
	FormatSRT  Format = "srt"
	FormatVTT  Format = "vtt"
	FormatASS  Format = "ass"
	FormatSSA  Format = "ssa"
	FormatSUP  Format = "sup"  // PGS bitmap
	FormatIDX  Format = "idx"  // VobSub index (paired with .sub)
	FormatSUB  Format = "sub"  // VobSub bitmap payload (paired with .idx)
	FormatSMI  Format = "smi"  // SAMI
	FormatTTML Format = "ttml" // Timed Text Markup
)

// textFormats are the character-based formats the size cap and (where
// applicable) markup/tag sanitizers apply to. Bitmap formats (sup/sub) carry
// binary payloads and take the larger MaxBitmapBytes cap instead.
var textFormats = map[Format]bool{
	FormatSRT:  true,
	FormatVTT:  true,
	FormatASS:  true,
	FormatSSA:  true,
	FormatSMI:  true,
	FormatTTML: true,
}

// classifyExt maps a lowercase, dot-less extension to its hardened Format. Only
// these extensions are admitted; anything else — including MicroDVD's .sub-like
// .txt and unknown containers — is rejected by the caller.
var classifyExt = map[string]Format{
	"srt":  FormatSRT,
	"vtt":  FormatVTT,
	"ass":  FormatASS,
	"ssa":  FormatSSA,
	"sup":  FormatSUP,
	"idx":  FormatIDX,
	"sub":  FormatSUB,
	"smi":  FormatSMI,
	"ttml": FormatTTML,
}

// Byte ceilings. Subtitles are tiny relative to media; a text sidecar past a few
// hundred KiB is already pathological, and the caps leave generous headroom for
// a long-running SDH file while still bounding a parser-DoS blob. Bitmap sidecars
// (PGS .sup, VobSub .sub) carry image data and take a larger ceiling.
const (
	// MaxTextBytes caps a character-based subtitle file (srt/vtt/ass/ssa/smi/ttml).
	MaxTextBytes = 8 << 20 // 8 MiB
	// MaxBitmapBytes caps a bitmap subtitle payload (.sup PGS / .sub VobSub).
	MaxBitmapBytes = 64 << 20 // 64 MiB
	// MaxDecompressionRatio bounds a single compressed archive entry's expansion
	// (uncompressed÷compressed). A zip-bomb inflates far past this; a real
	// subtitle archive expands only modestly.
	MaxDecompressionRatio = 200
	// MaxArchiveEntryBytes caps the uncompressed size of any single archive entry
	// extracted from a compressed provider download, independent of ratio.
	MaxArchiveEntryBytes = MaxBitmapBytes
	// MaxArchiveEntries bounds central-directory work before extraction.
	MaxArchiveEntries = 128
	// MaxArchiveTotalBytes bounds the aggregate declared uncompressed size of
	// non-directory members, including members outside the subtitle whitelist.
	MaxArchiveTotalBytes = 256 << 20 // 256 MiB
)

// Errors returned by the boundary. Callers compare with errors.Is to route a
// rejection (log + skip/serve-404) versus a genuine I/O fault.
var (
	ErrFormatNotAllowed   = errors.New("subtitleingest: format not in the hardened whitelist")
	ErrTooLarge           = errors.New("subtitleingest: subtitle exceeds the size cap")
	ErrZipSlip            = errors.New("subtitleingest: archive entry name escapes the extraction root")
	ErrZipBomb            = errors.New("subtitleingest: archive entry decompression ratio exceeds the cap")
	ErrUnsafeArchiveEntry = errors.New("subtitleingest: archive entry type is unsafe")
	ErrArchiveLimit       = errors.New("subtitleingest: archive exceeds an aggregate limit")
	ErrCraftedASS         = errors.New("subtitleingest: ASS input exceeds the override-tag scope limits")
)

// Classify resolves a lowercase, dot-less extension to its hardened Format,
// reporting false for anything outside the whitelist. The caller strips the dot
// and lowercases before calling; an ext carrying a leading dot or upper case is
// normalised defensively so a mis-cased caller still fails closed correctly.
func Classify(ext string) (Format, bool) {
	normalized := strings.ToLower(strings.TrimPrefix(ext, "."))
	f, ok := classifyExt[normalized]
	return f, ok
}

// IsText reports whether a Format is character-based (as opposed to a bitmap
// payload). Callers that only ever handle text — the WebVTT serve path — use it
// to reject a bitmap format before reading it as a string.
func (f Format) IsText() bool { return textFormats[f] }

// capFor returns the byte ceiling for a Format: the text cap for character
// formats, the bitmap cap for sup/sub.
func capFor(f Format) int64 {
	if textFormats[f] {
		return MaxTextBytes
	}
	return MaxBitmapBytes
}

// CheckSize is the PRE-READ half of the boundary: it validates a subtitle's
// length from its directory entry, before a single byte is allocated. Guard can
// only be called on bytes that are already resident, so a caller that reads
// first and Guards after has already paid the allocation the cap exists to
// prevent — an 8 MiB cap consulted after an os.ReadFile of a 4 GiB file is a
// value check, not a memory bound. Every byte route stats first and calls this,
// then reads within the ceiling, then Guards the bytes. An unknown format fails
// closed here too, so an unservable format never reaches the filesystem read.
func CheckSize(ext string, size int64) error {
	format, ok := Classify(ext)
	if !ok {
		return fmt.Errorf("%w: %q", ErrFormatNotAllowed, ext)
	}
	if size > capFor(format) {
		return fmt.Errorf("%w: %d bytes for %s", ErrTooLarge, size, format)
	}
	return nil
}

// Guard is the content half of the boundary: it validates one fetched, scanned,
// or uploaded subtitle blob before any parse. It fails closed — an unknown
// format, an oversize file, or (for ass/ssa) a crafted-ASS payload is rejected.
// The size check is CheckSize's, re-applied to the bytes actually in hand, so a
// file that grew between the stat and the read is still refused. The bytes are
// never mutated here; sanitisation of servable output is a separate, serve-time
// step (SanitizeWebVTT), because the on-disk file is preserved for
// byte-identical passthrough to libass engines.
func Guard(ext string, data []byte) error {
	if err := CheckSize(ext, int64(len(data))); err != nil {
		return err
	}
	if format, _ := Classify(ext); format == FormatASS || format == FormatSSA {
		return CheckASS(data)
	}
	return nil
}

// ValidateZipEntryName rejects a zip-slip path-traversal entry name before any
// write. A safe entry is a forward-slashed relative path that stays under the
// extraction root: no absolute path, no volume prefix, no ".." segment, and no
// backslash (which some extractors treat as a separator on Windows). The name is
// validated as-declared, never after a filepath.Join that would already have
// escaped.
func ValidateZipEntryName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty entry name", ErrZipSlip)
	}
	if strings.ContainsRune(name, '\\') {
		return fmt.Errorf("%w: %q contains a backslash separator", ErrZipSlip, name)
	}
	if strings.HasPrefix(name, "/") || (len(name) >= 2 && name[1] == ':') {
		return fmt.Errorf("%w: %q is an absolute path", ErrZipSlip, name)
	}
	cleaned := path.Clean(name)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("%w: %q escapes the root", ErrZipSlip, name)
	}
	return nil
}

// CheckDecompressionRatio rejects a zip-bomb entry: an uncompressed size that
// expands past MaxDecompressionRatio× its compressed size, or past
// MaxArchiveEntryBytes outright. A zero compressed size with any payload is
// treated as a bomb (an unbounded ratio), never a divide-by-zero.
func CheckDecompressionRatio(compressed, uncompressed uint64) error {
	if uncompressed > MaxArchiveEntryBytes {
		return fmt.Errorf("%w: %d uncompressed bytes", ErrZipBomb, uncompressed)
	}
	if uncompressed == 0 {
		return nil
	}
	if compressed == 0 {
		return fmt.Errorf("%w: %d bytes from a zero-length compressed entry", ErrZipBomb, uncompressed)
	}
	ratio := uncompressed / compressed
	if ratio > MaxDecompressionRatio || (ratio == MaxDecompressionRatio && uncompressed%compressed != 0) {
		return fmt.Errorf("%w: %dx expansion", ErrZipBomb, uncompressed/compressed)
	}
	return nil
}

// InspectArchive screens every entry of a compressed provider download from the
// central directory alone: each entry name must clear the zip-slip guard and
// each entry's DECLARED sizes must clear the decompression-ratio guard.
// Directory entries and entries whose extension is outside the whitelist are
// skipped (not extracted), leaving only admitted subtitle members.
//
// THE CALLER MUST WRAP EVERY ENTRY READ IN AN io.LimitReader; use
// ReadArchiveEntry rather than opening an entry directly. This function is not,
// on its own, a bomb defence: the central-directory sizes it reads are written
// by whoever built the archive, so a bomb can simply declare an honest-looking
// UncompressedSize64 and still inflate to gigabytes. archive/zip bounds
// f.Open()'s reader by the COMPRESSED size only, so an unbounded io.ReadAll over
// that reader inflates the full payload no matter what this check returned.
// InspectArchive rejects the careless and the truthful bomb cheaply, before any
// I/O; ReadArchiveEntry's LimitReader is what actually holds the line.
func InspectArchive(zr *zip.Reader) error {
	if zr == nil {
		return fmt.Errorf("%w: nil archive reader", ErrUnsafeArchiveEntry)
	}
	if len(zr.File) > MaxArchiveEntries {
		return fmt.Errorf("%w: %d entries", ErrArchiveLimit, len(zr.File))
	}
	var declaredTotal uint64
	for _, entry := range zr.File {
		if err := validateArchiveEntryShape(entry); err != nil {
			return err
		}
		if archiveEntryIsDirectory(entry) {
			continue // directory entry: never written as a file
		}
		if entry.UncompressedSize64 > uint64(MaxArchiveTotalBytes)-declaredTotal {
			return fmt.Errorf("%w: declared size exceeds %d bytes", ErrArchiveLimit, MaxArchiveTotalBytes)
		}
		declaredTotal += entry.UncompressedSize64
		name := entry.Name
		if _, ok := Classify(strings.TrimPrefix(path.Ext(name), ".")); !ok {
			continue // non-subtitle member: not extracted, so not a threat
		}
		if err := CheckDecompressionRatio(entry.CompressedSize64, entry.UncompressedSize64); err != nil {
			return err
		}
	}
	return nil
}

// ReadArchiveEntry opens and reads one admitted subtitle member through an
// actual-output bound. InspectArchive must still run first so its archive-wide
// entry-count and aggregate-declared-size limits apply. This function repeats
// the per-entry name, type, declared-ratio, and content guards so callers cannot
// accidentally separate safe extraction from the checks that make it safe.
func ReadArchiveEntry(entry *zip.File) ([]byte, error) {
	if err := validateArchiveEntryShape(entry); err != nil {
		return nil, err
	}
	if archiveEntryIsDirectory(entry) {
		return nil, fmt.Errorf("%w: %q is a directory", ErrUnsafeArchiveEntry, entry.Name)
	}
	ext := strings.TrimPrefix(path.Ext(entry.Name), ".")
	format, ok := Classify(ext)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrFormatNotAllowed, ext)
	}
	if err := CheckDecompressionRatio(entry.CompressedSize64, entry.UncompressedSize64); err != nil {
		return nil, err
	}
	data, err := readArchiveEntry(entry, capFor(format))
	if err != nil {
		return nil, err
	}
	if err := Guard(ext, data); err != nil {
		return nil, err
	}
	return data, nil
}

func readArchiveEntry(entry *zip.File, limit int64) ([]byte, error) {
	rc, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("subtitleingest: open archive entry: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(rc, limit+1))
	closeErr := rc.Close()
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: inflated output exceeds %d bytes", ErrZipBomb, limit)
	}
	if readErr != nil {
		if errors.Is(readErr, zip.ErrFormat) {
			return nil, fmt.Errorf("%w: inflated output contradicts the declared size", ErrZipBomb)
		}
		return nil, fmt.Errorf("subtitleingest: read archive entry: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("subtitleingest: close archive entry: %w", closeErr)
	}
	return data, nil
}

func validateArchiveEntryShape(entry *zip.File) error {
	if entry == nil {
		return fmt.Errorf("%w: nil entry", ErrUnsafeArchiveEntry)
	}
	if err := ValidateZipEntryName(entry.Name); err != nil {
		return err
	}
	mode := entry.Mode()
	if mode&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %q is a symlink", ErrUnsafeArchiveEntry, entry.Name)
	}
	if mode&os.ModeType == os.ModeDir {
		return nil
	}
	if mode&os.ModeType != 0 {
		return fmt.Errorf("%w: %q is not a regular file", ErrUnsafeArchiveEntry, entry.Name)
	}
	return nil
}

func archiveEntryIsDirectory(entry *zip.File) bool {
	return entry.Mode().IsDir() || strings.HasSuffix(entry.Name, "/")
}
