package store

// Embedding Store (ADR-0041 §embedding-store): Core owns the vector store and
// all retrieval.  Plugins write via EmbeddingProvider (a write-only Extension
// Point seam); Core never exposes a query interface to plugins.
//
// v1 retrieval is cosine similarity in pure Go — no CGO, no sqlite-vec
// extension — over a bounded, deterministic window scoped to Reveal-eligible
// Media BEFORE scoring. Restricted content is never ranked, scored, or leaked
// to an unrevealed caller (the hard rule from ADR-0041
// §semantic-reveal-contract).
//
// The schema lives in 0045_media_embeddings.sql:
//   - media_embedding  (fingerprint, kind) → (dims, vector blob, indexed_at_ms)
//   - pending_ai_tag   — auto-tag proposals awaiting operator approval
//   - failed_enrichment — per-media failure log enforcing the retry cap

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// EmbeddingKind names one semantic embedding model/schema — e.g. "text-v1",
// "clip-v1". Kind is opaque to Core; the plugin that writes a kind must be the
// one that produces the query vector for semantic search.
type EmbeddingKind string

// MaxEmbeddingInputBytes is the hard input-sanitization limit for embedding
// text (ADR-0041 §input-sanitization). Callers should truncate before passing
// to the plugin; Core rejects inputs that exceed it.
const MaxEmbeddingInputBytes = 4_096

// MaxEmbeddingBatch is the default number of Media candidates an embedding
// provider receives per store read.
const MaxEmbeddingBatch = 100

// MaxEmbeddingCandidateFieldChars bounds each text field selected for
// embedding. It is a SQL-side guardrail; the provider still applies the final
// byte budget after joining fields.
const MaxEmbeddingCandidateFieldChars = 2_048

// maxSemanticSearchCandidates bounds the vectors fetched and scored for one
// semantic query. The SQL candidate window is deliberately independent of the
// caller's result limit so an oversized limit cannot restore a full-table scan.
const maxSemanticSearchCandidates = 1_000

// MaxEnrichmentAttempts is the per-media, per-kind retry cap (ADR-0041
// §max-retry-cap). Once a media+kind pair reaches this count the store refuses
// further UpsertEmbedding calls.
const MaxEnrichmentAttempts = 5

// SemanticResult is one item returned by SemanticSearch, ordered by cosine
// similarity descending. Score is in [0, 1]; 1.0 is a perfect match.
type SemanticResult struct {
	MediaID MediaID
	Score   float32
}

// PendingAITagStatus is the lifecycle state of one pending_ai_tag row.
type PendingAITagStatus string

const (
	PendingAITagPending  PendingAITagStatus = "pending"
	PendingAITagApproved PendingAITagStatus = "approved"
	PendingAITagRejected PendingAITagStatus = "rejected"
)

// PendingAITag is one auto-tag proposal awaiting operator approval.
type PendingAITag struct {
	ID         string
	MediaID    MediaID
	TagKind    string
	Value      string
	ProposedBy string
	RunID      string
	ProposedAt time.Time
	Status     PendingAITagStatus
}

// SanitizeEmbeddingInput enforces the ADR-0041 input-sanitization contract:
// it trims leading/trailing whitespace, collapses runs of whitespace to a
// single space, and returns a ValidationError if the result exceeds
// MaxEmbeddingInputBytes or is empty.
func SanitizeEmbeddingInput(text string) (string, error) {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "", ValidationError("embedding input is empty")
	}
	if len(text) > MaxEmbeddingInputBytes {
		return "", ValidationError(fmt.Sprintf("embedding input exceeds %d bytes", MaxEmbeddingInputBytes))
	}
	return text, nil
}

// MediaEmbeddingCandidate is one Media item awaiting an embedding of some kind:
// its content fingerprint (the vector-store key) plus the text fields v1 embeds
// (title + summary/text metadata — ADR-0041 §embedding-input). The
// fingerprint is the canonical one for the Media; LibraryID rides along so the
// caller can attribute provenance, never for a reveal decision (indexing is a
// server-internal write, not a viewer read).
type MediaEmbeddingCandidate struct {
	MediaID     MediaID
	LibraryID   LibraryID
	Fingerprint string
	Title       string
	Summary     string
}

