package store

import (
	"context"
	"math"
)

// Similar-Media retrieval (CORE-1974, OpenSpec core-1974-sonic-similarity): a
// "more like these" read over the ADR-0041 vector store, seeded by Media rather
// than by an embedded query string. It serves any embedding kind; the first user
// is the per-file audio feature vector an Oscillux analysis pass writes, which is
// small (tens of values), so the scan covers the whole Reveal-eligible set up to
// a value budget instead of SemanticSearch's fixed 1,000-row window.

// MaxSimilarMediaSeeds bounds how many seed Media one SimilarMedia read accepts.
const MaxSimilarMediaSeeds = 50

// MaxSimilarMediaLimit caps the result size of one SimilarMedia read.
const MaxSimilarMediaLimit = 200

// defaultSimilarMediaLimit is the result size when the caller passes none.
const defaultSimilarMediaLimit = 50

// similarMediaScanValues bounds the vector values one SimilarMedia read scores
// (candidate rows times dimensions): 4M float32 values is 16 MiB of vectors and a
// few milliseconds of arithmetic. At 32 dimensions it covers 125,000 Media; a
// wider kind covers proportionally fewer, in the same deterministic order as
// SemanticSearch's window.
const similarMediaScanValues = 4_000_000

// similarMediaCandidateLimit is the candidate row budget for vectors of dims
// values, at least 1,000 rows (SemanticSearch's window) whatever the width.
func similarMediaCandidateLimit(dims int) int {
	if dims <= 0 {
		return maxSemanticSearchCandidates
	}
	return max(maxSemanticSearchCandidates, similarMediaScanValues/dims)
}

// SimilarMedia returns at most limit Media whose embeddings of kind are nearest
// (cosine) to the centroid of the seeds' unit-length embeddings, best first,
// never listing a seed. Seeds and candidates are read through the same
// Reveal, Library-access and certification scoping as SemanticSearch, so a seed
// the caller cannot see contributes nothing and a hidden candidate is never
// scored. Seeds without an embedding of kind are ignored; seeds whose
// dimensionality differs from the first usable seed (in the order given) are
// ignored. The result is empty, not an error, when no seed is usable.
//
// Scores are cosine similarities in [-1, 1]: feature vectors that are centred on
// a population mean (the audio features are) score below 0 for opposites.
func (s *Store) SimilarMedia(ctx context.Context, filter LibraryFilter, kind EmbeddingKind, seeds []MediaID, limit int) ([]SemanticResult, error) {
	if kind == "" {
		return nil, ValidationError("embedding kind is required")
	}
	if len(seeds) == 0 {
		return nil, ValidationError("at least one seed Media is required")
	}
	if len(seeds) > MaxSimilarMediaSeeds {
		return nil, ValidationError("too many seed Media")
	}
	if limit <= 0 {
		limit = defaultSimilarMediaLimit
	}
	limit = min(limit, MaxSimilarMediaLimit)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}

	seedVectors, err := s.embeddingsForMediaLocked(ctx, seeds, filter, kind)
	if err != nil {
		return nil, err
	}
	query, seedSet := similarMediaCentroid(seeds, seedVectors)
	if query == nil {
		return []SemanticResult{}, nil
	}

	results, err := s.semanticSearchSQL(ctx, filter, kind, query, limit+len(seedSet), similarMediaCandidateLimit(len(query)))
	if err != nil {
		return nil, err
	}
	out := make([]SemanticResult, 0, min(limit, len(results)))
	for _, r := range results {
		if seedSet[r.MediaID] {
			continue
		}
		out = append(out, r)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// similarMediaCentroid sums the usable seeds' vectors, each scaled to unit
// length so no seed outweighs another by magnitude, walking seeds in the given
// order so the dimensionality choice is deterministic. It returns a nil query
// when no seed is usable, plus the set of every named seed (usable or not) so
// the caller never lists one. A zero-length vector is skipped; a zero centroid
// (seeds that cancel out) is treated as unusable.
func similarMediaCentroid(seeds []MediaID, vectors map[MediaID][]float32) ([]float32, map[MediaID]bool) {
	seedSet := make(map[MediaID]bool, len(seeds))
	var sum []float64
	for _, id := range seeds {
		if seedSet[id] {
			continue
		}
		seedSet[id] = true
		vec, ok := vectors[id]
		if !ok || len(vec) == 0 {
			continue
		}
		if sum == nil {
			sum = make([]float64, len(vec))
		} else if len(vec) != len(sum) {
			continue
		}
		var norm float64
		for _, v := range vec {
			norm += float64(v) * float64(v)
		}
		if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
			continue
		}
		scale := 1 / math.Sqrt(norm)
		for i, v := range vec {
			sum[i] += float64(v) * scale
		}
	}
	var total float64
	for _, v := range sum {
		total += v * v
	}
	if total == 0 {
		return nil, seedSet
	}
	query := make([]float32, len(sum))
	for i, v := range sum {
		query[i] = float32(v)
	}
	return query, seedSet
}
