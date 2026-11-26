package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// Identification revision kinds (catalog 0140 / encrypted-library 0018).
const (
	IdentificationKindIdentify = "identify"
	IdentificationKindFixMatch = "fix-match"
)

// ErrIdentificationRequestConflict reports a request ID that already committed
// a different identification for the same Media. The earlier commit stands.
const ErrIdentificationRequestConflict = ValidationError("identification request ID was already used for a different identification")

// IdentificationCommit is one identification's local Media changes. RequestID
// is the idempotency receipt: a replay with the same content returns the
// committed revision without writing; "" mints a fresh ID, so the commit is
// never deduplicated.
type IdentificationCommit struct {
	RequestID    string
	IntentDigest string
	Metadata     MediaMetadata
	Provenance   MediaMetadataProvenance
	Fields       []MediaField
}

// IdentificationReceipt is the committed revision of an identification.
// Replayed is true when the request had already committed and nothing was
// written this time.
type IdentificationReceipt struct {
	RequestID string
	Revision  int64
	Replayed  bool
}

// IdentificationRevision is one immutable identification revision row.
type IdentificationRevision struct {
	Revision    int64
	RequestID   string
	Kind        string
	PluginID    string
	ScraperID   string
	CandidateID string
	ActorID     ProfileID
	DetailJSON  string
	AppliedAt   time.Time
}

// CommitMediaIdentification commits the replacement metadata, selected lock
// clears, candidate provenance, plugin fields and one immutable revision row
// together at operator precedence, on the plane that owns the Media. An
// Encrypted Library's Media commits on its own plane and emits no shared
// event; a sealed plane's Media is refused as not found.
func (s *Store) CommitMediaIdentification(ctx context.Context, id MediaID, commit IdentificationCommit) (IdentificationReceipt, error) {
	return s.commitIdentification(ctx, id, commit, IdentificationKindIdentify, nil, func(context.Context, *sql.Tx) (string, error) {
		if commit.IntentDigest == "" {
			return "{}", nil
		}
		detail, err := json.Marshal(struct {
			IntentDigest string `json:"intentDigest,omitempty"`
		}{IntentDigest: commit.IntentDigest})
		return string(detail), err
	})
}

// FixMatchCommit is a Fix Match: the identification plus the match pin and
// Unmatched-queue resolution, visible under Filter.
type FixMatchCommit struct {
	IdentificationCommit
	Filter LibraryFilter
}

// FixMatchAudit is the detail_json of a fix-match revision.
type FixMatchAudit struct {
	ClearedLocks     []string `json:"clearedLocks"`
	Ordinal          string   `json:"ordinal"`
	PinnedFiles      int64    `json:"pinnedFiles"`
	UnmatchedCleared bool     `json:"unmatchedCleared"`
	IntentDigest     string   `json:"intentDigest,omitempty"`
}

// CommitFixMatch applies a Fix Match in one transaction on the Media's own
// plane: the identification, the match pin on every fingerprinted file, the
// Unmatched-queue resolution and an immutable fix-match revision that audits
// them. A replay of the same request commits nothing and returns the original
// revision, so the audit converges to exactly one row per request.
func (s *Store) CommitFixMatch(ctx context.Context, id MediaID, commit FixMatchCommit) (IdentificationReceipt, error) {
	metadata := commit.Metadata
	return s.commitIdentification(ctx, id, commit.IdentificationCommit, IdentificationKindFixMatch, &commit.Filter,
		func(ctx context.Context, tx *sql.Tx) (string, error) {
			audit := FixMatchAudit{
				ClearedLocks: append([]string{}, metadata.ClearLocks...),
				Ordinal:      "kept",
				IntentDigest: commit.IntentDigest,
			}
			switch {
			case metadata.OperatorOrdinal != nil:
				audit.Ordinal = "set"
			case metadata.ClearOrdinal:
				audit.Ordinal = "cleared"
			}
			pinned, err := tx.ExecContext(ctx,
				"UPDATE media_file SET match_pinned = 1 WHERE media_id = ? AND content_fingerprint <> ''", id)
			if err != nil {
				return "", err
			}
			if audit.PinnedFiles, err = pinned.RowsAffected(); err != nil {
				return "", err
			}
			cleared, err := tx.ExecContext(ctx, "DELETE FROM unmatched_queue WHERE media_id = ?", id)
			if err != nil {
				return "", err
			}
			n, err := cleared.RowsAffected()
			audit.UnmatchedCleared = n > 0
			if err != nil {
				return "", err
			}
			detail, err := json.Marshal(audit)
			return string(detail), err
		})
}

