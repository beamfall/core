package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ManagedFileConverter is the consumer-defined seam the
// Conversion Workflow injects so the store package — never the convert package —
// owns the file mutation (arch conformance: file writes route through
// managed-file-ops). It transcodes src to dst using a named, audited preset; the
// concrete implementation is the core FFmpeg Runner. dst is a temp path inside
// the managed root that the store admits and atomically positions.
type ManagedFileConverter interface {
	// ConvertFile transcodes src to dst with the named preset, returning the
	// output container extension, primary codec, and media class actually
	// written (so the store names and admits the file correctly). An unknown
	// profile is rejected with an error.
	ConvertFile(ctx context.Context, src, dst, profile string) (container, codec string, class MediaClass, err error)
}

// ConvertResult is the outcome of a successful ConvertManagedFile: the new
// admitted Media and the trash record preserving the original (invariant 6).
// A succession recovery that finds the original already preserved reports a
// zero Original — the trash record belongs to the generation that moved it.
type ConvertResult struct {
	Converted Media
	Original  ManagedTrash
}

// ConvertOwnerLease names the live job lease a conversion runs under. It is
// the ADR-0174 ownership-succession token for the replacement path: the Owner
// string is the job-lease fencing token (workerID#generation, where the
// generation is the job's attempt count — strictly monotonic, incremented by
// every claim), and every durable write and side effect in the conversion is
// fenced on it. Both fields are required; there is no unfenced call path.
type ConvertOwnerLease struct {
	JobID JobID
	Owner string
}

// convertJournal is the durable succession journal record for one conversion
// attempt (ADR-0174: "the file swap and the succession mutation are ordered
// under a durable job journal"). It is stored as the job's checkpoint through
// the lease-fenced checkpoint write BEFORE any side effect, so recovery — same
// process or after a restart — can prove whether an on-disk destination is
// this job's own residue. Recovery trusts ONLY this record; a destination file
// the journal does not name is foreign and is never touched.
type convertJournal struct {
	Version int    `json:"version"`
	Dst     string `json:"dst"`
	Staging string `json:"staging"`
}

// convertStep fires the test-only interleaving hook at a named fence point.
func (s *Store) convertStep(step string) {
	if s.convertStepHook != nil {
		s.convertStepHook(step)
	}
}

// fenceConvertStep is one ownership-succession fence: it fires the step hook,
// then requires the caller's lease to still be THE live lease. ErrJobLeaseLost
// is the instruction to stop — the successor generation owns every later write.
func (s *Store) fenceConvertStep(ctx context.Context, step string, lease ConvertOwnerLease) error {
	s.convertStep(step)
	return s.verifyLiveJobLease(ctx, lease.JobID, lease.Owner)
}

// ResolveManagedMediaFile gates a managed source path for a per-op
// Profile-authority check (apple ADR-0003 §1) and confirms it is cataloged
// media. It returns the canonical on-disk path, its visible library root, and
// the backing MediaID. A profile whose LibraryFilter does not grant visibility
// to the library/path is rejected here ("path is outside the library roots" /
// "unknown library") — this is the rejection-without-authority gate. A path that
// resolves but is not cataloged media is rejected too (conversion operates only
// on managed library media, never arbitrary files).
func (s *Store) ResolveManagedMediaFile(ctx context.Context, libraryID LibraryID, path string, filter LibraryFilter) (resolvedPath, root string, mediaID MediaID, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", "", "", err
	}
	source, root, err := s.managedExistingPathLocked(ctx, libraryID, path, filter)
	if err != nil {
		return "", "", "", err
	}
	if source.Kind != "file" {
		return "", "", "", ValidationError("conversion source is not a file")
	}
	mediaID, _, err = s.mediaForManagedFilePath(ctx, libraryID, source.Path)
	if err != nil {
		return "", "", "", err
	}
	return source.Path, root, mediaID, nil
}