// MediaEmbeddingCandidates returns the live, non-trashed Media in AI-indexing-
// enabled Libraries that do NOT yet have an embedding of the given kind — the
// ADR-0041 post-scan work list the Enrichment Plugin walks to populate the
// vector store. The per-Library AI-indexing opt-out (ai_indexing_enabled) is
// honoured in SQL: a Media in an opted-out Library is never a candidate, so a
// disabled Library is never indexed and stays semantic-search-unavailable.
// Trashed Media, trashed Libraries, and missing files are excluded. limit <= 0
// applies a default cap so one call bounds its scan; callers loop until empty.
//
// This is a server-internal write-path enumeration, NOT a reveal-filtered read:
// it returns restricted-Library candidates too, because the provider must index
// them so a revealed caller can find them. The Reveal contract is enforced on
// the READ side (SemanticSearch / RecommendFromEngagement), never here.
func (s *Store) MediaEmbeddingCandidates(ctx context.Context, kind EmbeddingKind, limit int) ([]MediaEmbeddingCandidate, error) {
	if kind == "" {
		return nil, ValidationError("embedding kind is required")
	}
	if limit <= 0 {
		limit = MaxEmbeddingBatch
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	// One row per Media: the canonical fingerprint is the smallest
	// content_fingerprint among its live files (deterministic across runs). A
	// Media is a candidate when that canonical fingerprint has no embedding of
	// this kind — mirrors the "first embedding encountered per media" dedupe that
	// semanticSearchSQL applies on the read side, so we index exactly the
	// fingerprint search will later score.
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT m.id, m.library_id, fp.canonical,
		       SUBSTR(COALESCE(m.title, ''), 1, ?),
		       SUBSTR(TRIM(COALESCE(m.summary, '') || ' ' || COALESCE(ft.fields, '')), 1, ?)
		FROM media m
		JOIN library l ON l.id = m.library_id
		JOIN (
			SELECT media_id, MIN(content_fingerprint) AS canonical
			FROM media_file
			WHERE missing_at_ms IS NULL
			GROUP BY media_id
		) fp ON fp.media_id = m.id
		LEFT JOIN media_embedding e
		  ON e.media_fingerprint = fp.canonical
		  AND e.embedding_kind = ?
		LEFT JOIN (
			SELECT media_id, group_concat(value, ' ') AS fields
			FROM media_field_text
			GROUP BY media_id
		) ft ON ft.media_id = m.id
		WHERE m.trashed_at_ms IS NULL
		  AND l.trashed_at_ms IS NULL
		  AND l.ai_indexing_enabled = 1
		  AND e.media_fingerprint IS NULL
		ORDER BY m.id
		LIMIT ?
	`, MaxEmbeddingCandidateFieldChars, MaxEmbeddingCandidateFieldChars, string(kind), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MediaEmbeddingCandidate{}
	for rows.Next() {
		var c MediaEmbeddingCandidate
		var rawID, rawLib string
		if err := rows.Scan(&rawID, &rawLib, &c.Fingerprint, &c.Title, &c.Summary); err != nil {
			return nil, err
		}
		c.MediaID = MediaID(rawID)
		c.LibraryID = LibraryID(rawLib)
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpsertEmbedding stores or replaces the embedding vector for a
// (fingerprint, kind) pair (ADR-0041 EmbeddingProvider Extension Point write
// path). It enforces the retry cap via failed_enrichment.attempt_count: if a
// prior failure record exists and has reached MaxEnrichmentAttempts this call
// returns a ValidationError and does NOT write the embedding.
//
// fingerprint must be non-empty. vector must be non-empty. Rewrites for an
// existing (fingerprint, kind) must keep the original dimensionality; a model
// that changes dimensions must write a new embedding kind.
func (s *Store) UpsertEmbedding(ctx context.Context, fingerprint string, kind EmbeddingKind, vector []float32) error {
	if fingerprint == "" {
		return ValidationError("fingerprint is required")
	}
	if kind == "" {
		return ValidationError("embedding kind is required")
	}
	if len(vector) == 0 {
		return ValidationError("vector is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}

	// Retry-cap check: if a failure row exists and hit the cap, block the write.
	var attempts int
	err := s.catalogR.QueryRowContext(ctx,
		`SELECT attempt_count FROM failed_enrichment
		 WHERE media_id = (SELECT id FROM media WHERE id IN
		   (SELECT media_id FROM media_file WHERE content_fingerprint = ?))
		   AND embedding_kind = ?`,
		fingerprint, string(kind),
	).Scan(&attempts)
	if err == nil && attempts >= MaxEnrichmentAttempts {
		return ValidationError(fmt.Sprintf("enrichment retry cap (%d) reached for this media+kind", MaxEnrichmentAttempts))
	}
	// sql.ErrNoRows means no failure record — fine, proceed.

	blob := float32SliceToBlob(vector)
	nowMS := time.Now().UTC().UnixMilli()
	result, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO media_embedding (media_fingerprint, embedding_kind, dims, vector, indexed_at_ms)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (media_fingerprint, embedding_kind) DO UPDATE
		  SET dims = excluded.dims,
		      vector = excluded.vector,
		      indexed_at_ms = excluded.indexed_at_ms
		  WHERE media_embedding.dims = excluded.dims
	`, fingerprint, string(kind), len(vector), blob, nowMS)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ValidationError("embedding dimensionality mismatch for fingerprint+kind")
	}
	return nil
}

