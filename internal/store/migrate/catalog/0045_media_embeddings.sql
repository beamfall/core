-- Vector store for EmbeddingProvider Extension Point (ADR-0041 §embedding-store).
-- Plugins write (fingerprint, kind, vector) tuples via Core's write-path seam;
-- Core owns retrieval — plugins never query directly.
--
-- media_fingerprint matches media_file.content_fingerprint (the sha256: identity
-- key). The FK cascades so embeddings are purged when their Media row goes away.
-- dims records the vector dimensionality so a future re-index can detect stale
-- rows without decoding the blob.  indexed_at_ms is epoch milliseconds.
CREATE TABLE media_embedding (
    media_fingerprint TEXT NOT NULL,
    embedding_kind    TEXT NOT NULL,
    dims              INTEGER NOT NULL,
    vector            BLOB NOT NULL,
    indexed_at_ms     INTEGER NOT NULL,
    PRIMARY KEY (media_fingerprint, embedding_kind)
);

-- pending_ai_tags: auto-tag proposals awaiting operator approval (ADR-0041
-- §pending-ai-tags).  AI writes land here, never directly on media.  status is
-- one of 'pending', 'approved', 'rejected'.  proposed_at_ms is epoch millis.
CREATE TABLE pending_ai_tag (
    id             TEXT NOT NULL PRIMARY KEY,
    media_id       TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    tag_kind       TEXT NOT NULL,
    value          TEXT NOT NULL,
    proposed_by    TEXT NOT NULL,
    proposed_at_ms INTEGER NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending'
);

-- failed_enrichment: per-media, per-kind enrichment failure log (ADR-0041
-- §max-retry-cap).  attempt_count lets the store enforce the cap and lets
-- operators observe problem items.  Cascades on Media deletion.
CREATE TABLE failed_enrichment (
    media_id       TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    embedding_kind TEXT NOT NULL,
    failed_at_ms   INTEGER NOT NULL,
    error_text     TEXT NOT NULL,
    attempt_count  INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (media_id, embedding_kind)
);
