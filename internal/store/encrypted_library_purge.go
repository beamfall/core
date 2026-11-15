package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// libraryPurgePendingConfigPrefix keys a server_config row per encrypted Library
// whose purge has committed but whose private database files or key material
// may still exist. The purge transaction writes it with the catalog deletion;
// completeLibraryPurge deletes it only once every teardown step has succeeded.
const libraryPurgePendingConfigPrefix = "library.purge-pending."

// OfflinePackDirName is the data-folder subdirectory holding offline pack
// ciphertext, one folder per pack.
const OfflinePackDirName = "offline-packs"

// EncryptedLibraryKeyEraser destroys a purged encrypted Library's key material
// so nothing it left behind can be decrypted again. Erasing keys that are
// already gone must succeed. The store calls it with no lock held: the key
// manager calls into the store while it holds its own per-Library lease.
type EncryptedLibraryKeyEraser interface {
	EraseLibraryKeys(libraryID LibraryID) error
}

// SetEncryptedLibraryKeyEraser installs the key-erasure seam, once at wiring
// time like SetEncryptedDerivativeReconciler. Until it is installed an
// encrypted Library cannot be purged.
func (s *Store) SetEncryptedLibraryKeyEraser(e EncryptedLibraryKeyEraser) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.encKeyEraser = e
}

// checkEncryptedLibraryPurgeableTx refuses an encrypted Library purge the store
// could not finish. Without a key eraser the keys would outlive the Library. A
// Library whose private-database folder name another encrypted Library shares
// (safeLibraryPathComponent is lossy) cannot have that folder removed without
// destroying the other Library's database.
func (s *Store) checkEncryptedLibraryPurgeableTx(ctx context.Context, tx *sql.Tx, id LibraryID, canEraseKeys bool) error {
	if !canEraseKeys {
		return ValidationError("an encrypted library cannot be removed permanently while its key store is unavailable")
	}
	others, err := queryIDs[LibraryID](ctx, tx, `SELECT id FROM library WHERE encrypted = 1 AND id != ?`, id)
	if err != nil {
		return err
	}
	component := safeLibraryPathComponent(id)
	for _, other := range others {
		if safeLibraryPathComponent(other) == component {
			return ValidationError("this encrypted library shares its private database folder with another library and cannot be removed permanently")
		}
	}
	return nil
}

// purgeEncryptedLibraryPackItemsTx removes the offline pack items that name a
// purged encrypted Library's Media (CORE-1852). That Media lives in the
// Library's private database, so the catalog media delete trigger never sees
// it. While the Library is unsealed, listed is true and mediaIDs holds every
// Media id it had, and their items go by id. A sealed Library's ids cannot be
// listed, so each pack that names the Library drops the items no readable
// plane holds. A pack that also names another sealed encrypted Library keeps
// its items: which of them belonged to the purged Library cannot be told.
// It returns the removed items' ciphertext paths, whose files the caller
// removes once the purge has committed.
func (s *Store) purgeEncryptedLibraryPackItemsTx(ctx context.Context, tx *sql.Tx, id LibraryID, mediaIDs []MediaID, listed bool) ([]string, error) {
	var ciphertexts []string
	removeItems := func(selectPaths, deleteItems string, args ...any) error {
		paths, err := queryIDs[string](ctx, tx, selectPaths, args...)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, deleteItems, args...); err != nil {
			return err
		}
		ciphertexts = append(ciphertexts, paths...)
		return nil
	}
	if listed {
		for _, mediaID := range mediaIDs {
			if err := removeItems(
				`SELECT ciphertext_path FROM offline_pack_item WHERE media_id = ?`,
				`DELETE FROM offline_pack_item WHERE media_id = ?`, mediaID); err != nil {
				return nil, err
			}
		}
		return ciphertexts, nil
	}
	packs, err := s.offlinePacksNamingOnlySealedLibraryTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	for _, pack := range packs {
		items, err := queryIDs[MediaID](ctx, tx, `SELECT media_id FROM offline_pack_item WHERE pack_id = ?`, pack)
		if err != nil {
			return nil, err
		}
		for _, mediaID := range items {
			_, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
			var missing ValidationError
			if err != nil && !errors.As(err, &missing) {
				return nil, err
			}
			if err == nil {
				continue
			}
			if err := removeItems(
				`SELECT ciphertext_path FROM offline_pack_item WHERE pack_id = ? AND media_id = ?`,
				`DELETE FROM offline_pack_item WHERE pack_id = ? AND media_id = ?`, pack, mediaID); err != nil {
				return nil, err
			}
		}
	}
	return ciphertexts, nil
}

// removeOfflinePackCiphertexts deletes the ciphertext files of offline pack
// items a committed purge removed (CORE-1852). Only files under the data
// folder's offline pack root are touched. The rows are already gone, so a
// failure is logged rather than returned.
func (s *Store) removeOfflinePackCiphertexts(ctx context.Context, paths []string) {
	if len(paths) == 0 || s.dataDir == "" {
		return
	}
	packRoot := filepath.Join(s.dataDir, OfflinePackDirName)
	root, err := os.OpenRoot(packRoot)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.WarnContext(ctx, "library purge: offline pack folder unavailable", "error", err)
		}
		return
	}
	defer func() { _ = root.Close() }()
	for _, path := range paths {
		rel, err := filepath.Rel(packRoot, path)
		if path == "" || err != nil || !filepath.IsLocal(rel) {
			continue
		}
		if err := root.Remove(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.WarnContext(ctx, "library purge: offline pack item file removal failed", "error", err)
		}
	}
}