// RecordEnrichmentFailure logs one enrichment failure for (mediaID, kind),
// incrementing attempt_count on conflict. This is the write path for
// MaxEnrichmentAttempts enforcement — plugins call this when they cannot
// produce a vector for a media item. Callers should follow up with a
// UpsertEmbedding attempt check to honour the cap on the next cycle.
func (s *Store) RecordEnrichmentFailure(ctx context.Context, mediaID MediaID, kind EmbeddingKind, errText string) error {
	if mediaID == "" {
		return ValidationError("media ID is required")
	}
	if kind == "" {
		return ValidationError("embedding kind is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	nowMS := time.Now().UTC().UnixMilli()
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO failed_enrichment (media_id, embedding_kind, failed_at_ms, error_text, attempt_count)
		VALUES (?, ?, ?, ?, 1)
		ON CONFLICT (media_id, embedding_kind) DO UPDATE
		  SET failed_at_ms   = excluded.failed_at_ms,
		      error_text     = excluded.error_text,
		      attempt_count  = failed_enrichment.attempt_count + 1
	`, string(mediaID), string(kind), nowMS, errText)
	return err
}

// SemanticSearch returns at most limit Media items whose embeddings of the
// given kind are nearest to queryVector, reveal-filtered by filter (ADR-0041
// §semantic-reveal-contract). Restricted-library Media are excluded from the
// candidate scan before scoring — they are never ranked or partially revealed.
//
// v1 algorithm: cosine similarity in Go over a deterministic, SQL-bounded
// window of the Reveal-eligible fingerprint set. The window trades global
// nearest-neighbour recall for a hard per-request CPU and memory bound until an
// indexed vector-search implementation replaces it.
func (s *Store) SemanticSearch(ctx context.Context, filter LibraryFilter, kind EmbeddingKind, queryVector []float32, limit int) ([]SemanticResult, error) {
	if kind == "" {
		return nil, ValidationError("embedding kind is required")
	}
	if len(queryVector) == 0 {
		return nil, ValidationError("query vector is required")
	}
	if limit <= 0 {
		limit = 50
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	return s.semanticSearchSQL(ctx, filter, kind, queryVector, limit, maxSemanticSearchCandidates)
}

// semanticSearchSQL is the inner, locked implementation.
// ADR-0041 hard rule: the SQL JOIN to media / library scopes the candidate
// set to Reveal-eligible rows BEFORE the vector is fetched — the WHERE clause
// on is_restricted and trashed_at_ms is not a post-filter; it gates which rows
// are even read from media_embedding.
func (s *Store) semanticSearchSQL(ctx context.Context, filter LibraryFilter, kind EmbeddingKind, queryVector []float32, limit, candidateLimit int) ([]SemanticResult, error) {
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	var visible []LibraryID
	for _, library := range libraries {
		if s.embeddingLibraryReadableLocked(library, filter) {
			visible = append(visible, library.ID)
		}
	}
	if len(visible) == 0 {
		return []SemanticResult{}, nil
	}

	placeholders := make([]string, len(visible))
	args := make([]any, 0, len(visible)+4)
	for i, id := range visible {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, string(kind), len(queryVector))
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	args = append(args, certArgs...)
	args = append(args, candidateLimit)

	// Fetch candidate (media_id, vector blob) rows scoped to Reveal-eligible
	// libraries. Drive from visible Media/File rows, then probe the embedding
	// primary key by (fingerprint, kind); CROSS JOIN prevents SQLite from
	// reverting to an embedding-table scan. The SQL LIMIT bounds rows before
	// vector blobs enter Go.
	rows, err := s.catalogR.QueryContext(ctx, semanticSearchCandidateQuery(placeholders, certClause), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candidates []embeddingCandidate
	seen := map[MediaID]bool{}
	for rows.Next() {
		var rawID string
		var dims int
		var blob []byte
		if err := rows.Scan(&rawID, &dims, &blob); err != nil {
			return nil, err
		}
		id := MediaID(rawID)
		if seen[id] {
			continue // multiple files per media: keep only the first embedding encountered
		}
		seen[id] = true
		vec, err := blobToFloat32Slice(blob)
		if err != nil {
			continue // corrupt blob — skip rather than abort the whole result
		}
		score := cosineSimilarity(queryVector, vec)
		if score == cosineSimilarityMismatch {
			continue // dims/blob disagree — not comparable, skip
		}
		candidates = append(candidates, embeddingCandidate{mediaID: id, score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Sort descending by score, then truncate to limit.
	sortCandidates(candidates)
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	results := make([]SemanticResult, len(candidates))
	for i, c := range candidates {
		results[i] = SemanticResult{MediaID: c.mediaID, Score: c.score}
	}
	return results, nil
}

func semanticSearchCandidateQuery(placeholders []string, certClause string) string {
	return `
		SELECT m.id, e.dims, e.vector
		FROM media m
		JOIN library l ON l.id = m.library_id
		JOIN media_file mf ON mf.media_id = m.id
		CROSS JOIN media_embedding e
		WHERE m.library_id IN (` + strings.Join(placeholders, ", ") + `)
		  AND m.trashed_at_ms IS NULL
		  AND mf.missing_at_ms IS NULL
		  AND l.trashed_at_ms IS NULL
		  AND e.media_fingerprint = mf.content_fingerprint
		  AND e.embedding_kind = ?
		  AND e.dims = ?
	` + certClause + `
		ORDER BY m.library_id, m.title COLLATE NOCASE, m.id
		LIMIT ?
	`
}

// Recommendation seed weights: favorite > most-played >
// recently-played. A favorited item is the strongest declared signal; raw play
// count is a softer one; a bare recent touch is the weakest. The weight scales
// the seed vector's contribution to the centroid, so stronger signals pull the
// query toward their neighbourhood.
const (
	recommendFavoriteWeight float32 = 3.0
	recommendPlayWeight     float32 = 1.0
	recommendRecencyWeight  float32 = 0.5
)

// recommendRecencyHalfLifeMS is the recency decay half-life (30 days). A seed's
// recency contribution is halved every half-life, so a play from last week
// pulls harder than one from last quarter — the ADR-0053 §2.3 recency decay.
const recommendRecencyHalfLifeMS = 30 * 24 * 3600 * 1000

// recommendSeedLimit caps how many engagement rows seed one recommendation
// query. Bounds the events-DB scan and the subsequent embedding lookups on a
// long-running Profile; the strongest-weighted recent rows dominate the
// centroid anyway, so the cap does not meaningfully change the result.
const recommendSeedLimit = 500

// RecommendFromEngagement is the ADR-0053 `recommend` retrieval mode: a
// content-based, engagement-seeded read over the existing ADR-0041 vector store.
// It is a sibling of SemanticSearch — same Core-owned vector store, same
// brute-force-cosine retrieval, same Reveal-filtered candidate scan — differing
// only in where the query vector comes from: instead of an embedded query
// string, it derives a weighted centroid from the embeddings of the Profile's
// own engagement seeds (favorites, most-played, recently-played per ADR-0005),
// then returns the nearest content-neighbours, EXCLUDING the seeds themselves
// and any item the Profile has already finished (watched).
//
// There is NO collaborative filtering: the seed is the Profile's own engagement
// only; no other Profile's data is read. The seed is
// computed live, per query, never stored.
//
// Graceful degradation: an empty result is returned (never an
// error) when the Profile has no engagement, when none of the engaged items have
// an embedding of the requested kind yet (so the mode works before ENR-1
// populates embeddings), or when no candidate neighbours remain after excluding
// seeds + finished items.
//
// Reveal-filtering is identical to SemanticSearch: the candidate
// scan's SQL JOIN scopes neighbours to Reveal-eligible rows BEFORE scoring, so a
// restricted neighbour never surfaces to an unrevealed caller. The seed itself is
// read from the same Reveal-filtered visible set, so a restricted seed item can
// only ever pull restricted neighbours into a Session already entitled to see
// them.
func (s *Store) RecommendFromEngagement(ctx context.Context, profileID ProfileID, filter LibraryFilter, kind EmbeddingKind, limit int) ([]SemanticResult, error) {
	if profileID == "" {
		return nil, ValidationError("profile ID is required")
	}
	if kind == "" {
		return nil, ValidationError("embedding kind is required")
	}
	if limit <= 0 {
		limit = 50
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}

	// 1. Gather the Profile's engagement seeds (events DB), weighted per §2.3.
	//    A media item may be both favorited and played; weights sum so a
	//    favorited, heavily-played item seeds hardest. watched=1 items still seed
	//    (a finished item is a strong "more like this" signal) but are excluded
	//    from the RESULT set below.
	seedWeights, finished, err := s.recommendSeedWeightsLocked(ctx, profileID)
	if err != nil {
		return nil, err
	}
	if len(seedWeights) == 0 {
		return []SemanticResult{}, nil // no engagement → graceful empty
	}

	// 2. Build the weighted seed centroid from the seeds' embeddings of this
	//    kind. Seeds are read through the same Reveal-filtered visible set as the
	//    candidate scan, so a hidden seed never contributes to the query vector.
	seedIDs := make([]MediaID, 0, len(seedWeights))
	for id := range seedWeights {
		seedIDs = append(seedIDs, id)
	}
	seedVectors, err := s.embeddingsForMediaLocked(ctx, seedIDs, filter, kind)
	if err != nil {
		return nil, err
	}
	if len(seedVectors) == 0 {
		return []SemanticResult{}, nil // no embeddings yet (pre-ENR-1) → graceful empty
	}
	var dims int
	for _, v := range seedVectors {
		dims = len(v)
		break
	}
	centroid := make([]float64, dims)
	var combined bool
	for id, vec := range seedVectors {
		if len(vec) != dims {
			continue // mixed-dimension seeds are not combinable; skip the odd one
		}
		w := float64(seedWeights[id])
		for i := range vec {
			centroid[i] += w * float64(vec[i])
		}
		combined = true
	}
	if !combined {
		return []SemanticResult{}, nil
	}
	queryVector := make([]float32, dims)
	for i := range centroid {
		queryVector[i] = float32(centroid[i])
	}

	// 3. Nearest-neighbour scan over the Reveal-eligible candidate set (same SQL
	//    path as semantic), then exclude seeds + finished items from the result.
	// Recommendation exclusions happen after scoring, so retain its existing
	// unbounded candidate scan. AUDIT0724-PERF-EMBED-1 caps SemanticSearch only;
	// recommendation retrieval needs a separate pre-cap seed/finished exclusion.
	results, err := s.semanticSearchSQL(ctx, filter, kind, queryVector, limit+len(seedWeights), -1)
	if err != nil {
		return nil, err
	}
	out := make([]SemanticResult, 0, limit)
	for _, r := range results {
		if _, isSeed := seedWeights[r.MediaID]; isSeed {
			continue // never recommend an item the Profile already engaged
		}
		if finished[r.MediaID] {
			continue // never recommend an already-finished item
		}
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// recommendSeedWeightsLocked reads the Profile's engagement (events DB) into a
// per-media weight (favorites + plays + recency, ADR-0053 §2.3) plus the set of
// items the Profile has finished (watched). Caller holds s.mu. The query is
// scoped to the Profile only — no other Profile's rows are read (no CF).
func (s *Store) recommendSeedWeightsLocked(ctx context.Context, profileID ProfileID) (map[MediaID]float32, map[MediaID]bool, error) {
	weights := map[MediaID]float32{}
	finished := map[MediaID]bool{}
	nowMS := timeToMillis(time.Now().UTC())

	// Played / recently-played seeds from playback_progress.
	progressRows, err := s.eventsR.QueryContext(ctx, `
		SELECT media_id, play_count, watched, updated_at_ms
		FROM playback_progress
		WHERE profile_id = ?
		ORDER BY updated_at_ms DESC
		LIMIT ?
	`, profileID, recommendSeedLimit)
	if err != nil {
		return nil, nil, err
	}
	defer progressRows.Close()
	for progressRows.Next() {
		var mediaID MediaID
		var playCount, watched int
		var updatedAtMS int64
		if err := progressRows.Scan(&mediaID, &playCount, &watched, &updatedAtMS); err != nil {
			return nil, nil, err
		}
		decay := recencyDecay(nowMS, updatedAtMS)
		w := recommendRecencyWeight * decay
		if playCount > 0 {
			w += recommendPlayWeight * float32(playCount) * decay
		}
		weights[mediaID] += w
		if watched != 0 {
			finished[mediaID] = true
		}
	}
	if err := progressRows.Err(); err != nil {
		return nil, nil, err
	}

	// Favorite seeds (strongest signal).
	favRows, err := s.eventsR.QueryContext(ctx, `
		SELECT media_id, favorited_at_ms
		FROM media_favorite
		WHERE profile_id = ?
		ORDER BY favorited_at_ms DESC
		LIMIT ?
	`, profileID, recommendSeedLimit)
	if err != nil {
		return nil, nil, err
	}
	defer favRows.Close()
	for favRows.Next() {
		var mediaID MediaID
		var favoritedAtMS int64
		if err := favRows.Scan(&mediaID, &favoritedAtMS); err != nil {
			return nil, nil, err
		}
		weights[mediaID] += recommendFavoriteWeight * recencyDecay(nowMS, favoritedAtMS)
	}
	if err := favRows.Err(); err != nil {
		return nil, nil, err
	}
	return weights, finished, nil
}

// recencyDecay returns an exponential recency multiplier in (0, 1]: 1.0 for a
// just-now event, halving every recommendRecencyHalfLifeMS. A future or zero
// timestamp clamps to 1.0 (no decay).
func recencyDecay(nowMS, eventMS int64) float32 {
	age := nowMS - eventMS
	if age <= 0 {
		return 1.0
	}
	return float32(math.Pow(0.5, float64(age)/float64(recommendRecencyHalfLifeMS)))
}

// embeddingsForMediaLocked returns the embedding vectors of the given kind for
// the subset of the given Media IDs that are BOTH Reveal-eligible under filter
// AND have an embedding of that kind. Caller holds s.mu. It reuses the exact
// Reveal-scoping JOIN that semanticSearchSQL's candidate scan uses (restricted /
// trashed / missing rows are gated in SQL before any vector is read), so a
// hidden seed never reaches the caller. Chunked to bound the IN-list size.
func (s *Store) embeddingsForMediaLocked(ctx context.Context, ids []MediaID, filter LibraryFilter, kind EmbeddingKind) (map[MediaID][]float32, error) {
	out := map[MediaID][]float32{}
	if len(ids) == 0 {
		return out, nil
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	visible := map[LibraryID]bool{}
	for _, library := range libraries {
		if s.embeddingLibraryReadableLocked(library, filter) {
			visible[library.ID] = true
		}
	}
	if len(visible) == 0 {
		return out, nil
	}
	visibleIDs := make([]LibraryID, 0, len(visible))
	for id := range visible {
		visibleIDs = append(visibleIDs, id)
	}
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)

	const chunkSize = 400
	for start := 0; start < len(ids); start += chunkSize {
		chunk := ids[start:min(start+chunkSize, len(ids))]
		mediaPlaceholders := make([]string, len(chunk))
		libPlaceholders := make([]string, len(visibleIDs))
		args := make([]any, 0, 1+len(chunk)+len(visibleIDs)+len(certArgs))
		args = append(args, string(kind))
		for i, id := range chunk {
			mediaPlaceholders[i] = "?"
			args = append(args, id)
		}
		for i, id := range visibleIDs {
			libPlaceholders[i] = "?"
			args = append(args, id)
		}
		args = append(args, certArgs...)
		rows, err := s.catalogR.QueryContext(ctx, `
			SELECT mf.media_id, e.vector
			FROM media_embedding e
			JOIN media_file mf ON mf.content_fingerprint = e.media_fingerprint
			JOIN media m ON m.id = mf.media_id
			JOIN library l ON l.id = m.library_id
			WHERE e.embedding_kind = ?
			  AND m.id IN (`+strings.Join(mediaPlaceholders, ", ")+`)
			  AND m.library_id IN (`+strings.Join(libPlaceholders, ", ")+`)
			  AND m.trashed_at_ms IS NULL
			  AND mf.missing_at_ms IS NULL
			  AND l.trashed_at_ms IS NULL
		`+certClause, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var rawID string
			var blob []byte
			if err := rows.Scan(&rawID, &blob); err != nil {
				rows.Close()
				return nil, err
			}
			id := MediaID(rawID)
			if _, seen := out[id]; seen {
				continue // multiple files per media: keep the first embedding
			}
			vec, err := blobToFloat32Slice(blob)
			if err != nil {
				continue // corrupt blob — skip rather than abort
			}
			out[id] = vec
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

func (s *Store) embeddingLibraryReadableLocked(library Library, filter LibraryFilter) bool {
	if !libraryVisible(library, filter) {
		return false
	}
	if !library.Encrypted {
		return true
	}
	db := s.encryptedDBs[library.ID]
	return db != nil && db.state == "unsealed-registered" && db.reader != nil
}

// ProposePendingAITag inserts one auto-tag proposal into pending_ai_tag,
// awaiting operator approval (ADR-0041 §pending-ai-tags). The caller supplies
// a stable ID (e.g. derived from media+kind+value).
func (s *Store) ProposePendingAITag(ctx context.Context, tag PendingAITag) error {
	if tag.ID == "" {
		return ValidationError("tag ID is required")
	}
	if tag.MediaID == "" {
		return ValidationError("media ID is required")
	}
	if tag.TagKind == "" {
		return ValidationError("tag kind is required")
	}
	if tag.Value == "" {
		return ValidationError("tag value is required")
	}
	if tag.ProposedBy == "" {
		return ValidationError("proposedBy is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if tag.RunID != "" {
		if err := s.enrichmentRunExistsSQL(ctx, tag.RunID); err != nil {
			return err
		}
	}
	tombstoned, err := s.aiTagTombstonedSQL(ctx, tag.MediaID, tag.TagKind, tag.Value)
	if err != nil {
		return err
	}
	if tombstoned {
		return nil
	}
	nowMS := time.Now().UTC().UnixMilli()
	if !tag.ProposedAt.IsZero() {
		nowMS = tag.ProposedAt.UTC().UnixMilli()
	}
	if tag.RunID == "" {
		_, err = s.catalogW.ExecContext(ctx, `
			INSERT INTO pending_ai_tag (id, media_id, tag_kind, value, proposed_by, proposed_at_ms, status)
			VALUES (?, ?, ?, ?, ?, ?, 'pending')
			ON CONFLICT (id) DO NOTHING
		`, tag.ID, string(tag.MediaID), tag.TagKind, tag.Value, tag.ProposedBy, nowMS)
		return err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO pending_ai_tag (id, media_id, tag_kind, value, proposed_by, proposed_at_ms, status)
		VALUES (?, ?, ?, ?, ?, ?, 'pending')
		ON CONFLICT (id) DO NOTHING
	`, tag.ID, string(tag.MediaID), tag.TagKind, tag.Value, tag.ProposedBy, nowMS)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		return nil
	}
	if err := appendEnrichmentRunProposalTx(ctx, tx, tag.RunID, tag.ID, tag.MediaID); err != nil {
		return err
	}
	return tx.Commit()
}

