-- Library Health Cache (ADR-0042): pre-computed per-Library, per-dimension
-- counts refreshed on each scan completion. Avoids running N_libraries × 5
-- live COUNT queries on every Console health-dashboard load. Falls back to
-- live queries for any Library that has no cached row yet (first-run or
-- post-migration). Cascade-deletes with the Library.
CREATE TABLE IF NOT EXISTS library_health_cache (
  library_id     TEXT    NOT NULL REFERENCES library(id) ON DELETE CASCADE,
  dimension      TEXT    NOT NULL,
  count          INTEGER NOT NULL DEFAULT 0,
  refreshed_at_ms INTEGER NOT NULL,
  PRIMARY KEY (library_id, dimension)
);