// ConvertManagedFile runs a user-directed conversion (apple ADR-0003 §1) under
// the ADR-0174 ownership-succession contract: it gates the source on the
// caller's Profile authority, drives the injected core FFmpeg converter to
// write a NEW managed file beside the original, admits/indexes that output like
// any managed file (ImportMediaFile), then preserves the ORIGINAL in trash
// (DeleteManagedPath — recoverable, invariant 6). FFmpeg is a least-privilege
// child writing to a temp under server-owned staging that is atomically
// positioned. Ordering is convert → journal → admit new → trash original: any
// earlier failure leaves the original untouched and admits nothing.
//
// Succession: the conversion runs only on behalf of a live job
// lease. The lease token is the monotonic succession token, and every durable
// write and side effect is fenced on it — entry, the durable journal write, the
// admission, the catalog import, and the trash of the original. Admission is
// NO-REPLACE, so a stale generation can never overwrite the bytes under a
// successor's admitted file. A crash or handover at any point leaves a state
// this method's entry recovery converges: a cataloged destination rolls the
// replacement forward, this job's own journal-named residue is removed and the
// conversion redone from source, and a destination the journal does not name
// refuses fail-closed. The job and catalog databases are separate SQLite
// files, so a fence and a catalog commit cannot share one transaction; the
// residual verify→commit window is convergent by construction (the import is a
// value-identical upsert for the same job, and the successor's recovery
// removes-and-redoes residue), never last-writer-wins.
func (s *Store) ConvertManagedFile(ctx context.Context, libraryID LibraryID, srcPath, profile string, converter ManagedFileConverter, filter LibraryFilter, lease ConvertOwnerLease, now time.Time) (ConvertResult, error) {
	if converter == nil {
		return ConvertResult{}, errors.New("converter is required")
	}
	if strings.TrimSpace(profile) == "" {
		return ConvertResult{}, ValidationError("conversion profile is required")
	}
	if lease.JobID == "" || strings.TrimSpace(lease.Owner) == "" {
		return ConvertResult{}, errors.New("conversion requires its owning job lease")
	}
	if err := s.fenceConvertStep(ctx, "entry", lease); err != nil {
		return ConvertResult{}, err
	}

	// Succession recovery: consult THIS job's durable journal before touching
	// anything. A prior generation may have died (or been fenced out) at any
	// point after its journal write; the journal names exactly what it was
	// doing. Journal staging is always redone fresh — staging residue is
	// removed here for a same-process handover and swept at startup for a
	// restart, so a recovery never reuses a dead generation's staged bytes.
	journal, err := s.convertJournalRecord(ctx, lease.JobID)
	if err != nil {
		return ConvertResult{}, err
	}
	if journal.Dst != "" {
		s.removeConvertStagingResidue(journal.Staging)
		done, result, err := s.recoverConvertJournal(ctx, libraryID, journal.Dst, srcPath, filter, lease, now)
		if err != nil {
			return ConvertResult{}, err
		}
		if done {
			return result, nil
		}
	}

	// Per-op authority gate + cataloged-media check.
	resolvedSrc, root, _, err := s.ResolveManagedMediaFile(ctx, libraryID, srcPath, filter)
	if err != nil {
		return ConvertResult{}, err
	}

	// Encrypted Libraries fail closed: a managed conversion stages
	// a plaintext transcode and admits it into the Library root as a new plaintext
	// managed file. There is no encrypted-plane write path yet, so
	// refuse the conversion — Sealed or Unsealed — rather than leak plaintext.
	encrypted, err := s.LibraryEncrypted(ctx, libraryID)
	if err != nil {
		return ConvertResult{}, err
	}
	if encrypted {
		return ConvertResult{}, ValidationError("managed conversion is unavailable for encrypted libraries")
	}

	// Compute the output path in the same managed directory, named after the
	// original stem with a "-<profile>" suffix and the new container extension.
	// The destination is validated to stay within the library root and must not
	// collide with an existing file (never overwrite, never clobber).
	dir := filepath.Dir(resolvedSrc)
	stem := strings.TrimSuffix(filepath.Base(resolvedSrc), filepath.Ext(resolvedSrc))

	// Run the converter into a SERVER-OWNED STAGING path OUTSIDE any Library root
	//, then admit the result by atomically moving it into the root.
	// The engine never writes inside the root: the catalog only ever sees the file
	// the store positions, and a co-tenant cannot read or replace the artifact in
	// the root during the admission window (closes the write→admission TOCTOU). We
	// let the converter report the container so the store names the file correctly
	// without re-deriving the preset table.
	tmpOut, err := s.newConvertStagingPath(profile)
	if err != nil {
		return ConvertResult{}, err
	}
	container, codec, class, err := converter.ConvertFile(ctx, resolvedSrc, tmpOut, profile)
	if err != nil {
		_ = os.Remove(tmpOut)
		return ConvertResult{}, err
	}
	dst := filepath.Join(dir, stem+"-"+sanitizeProfile(profile)+"."+container)
	if err := validateManagedTarget(root, dst); err != nil {
		_ = os.Remove(tmpOut)
		return ConvertResult{}, err
	}
	// A destination that exists now and is NOT named by this job's journal is
	// foreign: refuse fail-closed, never touch it. (This job's own residue was
	// already recovered above; a journal-named file materializing mid-convert is
	// handled by the admission's succession retry below.)
	if _, statErr := os.Lstat(dst); statErr == nil {
		if journal.Dst != dst {
			_ = os.Remove(tmpOut)
			return ConvertResult{}, ValidationError("converted output already exists")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		_ = os.Remove(tmpOut)
		return ConvertResult{}, statErr
	}

	// Durable succession journal write — the intent record, lease-fenced, and
	// strictly BEFORE any side effect. A stale owner is rejected here with
	// nothing on disk but its own staging, which it removes on the way out.
	record, err := json.Marshal(convertJournal{Version: 1, Dst: dst, Staging: tmpOut})
	if err != nil {
		_ = os.Remove(tmpOut)
		return ConvertResult{}, err
	}
	s.convertStep("journal")
	if err := s.UpdateJobCheckpoint(ctx, lease.JobID, lease.Owner, record, JobProgress{}, now); err != nil {
		_ = os.Remove(tmpOut)
		return ConvertResult{}, err
	}

	// Admission — fenced immediately before, NO-REPLACE, with one bounded
	// succession retry: a destination appearing since the foreign-file check can
	// only be a stale generation of this same job, so recovery either rolls the
	// replacement forward (the stale generation completed the import) or removes
	// the journal-named residue and admits again. The live generation never
	// fails terminally to a straggler it has already fenced out.
	if err := s.fenceConvertStep(ctx, "admit", lease); err != nil {
		_ = os.Remove(tmpOut)
		return ConvertResult{}, err
	}
	admitErr := admitStagedFileNoReplace(tmpOut, dst)
	if errors.Is(admitErr, os.ErrExist) {
		done, result, recErr := s.recoverConvertJournal(ctx, libraryID, dst, srcPath, filter, lease, now)
		if recErr != nil {
			_ = os.Remove(tmpOut)
			return ConvertResult{}, recErr
		}
		if done {
			_ = os.Remove(tmpOut)
			return result, nil
		}
		admitErr = admitStagedFileNoReplace(tmpOut, dst)
	}
	if admitErr != nil {
		_ = os.Remove(tmpOut)
		return ConvertResult{}, admitErr
	}

	// Admit/index the new managed file like any other (ImportMediaFile owns its
	// own lock + emits the catalog event), lease-fenced. On failure, undo the
	// on-disk write so we never leave an un-cataloged orphan and never trash the
	// original.
	if err := s.fenceConvertStep(ctx, "import", lease); err != nil {
		_ = removeConvertResidue(dst)
		return ConvertResult{}, err
	}
	media, _, err := s.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          libraryID,
		Path:               dst,
		Title:              stem,
		Container:          container,
		Codec:              codec,
		Class:              class,
		ContentFingerprint: HashMediaFilePath(dst),
	})
	if err != nil {
		_ = removeConvertResidue(dst)
		return ConvertResult{}, fmt.Errorf("admit converted file: %w", err)
	}

	// Preserve the original in trash (recoverable, invariant 6), lease-fenced.
	// The new file is already admitted and cataloged; a fence rejection here
	// leaves the completed-import state for the successor's recovery to roll
	// forward, and a trash failure still surfaces with the new file standing.
	if err := s.fenceConvertStep(ctx, "trash", lease); err != nil {
		return ConvertResult{Converted: media}, err
	}
	trash, err := s.DeleteManagedPath(ctx, libraryID, resolvedSrc, filter, now)
	if err != nil {
		return ConvertResult{Converted: media}, fmt.Errorf("preserve original in trash: %w", err)
	}
	return ConvertResult{Converted: media, Original: trash}, nil
}

