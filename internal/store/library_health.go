package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Library Health: operator-only library-quality reporting over the
// existing catalog DB — no separate health store, no aggregate score. Six
// generic, domain-agnostic dimensions, each a count + a drill-down list, all
// reveal-and-Library filtered exactly like every other read path. The
// vocabulary is named by shape (unmatched/broken/scrape-failure/missing
// artwork/duplicate/no-scraper), never by domain, so the AGPL firewall
// (invariant #1) stays intact.
//
// The dimensions, all scoped to live (non-trashed) Media:
//   - unmatched         media not identified (no identified title, no external
//     ID, no local-metadata identification)
//   - broken            media whose primary file is gone from disk (missing_at_ms set)
//   - scrape-failure    unmatched media an automatic scrape attempted and queued
//   - missing-artwork   media with no applied or stored artwork in any slot its
//     archetype's scrapers supply
//   - duplicate         media sharing a non-empty content hash with another live file
//   - no-scraper        unidentified media in a Library no plugin scraper can search
//
// Which dimensions apply to a Library follows its plugin's LibraryHealthProfile
// (CORE-2340, CORE-2383). A dimension that does not apply is absent from the
// Library's counts and lists no items.
//
// duplicate detection keys on the oshash content hash: two distinct Media whose
// available files carry the same non-empty oshash are byte-identical duplicates
// (ADR-0009 content-fingerprint identity). The perceptual phash is deliberately
// NOT used here — a phash match is a near-duplicate heuristic that belongs to an
// interactive review, not a high-confidence count.

// HealthDimension names one health dimension by shape (never by domain).
type HealthDimension string

const (
	HealthUnmatched      HealthDimension = "unmatched"
	HealthBroken         HealthDimension = "broken"
	HealthScrapeFailure  HealthDimension = "scrape-failure"
	HealthMissingArtwork HealthDimension = "missing-artwork"
	HealthDuplicate      HealthDimension = "duplicate"
	HealthNoScraper      HealthDimension = "no-scraper"
)

// HealthDimensions is the fixed, ordered dimension vocabulary.
var HealthDimensions = []HealthDimension{
	HealthUnmatched,
	HealthBroken,
	HealthScrapeFailure,
	HealthMissingArtwork,
	HealthDuplicate,
	HealthNoScraper,
}

// LibraryHealth is one Library's per-dimension counts. The aggregate summary
// row is the sum of these across visible Libraries, computed by the
// caller from the slice LibraryHealthCounts returns. Counts holds only the
// dimensions that apply to the Library.
type LibraryHealth struct {
	LibraryID LibraryID
	Title     string
	Counts    map[HealthDimension]int
}

// HealthItem is one Media item surfaced in a dimension drill-down. It carries
// only the operator-relevant identity — no domain metadata.
type HealthItem struct {
	MediaID   MediaID
	LibraryID LibraryID
	Title     string
}

// identifiedPredicate is the SQL fragment over `media m` for a Media the
// catalog counts as identified: an identified title, an external ID (scraped
// or read from local tags), or — for a plugin that does not auto-identify —
// the plugin's local-metadata hydration marker.
func identifiedPredicate(p LibraryHealthProfile) (string, []any) {
	frag := `m.title_identified = 1 OR EXISTS (
		SELECT 1 FROM media_external_id x WHERE x.media_id = m.id
	)`
	if p.LocalIdentityField == "" {
		return frag, nil
	}
	return frag + ` OR EXISTS (
		SELECT 1 FROM media_field_int i
		WHERE i.media_id = m.id AND i.field = ? AND i.value = 1
	)`, []any{p.LocalIdentityField}
}

