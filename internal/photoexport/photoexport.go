// Package photoexport is the bulk photo-export substrate (ADR-0045 §2.9 mode 2,
// extending the My Data export of ADR-0036). It runs a job-substrate job that
// emits every byte-exact original backing a Custodial Library, plus a portable,
// Takeout-style sidecar manifest (album membership, EXIF, Location, captions),
// so a Profile's own photo data exports to another service.
//
// The export is free (§2.7) and read-only: it never mutates the Library. It
// carries NO domain vocabulary (invariant 1) — "album membership" is a generic
// (relation, kind, title) tuple, EXIF/Location/captions are generic Media
// fields, and the manifest reproduces them verbatim without core naming a
// plugin's concepts.
//
// A Sealed Encrypted Library is absent until Unsealed: its plane is unreadable,
// so the store read surfaces store.ErrEncryptedLibraryDBDisabled and the export
// reports the Library as skipped rather than emitting partial data. The
// export-time step-up authorization gate is PANEL-18, not this package; the job
// payload carries the already-authorized request.
package photoexport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/beamfall/core/internal/store"
)

// JobKind is the ADR-0013 job-substrate kind for a bulk photo export.
const JobKind store.JobKind = "photo-export"

// ManifestVersion is the schema version stamped into every export manifest so a
// third-party reader can branch on shape. Bump on any breaking manifest change.
const ManifestVersion = 1

// manifestName is the Takeout-style root sidecar a third party reads first.
const manifestName = "export-manifest.json"

// originalsDir is the subtree under the export root that holds the byte-exact
// original files, one directory per Media item.
const originalsDir = "originals"

// ByteDecryptor recovers the byte-exact plaintext original for one file in an
// Encrypted Library from its at-rest ciphertext path. It is the same seam the
// media byte route uses (mediahttp.SetEncryptedBytes); composition wires it off
// libencrypt.Manager. A plaintext Library never calls it (the bytes are copied
// as-is), so a deployment without library encryption may leave it nil.
type ByteDecryptor interface {
	DecryptBytes(libraryID store.LibraryID, ciphertextPath, objectClass, immutableObjectID string) ([]byte, error)
}

// encryptedClassMediaBytes mirrors libencrypt's closed media-bytes ObjectClass
// as a plain string so this package stays decoupled from the crypto layer; the
// adapter validates it fail-closed.
const encryptedClassMediaBytes = "media-bytes"

// Store is the read surface the export needs: the per-Library Media walk and the
// per-Media export payload, both plane-aware.
type Store interface {
	MediaIDsForLibrary(ctx context.Context, libraryID store.LibraryID) ([]store.MediaID, error)
	ExportItem(ctx context.Context, mediaID store.MediaID) (store.ExportItem, error)
}

// JobPayload is the durable payload for JobKind. It names the Library to export
// and the destination directory the bytes + manifest are written to. The
// caller's export authority (PANEL-18 step-up) is assumed already granted before
// the job is enqueued; the job is a pure data-plane producer.
type JobPayload struct {
	LibraryID store.LibraryID `json:"libraryID"`
	Dest      string          `json:"dest"`
}

// Marshal encodes a JobPayload for store.EnqueueJob.
func (p JobPayload) Marshal() ([]byte, error) { return json.Marshal(p) }

// Service runs photo-export jobs over a Store, decrypting Encrypted-Library
// originals through the ByteDecryptor.
type Service struct {
	store     Store
	decryptor ByteDecryptor
	now       func() time.Time
}

// NewService builds a Service. decryptor may be nil when no Encrypted Library is
// ever exported; an Encrypted Library then fails closed rather than emitting
// ciphertext.
func NewService(st Store, decryptor ByteDecryptor) *Service {
	return &Service{store: st, decryptor: decryptor, now: time.Now}
}

// RunJob decodes a JobPayload and performs the export. Composition adapts this
// to the jobs.Handler signature (like offlinepack.HandleBuildJob), so this
// package never imports the jobs substrate.
func (s *Service) RunJob(ctx context.Context, payload []byte) error {
	var job JobPayload
	if err := json.Unmarshal(payload, &job); err != nil {
		return err
	}
	_, err := s.Export(ctx, job.LibraryID, job.Dest)
	return err
}