// convertJournalRecord reads this job's durable succession journal. A missing,
// empty, or foreign-shaped checkpoint reports a zero journal — recovery then
// treats every on-disk destination as foreign, which fails closed.
func (s *Store) convertJournalRecord(ctx context.Context, id JobID) (convertJournal, error) {
	job, err := s.Job(ctx, id)
	if err != nil {
		return convertJournal{}, err
	}
	if len(job.Checkpoint) == 0 {
		return convertJournal{}, nil
	}
	var journal convertJournal
	if err := json.Unmarshal(job.Checkpoint, &journal); err != nil {
		return convertJournal{}, nil
	}
	if journal.Version != 1 || journal.Dst == "" {
		return convertJournal{}, nil
	}
	return journal, nil
}

// recoverConvertJournal converges the state a prior generation of this job left
// at its journal-named destination (ADR-0174: a handover or restart at any
// point either completes the replacement or rolls back to source-untouched):
//
//   - dst cataloged → the import committed; roll the replacement FORWARD:
//     preserve the original in trash if it is still live, and report done with
//     the cataloged Media.
//   - dst on disk but uncataloged → the admission landed without its import;
//     remove the residue through a proven directory descriptor and report
//     not-done (the caller redoes a full fresh conversion from source).
//   - dst absent → nothing to converge; report not-done (fresh conversion; the
//     journal's staging never survives a handover, so redo means re-encode).
//
// Every branch acts under the caller's authority filter, and the side-effect
// branches are lease-fenced. dst comes from this job's own journal ONLY.
func (s *Store) recoverConvertJournal(ctx context.Context, libraryID LibraryID, dst, srcPath string, filter LibraryFilter, lease ConvertOwnerLease, now time.Time) (bool, ConvertResult, error) {
	exists, mediaID, cataloged, err := s.convertDstState(ctx, libraryID, dst, filter)
	if err != nil {
		return false, ConvertResult{}, err
	}
	if !exists {
		return false, ConvertResult{}, nil
	}
	if cataloged {
		media, err := s.Media(ctx, mediaID, filter)
		if err != nil {
			return false, ConvertResult{}, err
		}
		// The original may already be preserved (the prior generation reached
		// its trash step) — then the replacement is complete. Otherwise finish
		// it: fence, then trash.
		if _, _, _, resolveErr := s.ResolveManagedMediaFile(ctx, libraryID, srcPath, filter); resolveErr != nil {
			return true, ConvertResult{Converted: media}, nil
		}
		if err := s.fenceConvertStep(ctx, "trash", lease); err != nil {
			return false, ConvertResult{}, err
		}
		trash, err := s.DeleteManagedPath(ctx, libraryID, srcPath, filter, now)
		if err != nil {
			return false, ConvertResult{}, fmt.Errorf("preserve original in trash: %w", err)
		}
		return true, ConvertResult{Converted: media, Original: trash}, nil
	}
	if err := s.fenceConvertStep(ctx, "residue", lease); err != nil {
		return false, ConvertResult{}, err
	}
	if err := removeConvertResidue(dst); err != nil {
		return false, ConvertResult{}, err
	}
	return false, ConvertResult{}, nil
}

