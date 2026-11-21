package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
)

// DuplicateCopySet is one Media whose live Versions include two or more
// likely identical on-disk copies: the same non-empty OSHash and size. OSHash
// samples the file's ends, so this is a strong hint, not proof (CORE-2916).
// The verified-copy merge folds such copies into one Media as Versions; this
// read lets a manager see them and decide what to delete on disk. The server never deletes a file because of it.
type DuplicateCopySet struct {
	MediaID   MediaID
	Title     string
	LibraryID LibraryID
	Copies    []DuplicateCopy
}

// DuplicateCopy is one live part-0 file of a DuplicateCopySet.
type DuplicateCopy struct {
	Path         string
	SizeBytes    int64
	VersionLabel string
}

// DuplicateCopies lists the duplicate copy sets visible to the caller's scope,
// ordered by title. Trashed Media are left out, and so are Media above the
// caller's certification ceiling. Each visible Library is read on its own
// catalog plane, so an unsealed encrypted Library is covered; a sealed one is
// left out.
func (s *Store) DuplicateCopies(ctx context.Context, filter LibraryFilter) ([]DuplicateCopySet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	sets := []DuplicateCopySet{}
	for _, library := range libraries {
		if !libraryVisible(library, filter) {
			continue
		}
		plane, err := s.catalogPlaneForLibraryLocked(ctx, library.ID)
		if errors.Is(err, ErrEncryptedLibraryDBDisabled) {
			continue
		}
		if err != nil {
			return nil, err
		}
		certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
		if plane.encrypted {
			// The private catalog has no library table; scope by the catalog
			// Library row's Plugin instead.
			pluginID := ""
			if library.PluginID != nil {
				pluginID = *library.PluginID
			}
			certClause, certArgs = planeCertificationFilterSQL(pluginID, filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
		}
		found, err := duplicateCopiesSQL(ctx, plane.reader, library.ID, certClause, certArgs)
		if err != nil {
			return nil, err
		}
		sets = append(sets, found...)
	}
	sort.SliceStable(sets, func(i, j int) bool {
		if sets[i].Title != sets[j].Title {
			return sets[i].Title < sets[j].Title
		}
		return sets[i].MediaID < sets[j].MediaID
	})
	return sets, nil
}

func duplicateCopiesSQL(ctx context.Context, reader *sql.DB, libraryID LibraryID, certClause string, certArgs []any) ([]DuplicateCopySet, error) {
	args := append([]any{libraryID}, certArgs...)
	rows, err := reader.QueryContext(ctx, `
		SELECT m.id, m.title, m.library_id, f.oshash, f.size_bytes, f.path, f.version_label
		FROM media m
		JOIN media_file f ON f.media_id = m.id
		WHERE m.library_id = ? AND m.trashed_at_ms IS NULL
		  AND f.part_ordinal = 0 AND f.missing_at_ms IS NULL AND f.oshash <> ''
		  AND EXISTS (
		    SELECT 1 FROM media_file g
		    WHERE g.media_id = m.id AND g.id <> f.id
		      AND g.part_ordinal = 0 AND g.missing_at_ms IS NULL
		      AND g.oshash = f.oshash AND g.size_bytes = f.size_bytes)`+certClause+`
		ORDER BY m.title, m.id, f.oshash, f.size_bytes, f.version_label, f.path
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sets := []DuplicateCopySet{}
	var lastMedia MediaID
	var lastHash string
	var lastSize int64
	for rows.Next() {
		var set DuplicateCopySet
		var hash string
		var file DuplicateCopy
		if err := rows.Scan(&set.MediaID, &set.Title, &set.LibraryID, &hash, &file.SizeBytes, &file.Path, &file.VersionLabel); err != nil {
			return nil, err
		}
		if len(sets) == 0 || set.MediaID != lastMedia || hash != lastHash || file.SizeBytes != lastSize {
			sets = append(sets, set)
			lastMedia, lastHash, lastSize = set.MediaID, hash, file.SizeBytes
		}
		current := &sets[len(sets)-1]
		current.Copies = append(current.Copies, file)
	}
	return sets, rows.Err()
}
