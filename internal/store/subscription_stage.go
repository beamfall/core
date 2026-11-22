package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
)

const (
	SubscriptionStagePrefix    = ".ingest-"
	SubscriptionStageSeparator = "--"
	SubscriptionStageSuffix    = ".staged"
)

// SubscriptionStageOwner is the staging namespace of one Subscription.
func SubscriptionStageOwner(subscriptionID SubscriptionID) string {
	sum := sha256.Sum256([]byte(subscriptionID))
	return hex.EncodeToString(sum[:16])
}

// SubscriptionStageKey hashes the exact GUID bytes inside the Subscription's namespace.
func SubscriptionStageKey(subscriptionID SubscriptionID, guid string) string {
	sum := sha256.Sum256([]byte(guid))
	return SubscriptionStageOwner(subscriptionID) + "." + hex.EncodeToString(sum[:])
}

// SubscriptionStageName encodes the publication destination in the staging filename.
func SubscriptionStageName(key, targetBase, suffix string) string {
	return SubscriptionStagePrefix + key + SubscriptionStageSeparator + targetBase + suffix
}

// bindStagedSubscriptionEntryTx handles a scan after the publish link survived
// a rolled-back transaction. The acknowledged stage must share the visible
// inode before the scan can record a binding.
func bindStagedSubscriptionEntryTx(ctx context.Context, tx *sql.Tx, libraryID LibraryID, path string) (MediaID, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT s.id, s.root, se.guid, se.media_id
		FROM subscription s
		JOIN subscription_seen_entry r ON r.subscription_id = s.id
		JOIN subscription_entry se ON se.subscription_id = r.subscription_id AND se.guid = r.guid
		JOIN media m ON m.id = se.media_id
		WHERE s.library_id = ? AND m.library_id = ? AND m.trashed_at_ms IS NULL
		  AND r.path = '' AND r.seen_at_ms > 0 AND se.download_state = 'queued' AND se.download_path = ''
		  AND NOT EXISTS (SELECT 1 FROM media_file f WHERE f.media_id = se.media_id AND f.missing_at_ms IS NULL)
	`, libraryID, libraryID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	dir, base := filepath.Dir(path), filepath.Base(path)
	var visible os.FileInfo
	for rows.Next() {
		var id SubscriptionID
		var root, guid string
		var mediaID MediaID
		if err := rows.Scan(&id, &root, &guid, &mediaID); err != nil {
			return "", err
		}
		// Compare directories by identity, not spelling: a case-insensitive
		// volume reaches one directory under several names.
		if !sameDirectory(root, dir) {
			continue
		}
		if visible == nil {
			visible, err = os.Lstat(path)
			if err != nil || !visible.Mode().IsRegular() {
				return "", nil
			}
		}
		stage := filepath.Join(dir, SubscriptionStageName(SubscriptionStageKey(id, guid), base, SubscriptionStageSuffix))
		if info, err := os.Lstat(stage); err != nil || !os.SameFile(visible, info) {
			continue
		}
		if err := rows.Close(); err != nil {
			return "", err
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE subscription_entry SET download_path = ? WHERE subscription_id = ? AND guid = ?
		`, path, id, guid)
		return mediaID, err
	}
	return "", rows.Err()
}

func sameDirectory(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}
