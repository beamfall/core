package app

import (
	"context"
	"strings"

	"github.com/beamfall/core/internal/store"
)

// plugin-enrichment wiring note: plugin-enrichment is wired through a
// first-party semantic-provider seam, NOT as a plugin.Bundle. It does not
// participate in the Bundle activation flow (plugin.NewRegistry, Console
// override, plugin.ID keying) because it has no Entity Kinds, Sources, or
// scrapers to register — it contributes only an EmbeddingProvider seam
//. The Bundle abstraction is for domain data contributors;
// Provider is the right abstraction for a pure compute capability. If
// plugin-enrichment ever contributes domain data it would need to be rethreaded
// as a Bundle. The bundled implementation currently reports unavailable and
// emits no semantic vectors because it contains fingerprints, not a model.

// queryEmbedder adapts the enrichment provider to the core graph.QueryEmbedder
// seam. A future available provider computes vectors; Core owns the vector store
// and retrieval, so the seam type stays core consumer-defined
// rather than crossing into plugin-sdk.
type queryEmbedder struct{ p EnrichmentProvider }

func (q queryEmbedder) Kind() store.EmbeddingKind { return q.p.Kind() }
func (q queryEmbedder) Available() bool           { return q.p.Available() }
func (q queryEmbedder) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	return q.p.EmbedQuery(ctx, query)
}

type embeddingStore interface {
	MediaEmbeddingCandidates(context.Context, store.EmbeddingKind, int) ([]store.MediaEmbeddingCandidate, error)
	UpsertEmbedding(context.Context, string, store.EmbeddingKind, []float32) error
	RecordEnrichmentFailure(context.Context, store.MediaID, store.EmbeddingKind, string) error
}

// indexEmbeddings is the Core write-half of the ADR-0041 EmbeddingProvider: it
// drains Core's not-yet-indexed candidate list in batches and writes one
// embedding per Media via the store's UpsertEmbedding seam, computing each
// vector with an available semantic provider. The store
// coupling (candidate enumeration, UpsertEmbedding, the Reveal-filtered vector
// store) is why this loop stays in Core (ADR-0041 §2.1: Core owns the vector
// store) while the model lives in the plugin.
//
// Availability is the hard gate: the loop never reaches Kind, candidate
// enumeration, or writes when the provider reports unavailable.
func indexEmbeddings(ctx context.Context, st embeddingStore, p EnrichmentProvider) (int, error) {
	if p == nil || !p.Available() {
		return 0, nil
	}
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := indexEmbeddingBatch(ctx, st, p, store.MaxEmbeddingBatch)
		if err != nil {
			return total, err
		}
		total += n
		// Stop when a full scan produced no progress: either the candidate list
		// is empty, or every remaining candidate has empty text (never indexes).
		if n == 0 {
			return total, nil
		}
	}
}

// indexEmbeddingBatch embeds one store-bounded batch of candidates (ADR-0041
// §2.3 pipeline). A Media whose title+summary sanitizes to empty is skipped (an
// untitled, undescribed item has nothing to embed) rather than written as a
// degenerate zero vector. A single failed write is recorded against the retry
// cap and the batch continues — one bad item must not abort the whole batch.
func indexEmbeddingBatch(ctx context.Context, st embeddingStore, p EnrichmentProvider, batch int) (int, error) {
	kind := p.Kind()
	candidates, err := st.MediaEmbeddingCandidates(ctx, kind, batch)
	if err != nil {
		return 0, err
	}
	written := 0
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		clean, sanitizeErr := store.SanitizeEmbeddingInput(embedText(c.Title, c.Summary))
		if sanitizeErr != nil {
			continue
		}
		vec, embedErr := p.EmbedDocument(ctx, clean)
		if embedErr != nil {
			_ = st.RecordEnrichmentFailure(ctx, c.MediaID, kind, embedErr.Error())
			continue
		}
		if err := st.UpsertEmbedding(ctx, c.Fingerprint, kind, vec); err != nil {
			_ = st.RecordEnrichmentFailure(ctx, c.MediaID, kind, err.Error())
			continue
		}
		written++
	}
	return written, nil
}

// embedText joins the v1 text-metadata fields (title, summary) into one string
// for embedding (ADR-0041 §2.3, text-only). Fields are space-joined; empty
// fields drop out so the result is "" only when both are empty.
func embedText(title, summary string) string {
	parts := make([]string, 0, 2)
	if strings.TrimSpace(title) != "" {
		parts = append(parts, title)
	}
	if strings.TrimSpace(summary) != "" {
		parts = append(parts, summary)
	}
	return strings.Join(parts, " ")
}