// convertDstState classifies a journal-named destination under the caller's
// authority filter: absent, on disk but uncataloged, or cataloged (with the
// backing MediaID). Containment and trash-internal refusals surface as errors —
// a journal naming a path outside the visible roots is never acted on.
func (s *Store) convertDstState(ctx context.Context, libraryID LibraryID, dst string, filter LibraryFilter) (exists bool, mediaID MediaID, cataloged bool, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, "", false, err
	}
	source, _, err := s.managedExistingPathLocked(ctx, libraryID, dst, filter)
	if errors.Is(err, os.ErrNotExist) {
		return false, "", false, nil
	}
	if err != nil {
		return false, "", false, err
	}
	if source.Kind != "file" {
		return false, "", false, ValidationError("conversion destination is not a file")
	}
	mediaID, _, err = s.mediaForManagedFilePath(ctx, libraryID, source.Path)
	var validation ValidationError
	if errors.As(err, &validation) {
		return true, "", false, nil
	}
	if err != nil {
		return false, "", false, err
	}
	return true, mediaID, true, nil
}

// removeConvertResidue removes this job's own journal-named, uncataloged
// destination residue through a proven directory descriptor (the same posture
// as every managed-file mutation). An already-absent entry is success.
func removeConvertResidue(dst string) error {
	root, err := openManagedDir(filepath.Dir(dst))
	if err != nil {
		return err
	}
	defer root.Close()
	err = root.Remove(filepath.Base(dst))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// removeConvertStagingResidue removes a journal-named staging artifact, and
// ONLY a genuine one: the path must sit directly in this store's convert
// staging root and carry the convert staging suffix, so a corrupt or hostile
// journal can never aim the removal anywhere else. Best-effort — startup's
// SweepConvertStaging is the backstop.
func (s *Store) removeConvertStagingResidue(staging string) {
	if s.dataDir == "" || staging == "" {
		return
	}
	if filepath.Dir(staging) != convertStagingRoot(s.dataDir) {
		return
	}
	if !strings.HasSuffix(staging, convertStagingSuffix) {
		return
	}
	_ = os.Remove(staging)
}

// sanitizeProfile reduces a profile name to a filename-safe token (alnum, dash,
// underscore) so it can be embedded in the output filename without escaping.
func sanitizeProfile(profile string) string {
	var b strings.Builder
	for _, r := range profile {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := b.String()
	if out == "" {
		return "converted"
	}
	return out
}