// SetPendingAITagStatus transitions a pending_ai_tag to approved or rejected.
// Only 'approved' and 'rejected' are valid target statuses; 'pending' cannot
// be re-set. Unknown IDs are silently ignored (idempotent).
func (s *Store) SetPendingAITagStatus(ctx context.Context, tagID string, status PendingAITagStatus) error {
	if tagID == "" {
		return ValidationError("tag ID is required")
	}
	if status != PendingAITagApproved && status != PendingAITagRejected {
		return ValidationError("status must be 'approved' or 'rejected'")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if status == PendingAITagRejected {
		var mediaID MediaID
		var field, value string
		err := tx.QueryRowContext(ctx, `SELECT media_id, tag_kind, value FROM pending_ai_tag WHERE id = ?`, tagID).
			Scan(&mediaID, &field, &value)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if err := rejectMediaFieldValueTx(ctx, tx, mediaID, MediaField{Name: field, Type: EntityFieldText, Text: value}); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pending_ai_tag SET status = ? WHERE id = ?`, string(status), tagID); err != nil {
		return err
	}
	return tx.Commit()
}

// PendingAITagQueueItem is one row of the operator review queue: the proposal
// plus the Media's title so the Console can render a diff preview without a
// second round-trip. Library identity rides along for the reveal scope.
type PendingAITagQueueItem struct {
	Tag        PendingAITag
	MediaTitle string
	LibraryID  LibraryID
}

// PendingAITagQueue lists every still-pending auto-tag proposal the caller may
// see, oldest first (ADR-0041 §pending-ai-tags — the operator review queue).
// Reveal-and-Library filtered: proposals on Media in a Library the caller can't
// see are excluded at the SQL layer (the JOIN scopes to visible Libraries
// BEFORE any row is read), so the queue is never an existence oracle for hidden
// content. Only status='pending' rows are returned — approved/rejected
// proposals have left the queue.
func (s *Store) PendingAITagQueue(ctx context.Context, filter LibraryFilter, limit int) ([]PendingAITagQueueItem, error) {
	if limit <= 0 {
		limit = 100
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	var visible []LibraryID
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			visible = append(visible, library.ID)
		}
	}
	if len(visible) == 0 {
		return []PendingAITagQueueItem{}, nil
	}
	placeholders := make([]string, len(visible))
	args := make([]any, 0, len(visible)+1)
	for i, id := range visible {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, limit)
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT p.id, p.media_id, p.tag_kind, p.value, p.proposed_by, p.proposed_at_ms, p.status,
		       m.title, m.library_id
		FROM pending_ai_tag p
		JOIN media m ON m.id = p.media_id
		WHERE p.status = 'pending'
		  AND m.trashed_at_ms IS NULL
		  AND m.library_id IN (`+strings.Join(placeholders, ", ")+`)
		ORDER BY p.proposed_at_ms, p.id
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PendingAITagQueueItem{}
	for rows.Next() {
		var item PendingAITagQueueItem
		var proposedAtMS int64
		var statusStr, libraryID string
		if err := rows.Scan(&item.Tag.ID, &item.Tag.MediaID, &item.Tag.TagKind, &item.Tag.Value,
			&item.Tag.ProposedBy, &proposedAtMS, &statusStr, &item.MediaTitle, &libraryID); err != nil {
			return nil, err
		}
		item.Tag.ProposedAt = time.UnixMilli(proposedAtMS).UTC()
		item.Tag.Status = PendingAITagStatus(statusStr)
		item.LibraryID = LibraryID(libraryID)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out))
	for _, item := range out {
		ids = append(ids, item.Tag.ID)
	}
	runIDs, err := s.enrichmentRunIDsForPendingTags(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Tag.RunID = runIDs[out[i].Tag.ID]
	}
	return out, nil
}

// PromotePendingAITag is the AI-safety approval gate: the ONLY
// path by which an auto-tag reaches canonical Media metadata. It runs in one
// transaction: confirm the proposal is still pending and its Media is visible
// under filter, write the canonical media_field with the AI provenance stamp,
// and flip the proposal to 'approved'. A reject never calls this — it only sets
// status='rejected' and the staged value is discarded. Plugins cannot reach
// this path: they write only to pending_ai_tag via ProposePendingAITag.
//
// The promoted tag lands as a text media_field named by tag_kind, and the
// Media's mutated_by provenance is stamped MutationSourceAI so the operator can
// always tell an AI-applied value from a human or scan one.
func (s *Store) PromotePendingAITag(ctx context.Context, tagID string, filter LibraryFilter) error {
	return s.promotePendingAITag(ctx, tagID, "", filter)
}

func (s *Store) PromotePendingAITagInRun(ctx context.Context, tagID, runID string, filter LibraryFilter) error {
	if runID == "" {
		return ValidationError("enrichment run id is required")
	}
	return s.promotePendingAITag(ctx, tagID, runID, filter)
}

func (s *Store) promotePendingAITag(ctx context.Context, tagID, runID string, filter LibraryFilter) error {
	if tagID == "" {
		return ValidationError("tag ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	proposalRunID, err := s.enrichmentRunIDForPendingTag(ctx, tagID)
	if err != nil {
		return err
	}
	if runID == "" {
		runID = proposalRunID
	} else {
		if proposalRunID != "" && proposalRunID != runID {
			return ValidationError("pending tag belongs to another enrichment run")
		}
		if err := s.enrichmentRunExistsSQL(ctx, runID); err != nil {
			return err
		}
	}

	// Load the proposal + its Media's library/restriction in one read.
	var mediaID, tagKind, value, statusStr, libraryID string
	var restricted int
	err = s.catalogR.QueryRowContext(ctx, `
		SELECT p.media_id, p.tag_kind, p.value, p.status, m.library_id, l.is_restricted
		FROM pending_ai_tag p
		JOIN media m ON m.id = p.media_id
		JOIN library l ON l.id = m.library_id
		WHERE p.id = ?`, tagID,
	).Scan(&mediaID, &tagKind, &value, &statusStr, &libraryID, &restricted)
	if err != nil {
		return ValidationError("pending tag not found")
	}
	if PendingAITagStatus(statusStr) != PendingAITagPending {
		return ValidationError("pending tag is not awaiting approval")
	}
	// Reveal gate: never promote (or even confirm the existence of) a proposal
	// on Media the caller cannot see.
	if !libraryVisible(Library{ID: LibraryID(libraryID), IsRestricted: restricted != 0}, filter) {
		return ValidationError("pending tag not found")
	}
	if tombstoned, err := s.aiTagTombstonedSQL(ctx, MediaID(mediaID), tagKind, value); err != nil {
		return err
	} else if tombstoned {
		return ValidationError("pending tag was removed")
	}

	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	exists, err := mediaFieldExistsTx(ctx, tx, MediaID(mediaID), tagKind)
	if err != nil {
		return err
	}
	if exists {
		return ValidationError("media field already authored")
	}
	field := MediaField{Name: tagKind, Type: EntityFieldText, Text: value}
	if runID != "" {
		if err := appendEnrichmentRunItemTx(ctx, tx, runID, tagID, MediaID(mediaID), field); err != nil {
			return err
		}
	}
	if err := insertMediaFields(ctx, tx, MediaID(mediaID), []MediaField{field}); err != nil {
		return err
	}
	// Stamp AI provenance on the Media so the value is attributable.
	if _, err := tx.ExecContext(ctx,
		`UPDATE media SET mutated_by = ? WHERE id = ?`, string(MutationSourceAI), mediaID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE pending_ai_tag SET status = ? WHERE id = ?`, string(PendingAITagApproved), tagID); err != nil {
		return err
	}
	return tx.Commit()
}

// PendingAITags returns pending proposals for a media item, oldest first.
// Reveal-filtered: if the Media's library is restricted and filter.Revealed is
// false, the result is always empty.
func (s *Store) PendingAITags(ctx context.Context, mediaID MediaID, filter LibraryFilter) ([]PendingAITag, error) {
	if mediaID == "" {
		return nil, ValidationError("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}

	// Reveal check: look up the Media's library to enforce filter.
	var libraryID string
	var restricted int
	err := s.catalogR.QueryRowContext(ctx,
		`SELECT m.library_id, l.is_restricted FROM media m JOIN library l ON l.id = m.library_id WHERE m.id = ?`,
		string(mediaID),
	).Scan(&libraryID, &restricted)
	if err != nil {
		return []PendingAITag{}, nil // unknown media — yield empty, not error
	}
	if !libraryVisible(Library{ID: LibraryID(libraryID), IsRestricted: restricted != 0}, filter) {
		return []PendingAITag{}, nil
	}

	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT id, media_id, tag_kind, value, proposed_by, proposed_at_ms, status
		FROM pending_ai_tag
		WHERE media_id = ?
		ORDER BY proposed_at_ms
	`, string(mediaID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := []PendingAITag{}
	for rows.Next() {
		var t PendingAITag
		var proposedAtMS int64
		var statusStr string
		if err := rows.Scan(&t.ID, &t.MediaID, &t.TagKind, &t.Value, &t.ProposedBy, &proposedAtMS, &statusStr); err != nil {
			return nil, err
		}
		t.ProposedAt = time.UnixMilli(proposedAtMS).UTC()
		t.Status = PendingAITagStatus(statusStr)
		tags = append(tags, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(tags))
	for _, tag := range tags {
		ids = append(ids, tag.ID)
	}
	runIDs, err := s.enrichmentRunIDsForPendingTags(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range tags {
		tags[i].RunID = runIDs[tags[i].ID]
	}
	return tags, nil
}

func (s *Store) enrichmentRunIDForPendingTag(ctx context.Context, tagID string) (string, error) {
	runIDs, err := s.enrichmentRunIDsForPendingTags(ctx, []string{tagID})
	if err != nil {
		return "", err
	}
	return runIDs[tagID], nil
}

func (s *Store) enrichmentRunIDsForPendingTags(ctx context.Context, tagIDs []string) (map[string]string, error) {
	wanted := map[string]struct{}{}
	for _, id := range tagIDs {
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	out := map[string]string{}
	if len(wanted) == 0 {
		return out, nil
	}
	// ponytail: schema-free journal scan; add a pending_ai_tag.run_id column if proposal volume makes this hot.
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT i.batch_id, i.prior_state
		FROM op_batch_item i
		JOIN op_batch b ON b.id = i.batch_id
		WHERE b.kind = ?
		ORDER BY b.created_at_ms, i.seq
	`, opKindAITagRun)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var runID, raw string
		if err := rows.Scan(&runID, &raw); err != nil {
			return nil, err
		}
		var state enrichmentRunItemState
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			return nil, err
		}
		if _, ok := wanted[state.PendingID]; ok {
			out[state.PendingID] = runID
		}
	}
	return out, rows.Err()
}