// Manifest is the portable, Takeout-style root sidecar: a third party reads it
// to reconstruct the export without any Beamfall knowledge.
type Manifest struct {
	Version    int             `json:"version"`
	ExportedAt time.Time       `json:"exportedAt"`
	LibraryID  store.LibraryID `json:"libraryID"`
	Encrypted  bool            `json:"encrypted"`
	Items      []ManifestItem  `json:"items"`
}

// ManifestItem is one exported Media item: its descriptive row, the byte-exact
// files written for it (with the relative path they were written to), the
// plugin-enriched fields (EXIF, Location, …), sidecar caption tracks, and the
// generic Entity memberships (album membership, …).
type ManifestItem struct {
	MediaID     store.MediaID     `json:"mediaID"`
	Title       string            `json:"title"`
	Summary     string            `json:"summary,omitempty"`
	Year        int               `json:"year,omitempty"`
	Class       string            `json:"class"`
	Files       []ManifestFile    `json:"files"`
	Fields      []ManifestField   `json:"fields,omitempty"`
	Captions    []ManifestCaption `json:"captions,omitempty"`
	Memberships []ManifestMember  `json:"memberships,omitempty"`
}

// ManifestFile names one byte-exact original written under the originals subtree
// and the content fingerprint a third party can verify the bytes against.
type ManifestFile struct {
	// RelPath is the file's location relative to the export root, using forward
	// slashes so the manifest is portable across operating systems.
	RelPath            string `json:"relPath"`
	ContentFingerprint string `json:"contentFingerprint"`
	VersionLabel       string `json:"versionLabel,omitempty"`
	PartOrdinal        int    `json:"partOrdinal"`
}

// ManifestField is one plugin-declared Media field carried verbatim (EXIF,
// Location, captions text, …). Exactly the member matching Type is set.
type ManifestField struct {
	Name   string     `json:"name"`
	Type   string     `json:"type"`
	Text   string     `json:"text,omitempty"`
	Number float64    `json:"number,omitempty"`
	Flag   bool       `json:"flag,omitempty"`
	Date   *time.Time `json:"date,omitempty"`
}

// ManifestCaption is one sidecar caption track recorded for the item's bytes.
type ManifestCaption struct {
	RelPath  string `json:"relPath"`
	Kind     string `json:"kind"`
	Format   string `json:"format"`
	Language string `json:"language,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
	SDH      bool   `json:"sdh,omitempty"`
}

// ManifestMember is one generic Entity membership: the relation name, the target
// Entity's bare Kind, and its Title. "album membership" is just one such tuple.
type ManifestMember struct {
	Relation string `json:"relation"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
}

// ErrNoDecryptor is returned when an Encrypted Library is exported but no
// ByteDecryptor was wired — the export fails closed rather than emit ciphertext.
var ErrNoDecryptor = errors.New("photo export: encrypted library requires a byte decryptor")

// Export writes every byte-exact original for libraryID plus a Takeout-style
// manifest under dest, returning the manifest it wrote. dest is created if
// absent. The walk is deterministic (stable Media order) so a re-run reproduces
// the same layout. A Sealed Encrypted Library surfaces its store error
// unchanged (absent until Unsealed).
func (s *Service) Export(ctx context.Context, libraryID store.LibraryID, dest string) (Manifest, error) {
	if libraryID == "" {
		return Manifest{}, errors.New("library ID is required")
	}
	if dest == "" {
		return Manifest{}, errors.New("destination is required")
	}
	ids, err := s.store.MediaIDsForLibrary(ctx, libraryID)
	if err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(filepath.Join(dest, originalsDir), 0o700); err != nil {
		return Manifest{}, err
	}
	manifestPath := filepath.Join(dest, manifestName)
	if err := os.Remove(manifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, err
	}

	man := Manifest{
		Version:    ManifestVersion,
		ExportedAt: s.now().UTC(),
		LibraryID:  libraryID,
		Items:      make([]ManifestItem, 0, len(ids)),
	}

	for _, id := range ids {
		item, err := s.store.ExportItem(ctx, id)
		if err != nil {
			return Manifest{}, fmt.Errorf("export item %s: %w", id, err)
		}
		man.Encrypted = man.Encrypted || item.Encrypted
		mi, err := s.writeItem(dest, item)
		if err != nil {
			return Manifest{}, fmt.Errorf("write item %s: %w", id, err)
		}
		man.Items = append(man.Items, mi)
	}

	blob, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if err := writeFileAtomic(manifestPath, blob); err != nil {
		return Manifest{}, err
	}
	return man, nil
}