func (s *Store) commitIdentification(ctx context.Context, id MediaID, commit IdentificationCommit, kind string, filter *LibraryFilter, finish func(context.Context, *sql.Tx) (string, error)) (IdentificationReceipt, error) {
	if err := validateMediaMetadata(id, commit.Metadata); err != nil {
		return IdentificationReceipt{}, err
	}
	if err := validateMediaFields(commit.Fields); err != nil {
		return IdentificationReceipt{}, err
	}
	provenance, err := normalizeMediaMetadataProvenance(commit.Provenance)
	if err != nil {
		return IdentificationReceipt{}, err
	}
	commit.Provenance = provenance
	commit.Metadata.Source = MutationSourceOperator
	if commit.RequestID == "" {
		if commit.RequestID, err = newIdentificationRequestID(); err != nil {
			return IdentificationReceipt{}, err
		}
	}
	digest, err := identificationDigest(kind, commit)
	if err != nil {
		return IdentificationReceipt{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return IdentificationReceipt{}, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return IdentificationReceipt{}, err
	}
	if filter != nil {
		if _, err := s.visibleMediaSQLFrom(ctx, plane.reader, id, *filter); err != nil {
			return IdentificationReceipt{}, err
		}
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return IdentificationReceipt{}, err
	}
	defer tx.Rollback()
	receipt, done, err := identificationReceiptTx(ctx, tx, id, commit.RequestID, digest)
	if err != nil || done {
		return receipt, err
	}
	now := s.serverNow().UTC()
	if err := applyIdentificationTx(ctx, tx, id, commit, now); err != nil {
		return IdentificationReceipt{}, err
	}
	detail := "{}"
	if finish != nil {
		if detail, err = finish(ctx, tx); err != nil {
			return IdentificationReceipt{}, err
		}
	}
	revision, err := appendIdentificationRevisionTx(ctx, tx, id, commit, digest, kind, detail, now)
	if err != nil {
		return IdentificationReceipt{}, err
	}
	if err := enqueueArtworkActionsTx(ctx, tx, id, revision, commit, now); err != nil {
		return IdentificationReceipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return IdentificationReceipt{}, err
	}
	if !plane.encrypted {
		s.emitMediaUpdated(ctx, id)
	}
	return IdentificationReceipt{RequestID: commit.RequestID, Revision: revision}, nil
}

// identificationReceiptTx reports an earlier commit of requestID: done with
// its revision when the content matches, the conflict error when it differs.
func identificationReceiptTx(ctx context.Context, tx *sql.Tx, id MediaID, requestID, digest string) (IdentificationReceipt, bool, error) {
	var revision int64
	var stored string
	err := tx.QueryRowContext(ctx, `
		SELECT revision, request_digest FROM media_identification_revision
		WHERE media_id = ? AND request_id = ?`, id, requestID).Scan(&revision, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return IdentificationReceipt{}, false, nil
	}
	if err != nil {
		return IdentificationReceipt{}, false, err
	}
	if stored != digest {
		return IdentificationReceipt{}, true, ErrIdentificationRequestConflict
	}
	return IdentificationReceipt{RequestID: requestID, Revision: revision, Replayed: true}, true, nil
}

func applyIdentificationTx(ctx context.Context, tx *sql.Tx, id MediaID, commit IdentificationCommit, now time.Time) error {
	metadata := commit.Metadata
	if err := updateMediaMetadataTx(ctx, tx, id, metadata); err != nil {
		return err
	}
	if err := upsertMediaMetadataProvenance(ctx, tx, id, commit.Provenance); err != nil {
		return err
	}
	var err error
	if metadata.OperatorOrdinal != nil {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO media_operator_ordinal(media_id, sequence, ordinal_index, set_at_ms, actor_profile_id)
			VALUES (?, ?, ?, ?, NULLIF(?, ''))
			ON CONFLICT(media_id) DO UPDATE SET sequence = excluded.sequence,
				ordinal_index = excluded.ordinal_index, set_at_ms = excluded.set_at_ms,
				actor_profile_id = excluded.actor_profile_id`,
			id, metadata.OperatorOrdinal.Sequence, metadata.OperatorOrdinal.Index,
			timeToMillis(now), metadata.OrdinalActor)
	} else if metadata.ClearOrdinal {
		_, err = deleteMediaOrdinal(ctx, tx, id)
	}
	if err != nil {
		return err
	}
	_, err = upsertMediaFieldsTx(ctx, tx, id, commit.Fields, MutationSourceOperator, false)
	return err
}

func appendIdentificationRevisionTx(ctx context.Context, tx *sql.Tx, id MediaID, commit IdentificationCommit, digest, kind, detail string, now time.Time) (int64, error) {
	var revision int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(revision), 0) + 1 FROM media_identification_revision WHERE media_id = ?`, id).
		Scan(&revision); err != nil {
		return 0, err
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO media_identification_revision(media_id, revision, request_id, request_digest, kind,
			plugin_id, scraper_id, candidate_id, actor_profile_id, detail_json, applied_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)`,
		id, revision, commit.RequestID, digest, kind, commit.Provenance.PluginID, commit.Provenance.ScraperID,
		commit.Provenance.CandidateID, string(commit.Metadata.OrdinalActor), detail, timeToMillis(now))
	return revision, err
}

// MediaIdentificationRevisions lists a Media's identification revisions,
// oldest first, from the plane that owns it. Callers gate visibility.
func (s *Store) MediaIdentificationRevisions(ctx context.Context, id MediaID) ([]IdentificationRevision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return nil, err
	}
	rows, err := plane.reader.QueryContext(ctx, `
		SELECT revision, request_id, kind, plugin_id, scraper_id, candidate_id,
			COALESCE(actor_profile_id, ''), detail_json, applied_at_ms
		FROM media_identification_revision WHERE media_id = ? ORDER BY revision`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var revisions []IdentificationRevision
	for rows.Next() {
		var r IdentificationRevision
		var applied int64
		if err := rows.Scan(&r.Revision, &r.RequestID, &r.Kind, &r.PluginID, &r.ScraperID, &r.CandidateID,
			&r.ActorID, &r.DetailJSON, &applied); err != nil {
			return nil, err
		}
		r.AppliedAt = millisToTime(applied)
		revisions = append(revisions, r)
	}
	return revisions, rows.Err()
}

// identificationDigest fingerprints the content a request commits, so a
// replayed request ID is told apart from a reused one.
func identificationDigest(kind string, commit IdentificationCommit) (string, error) {
	m := commit.Metadata
	payload, err := json.Marshal(struct {
		Kind                      string
		Title, Summary, SortTitle *string
		Year                      *int
		ClearLocks                []string
		Ordinal                   *MediaOrdinal
		ClearOrdinal              bool
		Actor                     ProfileID
		Provenance                MediaMetadataProvenance
		Fields                    []MediaField
		Artwork                   map[string]string
	}{kind, m.Title, m.Summary, m.SortTitle, m.Year, m.ClearLocks, m.OperatorOrdinal, m.ClearOrdinal, m.OrdinalActor,
		commit.Provenance, commit.Fields, m.Artwork})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func newIdentificationRequestID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "identification-" + hex.EncodeToString(raw[:]), nil
}