// dimensionPredicate is the per-dimension SQL WHERE fragment (and its args)
// over the `media m` alias, selecting live Media that exhibits the dimension.
// Every fragment is AND-composed with the visibility and trashed-at guards by
// the callers below, so the fragments themselves never repeat those clauses.
// Missing artwork is judged in Go (healthMissingArtworkLocked) and has no
// fragment here.
func dimensionPredicate(p LibraryHealthProfile, dim HealthDimension) (string, []any) {
	switch dim {
	case HealthUnmatched, HealthNoScraper:
		identified, args := identifiedPredicate(p)
		return `NOT (` + identified + `)`, args
	case HealthBroken:
		// Broken = the Media cannot play. Two generic, domain-agnostic causes
		// fold into this one dimension:
		//   (a) no available (non-missing) file remains — the bytes are gone
		//       (missing_at_ms set on every file; a re-import to a new path
		//       clears it, so this is the live "bytes are gone" signal); or
		//   (b) an available file failed decode-verify (decode_broken = 1) — the
		//       bytes are present on disk but won't decode (corrupt/truncated),
		//       flagged by the weekly decode-verify sampling job.
		// Either way the Media is unplayable, which is exactly what "broken"
		// means; FEAT-8 deliberately reuses this dimension rather than adding a
		// sixth word to the fixed vocabulary.
		return `NOT EXISTS (
			SELECT 1 FROM media_file f
			WHERE f.media_id = m.id AND f.missing_at_ms IS NULL
		) OR EXISTS (
			SELECT 1 FROM media_file f
			WHERE f.media_id = m.id AND f.missing_at_ms IS NULL AND f.decode_broken = 1
		)`, nil
	case HealthScrapeFailure:
		// Unidentified AND an automatic scrape ran and queued it as unmatched
		// (below threshold or no candidates). Manual-review queueing and Media
		// never scraped are not failures.
		identified, args := identifiedPredicate(p)
		return `NOT (` + identified + `) AND EXISTS (
			SELECT 1 FROM unmatched_queue q
			WHERE q.media_id = m.id AND q.scraper_id <> ?
		)`, append(args, UnmatchedManualScraperID)
	case HealthDuplicate:
		// Another distinct, non-Extra Media has an available file sharing this
		// Media's non-empty oshash — byte-identical content under two catalog
		// rows. An Extra partner (trailer/BTS) is excluded by default
		//, so it never makes a real Media a duplicate candidate;
		// the outer callers also exclude Extras from the m alias.
		return `EXISTS (
			SELECT 1
			FROM media_file f
			JOIN media_file g ON g.oshash = f.oshash AND g.media_id <> f.media_id
			JOIN media gm ON gm.id = g.media_id AND gm.extra = 0 AND gm.trashed_at_ms IS NULL
			WHERE f.media_id = m.id
			  AND f.missing_at_ms IS NULL AND g.missing_at_ms IS NULL
			  AND f.oshash <> ''
		)`, nil
	}
	return `0`, nil
}

// healthLibraryLocked returns the visible Library named by libraryID.
func (s *Store) healthLibraryLocked(ctx context.Context, libraryID LibraryID, filter LibraryFilter) (Library, bool, error) {
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return Library{}, false, err
	}
	for _, library := range libraries {
		if library.ID == libraryID && libraryVisible(library, filter) {
			return library, true, nil
		}
	}
	return Library{}, false, nil
}