// writeItem writes one item's byte-exact files under originals/<mediaID>/ and
// returns its manifest entry. Every (Version, Part) file is emitted, so a
// RAW+JPEG pair or a Live Photo (still + motion) round-trips intact.
func (s *Service) writeItem(dest string, item store.ExportItem) (ManifestItem, error) {
	itemDir := filepath.Join(dest, originalsDir, safeComponent(string(item.Media.ID)))
	if err := os.MkdirAll(itemDir, 0o700); err != nil {
		return ManifestItem{}, err
	}

	mi := ManifestItem{
		MediaID: item.Media.ID,
		Title:   item.Media.Title,
		Summary: item.Media.Summary,
		Year:    item.Media.Year,
		Class:   string(item.Media.Class),
	}

	for i, f := range item.Files {
		name := exportFileName(i, f)
		plain, err := s.fileBytes(item, f)
		if err != nil {
			return ManifestItem{}, err
		}
		if err := writeFileAtomic(filepath.Join(itemDir, name), plain); err != nil {
			return ManifestItem{}, err
		}
		mi.Files = append(mi.Files, ManifestFile{
			RelPath:            filepath.ToSlash(filepath.Join(originalsDir, safeComponent(string(item.Media.ID)), name)),
			ContentFingerprint: f.ContentFingerprint,
			VersionLabel:       f.VersionLabel,
			PartOrdinal:        f.PartOrdinal,
		})
	}

	for _, fld := range item.Fields {
		entry := ManifestField{Name: fld.Name, Type: string(fld.Type), Text: fld.Text, Number: fld.Number, Flag: fld.Flag}
		if !fld.Date.IsZero() {
			d := fld.Date.UTC()
			entry.Date = &d
		}
		mi.Fields = append(mi.Fields, entry)
	}
	for _, c := range item.Captions {
		mi.Captions = append(mi.Captions, ManifestCaption{
			RelPath: c.RelPath, Kind: c.Kind, Format: c.Format,
			Language: c.Language, Forced: c.Forced, SDH: c.SDH,
		})
	}
	for _, m := range item.Memberships {
		mi.Memberships = append(mi.Memberships, ManifestMember{Relation: m.Relation, Kind: m.Kind, Title: m.Title})
	}
	return mi, nil
}

// fileBytes returns the byte-exact original for one file: the at-rest ciphertext
// decrypted for an Encrypted Library, or the file read as-is for a plaintext one.
func (s *Service) fileBytes(item store.ExportItem, f store.ExportFile) ([]byte, error) {
	if item.Encrypted {
		if s.decryptor == nil {
			return nil, ErrNoDecryptor
		}
		// The object identity is the file's persisted content fingerprint —
		// never its path. A file without one has no immutable identity and the
		// export fails closed rather than guessing.
		if f.ContentFingerprint == "" {
			return nil, errors.New("photoexport: encrypted file has no content fingerprint")
		}
		return s.decryptor.DecryptBytes(item.Media.LibraryID, f.Path, encryptedClassMediaBytes, f.ContentFingerprint)
	}
	return os.ReadFile(f.Path)
}

// exportFileName names a file in the originals directory. Multi-file Media
// disambiguate by (version, part) so a RAW+JPEG pair or a Live Photo's two parts
// never collide; the original extension is preserved for third-party readers.
func exportFileName(index int, f store.ExportFile) string {
	ext := filepath.Ext(f.Path)
	base := fmt.Sprintf("file-%02d", index)
	if f.VersionLabel != "" {
		base = fmt.Sprintf("%s-%s", base, safeComponent(f.VersionLabel))
	}
	base = fmt.Sprintf("%s-p%d", base, f.PartOrdinal)
	return base + ext
}

// safeComponent renders an identifier safe as a single path component: any
// separator or traversal char becomes '_', so a hostile id can never escape the
// export root.
func safeComponent(s string) string {
	if s == "" {
		return "_"
	}
	out := []rune(s)
	for i, r := range out {
		switch r {
		case '/', '\\', ':', 0:
			out[i] = '_'
		}
	}
	cleaned := string(out)
	if cleaned == "." || cleaned == ".." {
		return "_"
	}
	return cleaned
}

// writeFileAtomic writes bytes to path via a temp file + rename so a crash never
// leaves a half-written original or manifest. The file is 0600 (invariant 5: the
// export may contain decrypted Encrypted-Library bytes).
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".export-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
