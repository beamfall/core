package store

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// RetainedLegacyFingerprints returns unambiguous missing-file candidates for
// a scan. Read them once per walk: the available-path index excludes missing
// rows. Trashed Media and paths held by managed trash are ineligible.
func (s *Store) RetainedLegacyFingerprints(ctx context.Context, libraryID LibraryID, root string) (map[string]StoredFingerprint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	pathPrefix := strings.TrimSuffix(filepath.Clean(root), string(filepath.Separator)) + string(filepath.Separator)
	rows, err := plane.reader.QueryContext(ctx, `
		SELECT f.path, f.content_fingerprint, f.size_bytes, f.mtime_ms
		FROM media_file f JOIN media m ON m.id = f.media_id
		WHERE m.library_id = ? AND m.trashed_at_ms IS NULL AND f.missing_at_ms IS NOT NULL
		  AND f.path LIKE ? ESCAPE '\'
		GROUP BY f.path
		HAVING COUNT(*) = 1 AND f.content_fingerprint GLOB 'sha256:*'
	`, libraryID, escapeSQLLike(pathPrefix)+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	retained := make(map[string]StoredFingerprint)
	for rows.Next() {
		var path string
		var fp StoredFingerprint
		var mtimeMS int64
		if err := rows.Scan(&path, &fp.ContentFingerprint, &fp.SizeBytes, &mtimeMS); err != nil {
			return nil, err
		}
		fp.ModTime, fp.OK = millisToTime(mtimeMS), true
		retained[path] = fp
	}
	if err := rows.Err(); err != nil || len(retained) == 0 {
		return nil, err
	}

	roots, err := libraryRootsSQL(ctx, s.catalogR, libraryID)
	if err != nil {
		return nil, err
	}
	for _, managedRoot := range roots {
		if err := excludeManagedTrashFromScan(managedRoot, retained); err != nil {
			// Keep scanning when a manifest cannot be checked. The retained
			// rows cannot safely be used to revive an identity in this walk.
			return nil, nil
		}
	}
	return retained, nil
}

func excludeManagedTrashFromScan(root string, retained map[string]StoredFingerprint) error {
	dir := managedTrashRoot(root)
	trash, err := openManagedDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer trash.Close()
	entries, err := fs.ReadDir(trash.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// A leftover manifest with no entry no longer reserves the path.
		held, err := trash.Lstat(filepath.Join(entry.Name(), "entry"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		// Check case folding on a name that already exists, so scanning a
		// mounted root does not create a capability probe on it.
		upper, err := trash.Lstat(filepath.Join(entry.Name(), "ENTRY"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		foldCase := upper != nil && os.SameFile(held, upper)
		manifest, err := readManagedTrashManifest(filepath.Join(dir, entry.Name()))
		if err != nil {
			return err
		}
		pathPrefix := manifest.OriginalPath + string(filepath.Separator)
		for path := range retained {
			if (manifest.Kind == "folder" || len(path) == len(manifest.OriginalPath)) &&
				managedPathWithin(path, manifest.OriginalPath, pathPrefix, foldCase) {
				delete(retained, path)
			}
		}
	}
	return nil
}