// LibraryHealthApplies reports whether a dimension applies to one visible
// Library. An invisible or unknown Library answers false.
func (s *Store) LibraryHealthApplies(ctx context.Context, libraryID LibraryID, dim HealthDimension, filter LibraryFilter) (bool, error) {
	if !validHealthDimension(dim) {
		return false, ValidationError("unknown health dimension")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	library, visible, err := s.healthLibraryLocked(ctx, libraryID, filter)
	if err != nil || !visible {
		return false, err
	}
	return healthDimensionApplies(s.healthProfileLocked(library), dim), nil
}

// LibraryHealthCounts returns the per-dimension counts for every Library the
// caller can see, ordered by Library ID. The aggregate summary row is the
// element-wise sum across the returned slice (the caller owns that fold so the
// reveal-filtered Library set is never inferred from a server-side total).
//
// Counts are served from the library_health_cache table when a cached row
// exists for a Library (refreshed by RefreshLibraryHealthCache after each
// scan). A Library with no cache row yet — pre-first-scan or post-migration —
// falls back to live counts so the dashboard is always correct. Only the
// dimensions that apply to the Library are reported.
func (s *Store) LibraryHealthCounts(ctx context.Context, filter LibraryFilter) ([]LibraryHealth, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	// Load all cached rows once — avoids per-Library per-dimension queries when
	// the cache is warm. Keyed by (libraryID, dimension).
	type cacheKey struct {
		libraryID LibraryID
		dim       HealthDimension
	}
	cached := map[cacheKey]int{}
	cacheRows, err := s.catalogR.QueryContext(ctx, `
		SELECT library_id, dimension, count FROM library_health_cache
	`)
	if err != nil {
		return nil, err
	}
	func() {
		defer cacheRows.Close()
		for cacheRows.Next() {
			var libID, dim string
			var n int
			if scanErr := cacheRows.Scan(&libID, &dim, &n); scanErr == nil {
				cached[cacheKey{LibraryID(libID), HealthDimension(dim)}] = n
			}
		}
	}()

	out := make([]LibraryHealth, 0, len(libraries))
	for _, library := range libraries {
		if !libraryVisible(library, filter) {
			continue
		}
		profile := s.healthProfileLocked(library)
		counts := make(map[HealthDimension]int, len(HealthDimensions))
		for _, dim := range HealthDimensions {
			if !healthDimensionApplies(profile, dim) {
				continue
			}
			if n, ok := cached[cacheKey{library.ID, dim}]; ok {
				counts[dim] = n
				continue
			}
			// Cache miss: fall back to a live count for this dimension.
			n, err := s.dimensionCountLocked(ctx, library.ID, profile, dim)
			if err != nil {
				return nil, err
			}
			counts[dim] = n
		}
		out = append(out, LibraryHealth{
			LibraryID: library.ID,
			Title:     library.Title,
			Counts:    counts,
		})
	}
	return out, nil
}

func (s *Store) dimensionCountLocked(ctx context.Context, libraryID LibraryID, profile LibraryHealthProfile, dim HealthDimension) (int, error) {
	if dim == HealthMissingArtwork {
		items, err := s.healthMissingArtworkLocked(ctx, libraryID, profile, 0)
		return len(items), err
	}
	pred, args := dimensionPredicate(profile, dim)
	var n int
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM media m
		WHERE m.library_id = ? AND m.trashed_at_ms IS NULL`+mediaExcludeExtrasClause+` AND (`+pred+`)
	`, append([]any{libraryID}, args...)...).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// healthItemsLocked lists the live Media in one Library exhibiting an applicable
// dimension, ordered by Media ID. limit <= 0 is uncapped.
func (s *Store) healthItemsLocked(ctx context.Context, libraryID LibraryID, profile LibraryHealthProfile, dim HealthDimension, limit int) ([]HealthItem, error) {
	if !healthDimensionApplies(profile, dim) {
		return []HealthItem{}, nil
	}
	if dim == HealthMissingArtwork {
		return s.healthMissingArtworkLocked(ctx, libraryID, profile, limit)
	}
	pred, args := dimensionPredicate(profile, dim)
	if limit <= 0 {
		limit = -1 // SQLite: a negative LIMIT is uncapped.
	}
	args = append(append([]any{libraryID}, args...), limit)
	// #nosec G202 -- the predicate is a closed SQL fragment from dimensionPredicate; every value is bound.
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT m.id, m.library_id, m.title FROM media m
		WHERE m.library_id = ? AND m.trashed_at_ms IS NULL`+mediaExcludeExtrasClause+` AND (`+pred+`)
		ORDER BY m.id
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []HealthItem{}
	for rows.Next() {
		var item HealthItem
		if err := rows.Scan(&item.MediaID, &item.LibraryID, &item.Title); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// healthMissingArtworkLocked lists the Media whose archetype's artwork slots
// hold neither an applied candidate nor (through the installed probe) stored,
// non-generated artwork. The archetype is the profile's class field when set,
// else the stored media class. limit <= 0 is uncapped.
func (s *Store) healthMissingArtworkLocked(ctx context.Context, libraryID LibraryID, profile LibraryHealthProfile, limit int) ([]HealthItem, error) {
	archetype := `m.media_class`
	args := []any{}
	if profile.ClassField != "" {
		archetype = `COALESCE((
			SELECT t.value FROM media_field_text t WHERE t.media_id = m.id AND t.field = ?
		), m.media_class)`
		args = append(args, profile.ClassField)
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT m.id, m.library_id, m.title, `+archetype+`, (
			SELECT group_concat(a.slot, ',') FROM artwork_candidate a
			WHERE a.media_id = m.id AND a.applied = 1
		)
		FROM media m
		WHERE m.library_id = ? AND m.trashed_at_ms IS NULL`+mediaExcludeExtrasClause+`
		ORDER BY m.id
	`, append(args, libraryID)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []HealthItem{}
	for rows.Next() {
		var item HealthItem
		var class string
		var applied sql.NullString
		if err := rows.Scan(&item.MediaID, &item.LibraryID, &item.Title, &class, &applied); err != nil {
			return nil, err
		}
		if s.hasHealthArtwork(item.MediaID, profile.artworkSlotsFor(class), applied.String) {
			continue
		}
		items = append(items, item)
		if limit > 0 && len(items) >= limit {
			break
		}
	}
	return items, rows.Err()
}

// hasHealthArtwork reports whether any accepted slot has an applied candidate
// (appliedSlots is a comma-joined list) or stored artwork the probe accepts.
func (s *Store) hasHealthArtwork(id MediaID, slots []string, appliedSlots string) bool {
	applied := map[string]bool{}
	for _, slot := range strings.Split(appliedSlots, ",") {
		applied[slot] = true
	}
	for _, slot := range slots {
		if applied[slot] {
			return true
		}
		if s.healthArtworkProbe != nil && s.healthArtworkProbe(id, slot) {
			return true
		}
	}
	return false
}

// RefreshLibraryHealthCache pre-computes and persists the per-Library,
// per-dimension health counts. Call this after a scan completes so
// that subsequent LibraryHealthCounts calls read a single cached row per
// Library rather than running live counts per Library.
//
// The refresh is a best-effort write: it runs every applicable dimension for
// every Library, upserts the result into library_health_cache and drops rows
// for dimensions that no longer apply. Callers tolerate a failure here — the
// dashboard falls back to live queries for any Library that lacks a cache row.
func (s *Store) RefreshLibraryHealthCache(ctx context.Context, now time.Time) error {
	nowMS := now.UTC().UnixMilli()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return err
	}
	for _, library := range libraries {
		profile := s.healthProfileLocked(library)
		for _, dim := range HealthDimensions {
			if !healthDimensionApplies(profile, dim) {
				if _, err := s.catalogW.ExecContext(ctx, `
					DELETE FROM library_health_cache WHERE library_id = ? AND dimension = ?
				`, library.ID, string(dim)); err != nil {
					return err
				}
				continue
			}
			n, err := s.dimensionCountLocked(ctx, library.ID, profile, dim)
			if err != nil {
				return err
			}
			if _, err := s.catalogW.ExecContext(ctx, `
				INSERT INTO library_health_cache (library_id, dimension, count, refreshed_at_ms)
				VALUES (?, ?, ?, ?)
				ON CONFLICT (library_id, dimension) DO UPDATE
				  SET count = excluded.count, refreshed_at_ms = excluded.refreshed_at_ms
			`, library.ID, string(dim), n, nowMS); err != nil {
				return err
			}
		}
	}
	return nil
}