// --- vector helpers ----------------------------------------------------------

// float32SliceToBlob encodes a []float32 as a little-endian byte blob.
// The encoding is 4 bytes per element, same endianness on every platform,
// so blobs are portable across SQLite WAL copies.
func float32SliceToBlob(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

// blobToFloat32Slice decodes a little-endian blob back to []float32.
func blobToFloat32Slice(b []byte) ([]float32, error) {
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("embedding blob length %d not a multiple of 4", len(b))
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v, nil
}

// cosineSimilarityMismatch is the sentinel cosineSimilarity returns when the
// two vectors have different dimensionality. It is below the valid [0, 1] range
// so callers can detect and skip the pair rather than ranking a meaningless
// truncated overlap (e.g. across a 768→1024 model upgrade).
const cosineSimilarityMismatch float32 = -1

// cosineSimilarity returns the cosine similarity in [0, 1] between two vectors.
// Vectors of different dimensionality are not comparable: it returns
// cosineSimilarityMismatch (a sentinel below [0, 1]) so the caller skips the
// pair instead of scoring a truncated overlap. An all-zero vector returns 0.
func cosineSimilarity(a, b []float32) float32 {
	n := len(a)
	if len(b) != n {
		return cosineSimilarityMismatch
	}
	var dot, normA, normB float64
	for i := 0; i < n; i++ {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return float32(dot / denom)
}

// embeddingCandidate is one scored result from the brute-force cosine scan.
type embeddingCandidate struct {
	mediaID MediaID
	score   float32
}

// sortCandidates sorts candidates in place, highest score first.
// Uses insertion sort: for the typical limit (10–200 top-k results from a
// brute-force scan) O(n log n) stdlib sort would be fine too, but insertion
// sort avoids an allocation and keeps the code dependency-free.
func sortCandidates(candidates []embeddingCandidate) {
	for i := 1; i < len(candidates); i++ {
		j := i
		for j > 0 && candidates[j].score > candidates[j-1].score {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
			j--
		}
	}
}