// offlinePacksNamingOnlySealedLibraryTx returns the packs whose Library list
// names id and no other encrypted Library that is sealed.
func (s *Store) offlinePacksNamingOnlySealedLibraryTx(ctx context.Context, tx *sql.Tx, id LibraryID) ([]OfflinePackID, error) {
	quoted, err := json.Marshal(id)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, library_ids_json FROM offline_pack WHERE instr(library_ids_json, ?) > 0`, string(quoted))
	if err != nil {
		return nil, err
	}
	type candidate struct {
		id         OfflinePackID
		libraryIDs []LibraryID
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		var libraryIDsJSON string
		if err := rows.Scan(&c.id, &libraryIDsJSON); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(libraryIDsJSON), &c.libraryIDs); err != nil {
			_ = rows.Close()
			return nil, err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var packs []OfflinePackID
	for _, c := range candidates {
		if !slices.Contains(c.libraryIDs, id) {
			continue
		}
		shared, err := s.namesOtherSealedEncryptedLibraryTx(ctx, tx, id, c.libraryIDs)
		if err != nil {
			return nil, err
		}
		if !shared {
			packs = append(packs, c.id)
		}
	}
	return packs, nil
}

func (s *Store) namesOtherSealedEncryptedLibraryTx(ctx context.Context, tx *sql.Tx, id LibraryID, libraryIDs []LibraryID) (bool, error) {
	for _, other := range libraryIDs {
		if other == id {
			continue
		}
		var encrypted int
		err := tx.QueryRowContext(ctx, `SELECT encrypted FROM library WHERE id = ?`, other).Scan(&encrypted)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, err
		}
		if encrypted == 0 {
			continue
		}
		if db := s.encryptedDBs[other]; db == nil || db.state != "unsealed-registered" || db.reader == nil {
			return true, nil
		}
	}
	return false, nil
}

// completeLibraryPurge tears down a purged encrypted Library and then clears
// its pending row. Every step is idempotent, so it is safe to repeat after a
// partial failure or a crash.
//
// Ordering: the catalog commit in purgeLibraryLocked is the single point of no
// return. Nothing here runs before it, so a failure up to and including that
// commit leaves the Library whole and restorable. Once it has committed the
// Library is purged, and the pending row it wrote records the teardown still
// owed: the plaintext working copy (with its WAL/SHM) is wiped first, then the
// ciphertext and its pre-migration backups are removed, then the keys are
// erased even if a file step failed, because erased keys alone make any
// leftover ciphertext unreadable. The pending row survives any failure, and
// CompletePendingLibraryPurges retries it. Keys are never erased before the
// commit: that would leave a Library that is still listed but can never be
// decrypted.
func (s *Store) completeLibraryPurge(ctx context.Context, id LibraryID) error {
	eraser, fileErr := s.removePurgedEncryptedLibraryFiles(id)
	if eraser == nil && fileErr == nil {
		fileErr = errors.New("no encrypted-library key eraser is installed")
	}
	var keyErr error
	if eraser != nil {
		if err := eraser.EraseLibraryKeys(id); err != nil {
			keyErr = fmt.Errorf("erase key material: %w", err)
		}
	}
	if err := errors.Join(fileErr, keyErr); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `DELETE FROM server_config WHERE key = ?`, libraryPurgePendingConfigPrefix+string(id))
	return err
}

// removePurgedEncryptedLibraryFiles closes and unregisters the Library's
// private-database plane and removes every file it kept: the plaintext working
// copy and its sidecars, the ciphertext, and the pre-migration backups. It
// returns the installed key eraser, read under the same lock.
func (s *Store) removePurgedEncryptedLibraryFiles(id LibraryID) (EncryptedLibraryKeyEraser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	paths := s.encryptedLibraryDBPaths(id)
	var joined error
	// A plane quarantined at Open is keyed by its folder name, not the id.
	for _, key := range []LibraryID{id, LibraryID(safeLibraryPathComponent(id))} {
		db := s.encryptedDBs[key]
		if db == nil {
			continue
		}
		if db.reader != nil {
			joined = errors.Join(joined, db.reader.Close())
		}
		if db.writer != nil {
			joined = errors.Join(joined, db.writer.Close())
		}
		delete(s.encryptedDBs, key)
	}
	joined = errors.Join(joined,
		cleanupEncryptedLibraryWorkingDir(filepath.Dir(paths.working), paths.working),
		os.RemoveAll(filepath.Dir(paths.ciphertext)),
		os.RemoveAll(s.encryptedLibraryBackupDir(id)))
	return s.encKeyEraser, joined
}

// CompletePendingLibraryPurges finishes every encrypted Library purge whose
// teardown is still owed after a failure or a crash. The app calls it once the
// key eraser is installed at start-up, and every retention sweep calls it.
func (s *Store) CompletePendingLibraryPurges(ctx context.Context) error {
	s.mu.RLock()
	err := s.guardLocked()
	var keys []string
	if err == nil {
		keys, err = queryIDs[string](ctx, s.catalogR,
			`SELECT key FROM server_config WHERE key LIKE ? ORDER BY key`, libraryPurgePendingConfigPrefix+"%")
	}
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	var joined error
	for _, key := range keys {
		id, ok := strings.CutPrefix(key, libraryPurgePendingConfigPrefix)
		if !ok || id == "" {
			continue
		}
		if err := s.completeLibraryPurge(ctx, LibraryID(id)); err != nil {
			joined = errors.Join(joined, fmt.Errorf("library %s: %w", id, err))
		}
	}
	return joined
}