// LibraryHealthItems lists the live Media in one Library exhibiting a dimension,
// ordered by Media ID, capped by limit (<=0 falls back to 200). The Library
// must be visible to the caller; an invisible or unknown Library, or a
// dimension that does not apply to it, yields an empty list, never an
// existence oracle.
func (s *Store) LibraryHealthItems(ctx context.Context, libraryID LibraryID, dim HealthDimension, filter LibraryFilter, limit int) ([]HealthItem, error) {
	if !validHealthDimension(dim) {
		return nil, ValidationError("unknown health dimension")
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	library, visible, err := s.healthLibraryLocked(ctx, libraryID, filter)
	if err != nil {
		return nil, err
	}
	if !visible {
		return []HealthItem{}, nil
	}
	return s.healthItemsLocked(ctx, libraryID, s.healthProfileLocked(library), dim, limit)
}

// LibraryHealthBulkItems lists every live Media in one Library exhibiting a
// dimension for server-side summary remediation. Unlike LibraryHealthItems, it
// is intentionally uncapped; UI drilldowns keep the launch cap.
func (s *Store) LibraryHealthBulkItems(ctx context.Context, libraryID LibraryID, dim HealthDimension, filter LibraryFilter) ([]HealthItem, error) {
	if !validHealthDimension(dim) {
		return nil, ValidationError("unknown health dimension")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	library, visible, err := s.healthLibraryLocked(ctx, libraryID, filter)
	if err != nil {
		return nil, err
	}
	if !visible {
		return []HealthItem{}, nil
	}
	return s.healthItemsLocked(ctx, libraryID, s.healthProfileLocked(library), dim, 0)
}

func validHealthDimension(dim HealthDimension) bool {
	for _, d := range HealthDimensions {
		if d == dim {
			return true
		}
	}
	return false
}

// HealthBulkTrash soft-deletes every live Media exhibiting a dimension in one
// visible Library except IDs explicitly spared by keep, routing through the
// audited Trash path (invariant #6 — never a hard delete). It is the engine
// behind bulk-trash-broken. Duplicate cluster merges use MergeApply so they stay
// scoped to one content-fingerprint cluster and remain undo-batched.
//
// keep is the set of Media IDs to spare (the canonical survivors of a merge);
// pass nil to trash the whole matching set (bulk-trash-broken).
//
// Like TrashMedia (see the trashDisabled-asymmetry note there; JBF-2), this
// bulk path is intentionally NOT gated on s.trashDisabled: it is invoked only
// through admin-gated, human-confirmed Console mutations, so it is the
// "human confirmation" branch of invariant 6 rather than autonomous
// destruction. Only the unattended retention sweep yields when trash is off.
func (s *Store) HealthBulkTrash(ctx context.Context, libraryID LibraryID, dim HealthDimension, filter LibraryFilter, keep map[MediaID]bool) (int, error) {
	items, err := s.LibraryHealthItems(ctx, libraryID, dim, filter, 500)
	if err != nil {
		return 0, err
	}
	trashed := 0
	for _, item := range items {
		if keep[item.MediaID] {
			continue
		}
		if err := s.TrashMedia(ctx, item.MediaID, filter); err != nil {
			return trashed, err
		}
		trashed++
	}
	return trashed, nil
}

// HealthDuplicateGroups returns the duplicate clusters in one visible Library:
// each group is the set of live Media sharing one non-empty oshash, ordered so
// the lowest Media ID is the suggested canonical survivor (first element). A
// bulk-merge trashes every member except the survivor. Groups of one are never
// returned (a lone file is not a duplicate).
func (s *Store) HealthDuplicateGroups(ctx context.Context, libraryID LibraryID, filter LibraryFilter) ([][]HealthItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	visible := false
	for _, library := range libraries {
		if library.ID == libraryID && libraryVisible(library, filter) {
			visible = true
			break
		}
	}
	if !visible {
		return [][]HealthItem{}, nil
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT f.oshash, m.id, m.library_id, m.title
		FROM media m
		JOIN media_file f ON f.media_id = m.id AND f.missing_at_ms IS NULL AND f.oshash <> ''
		WHERE m.library_id = ? AND m.trashed_at_ms IS NULL`+mediaExcludeExtrasClause+`
		  AND f.oshash IN (
			SELECT g.oshash FROM media_file g
			JOIN media m2 ON m2.id = g.media_id AND m2.library_id = ? AND m2.trashed_at_ms IS NULL AND m2.extra = 0
			WHERE g.missing_at_ms IS NULL AND g.oshash <> ''
			GROUP BY g.oshash HAVING COUNT(DISTINCT g.media_id) > 1
		  )
		ORDER BY f.oshash, m.id
	`, libraryID, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := [][]HealthItem{}
	var curHash string
	var cur []HealthItem
	seen := map[MediaID]bool{}
	flush := func() {
		if len(cur) > 1 {
			groups = append(groups, cur)
		}
		cur = nil
		seen = map[MediaID]bool{}
	}
	for rows.Next() {
		var hash string
		var item HealthItem
		if err := rows.Scan(&hash, &item.MediaID, &item.LibraryID, &item.Title); err != nil {
			return nil, err
		}
		if hash != curHash {
			flush()
			curHash = hash
		}
		if seen[item.MediaID] {
			continue
		}
		seen[item.MediaID] = true
		cur = append(cur, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	flush()
	return groups, nil
}
