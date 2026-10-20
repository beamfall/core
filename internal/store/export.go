package store

import (
	"context"
	"database/sql"
	"sort"
	"strings"
)

// Full metadata export (ADR-0005 §5, roadmap FEAT-5) — the store half. This is
// the operator-gated, READ-only portability export of catalog curation: Media +
// their fields, Entities (plugin Kinds) + their fields, entity and Media→Media
// relations, Collections, Markers, and the pinned (applied) artwork choices.
//
// REVEAL-FILTERED (mandatory — the Reveal contract outranks export
// completeness): every row is gated by the SAME libraryVisible() predicate the
// rest of the read contract uses. A restricted Library's Media, entities,
// relations, collections, markers, and artwork pins appear ONLY when the
// caller's filter is Revealed. A non-revealed export omits them entirely; a
// revealed export includes them. The export never widens visibility.
//
// Unlike the browse feeds (allItemsSQL), the export does NOT exclude Extras:
// portability wants the operator's full curation, and an Extra's parent link is
// itself an exported Media→Media relation. Trashed Media are excluded — Trash is
// recoverable scaffolding, not curation (invariant #6).
//
// This is a NEW bulk read shape: the existing per-item readers are scoped to a
// single Media/Entity by a known relation name or kind, but the export must walk
// the whole reveal-filtered catalog with no such key. It reuses the shared
// visibility predicate and the field/marker/attribute decode helpers so it
// applies exactly the same Reveal and decode rules as the per-item paths.

// CatalogExport is the assembled, reveal-filtered metadata export payload.
type CatalogExport struct {
	Libraries       []Library
	Media           []ExportedMedia
	Entities        []Entity
	EntityRelations []ExportedEntityRelation
	MediaRelations  []ExportedMediaRelation
	Collections     []SmartCollection
}

// ExportedMedia is one Media plus the curation attached to it: its plugin
// fields, authored Markers, and pinned artwork (the applied candidate per slot).
type ExportedMedia struct {
	Media      Media
	Fields     []MediaField
	Markers    []Marker
	ArtworkPin []ArtworkPin
}

// ArtworkPin is the materialized (applied) artwork choice for a Media/slot pair
// — the operator's pinned candidate. SourceURL is the third-party origin the
// pin was fetched from; it is curation provenance, not a servable URL.
type ArtworkPin struct {
	Slot      string
	SourceURL string
	PluginID  string
	ScraperID string
}

// ExportedEntityRelation is one entity_relation row in export shape: the source
// Entity, the relation name, exactly one target (entity or Media), and the
// render-only attributes.
type ExportedEntityRelation struct {
	FromEntityID EntityID
	Relation     string
	ToEntityID   EntityID
	ToMediaID    MediaID
	Attributes   map[string]string
}

// ExportedMediaRelation is one media_relation row (Media→Media): the fixed-shape
// relation name and both endpoints.
type ExportedMediaRelation struct {
	FromMediaID MediaID
	Relation    string
	ToMediaID   MediaID
	Attributes  map[string]string
}

// ExportCatalog assembles the reveal-filtered metadata export for every Library
// visible under the filter. Every contained row is constrained to a visible
// Library, so a non-revealed caller never sees restricted-Library curation.
func (s *Store) ExportCatalog(ctx context.Context, filter LibraryFilter) (CatalogExport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return CatalogExport{}, err
	}

	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return CatalogExport{}, err
	}
	visible := make([]Library, 0, len(libraries))
	visibleSet := make(map[LibraryID]bool, len(libraries))
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			visible = append(visible, library)
			visibleSet[library.ID] = true
		}
	}
	sort.Slice(visible, func(i, j int) bool { return visible[i].ID < visible[j].ID })

	export := CatalogExport{
		Libraries:       visible,
		Media:           []ExportedMedia{},
		Entities:        []Entity{},
		EntityRelations: []ExportedEntityRelation{},
		MediaRelations:  []ExportedMediaRelation{},
		Collections:     []SmartCollection{},
	}
	if len(visible) == 0 {
		return export, nil
	}

	placeholders := make([]string, len(visible))
	libArgs := make([]any, len(visible))
	for i, library := range visible {
		placeholders[i] = "?"
		libArgs[i] = library.ID
	}
	inList := "(" + strings.Join(placeholders, ", ") + ")"

	// Media in visible Libraries (extras included, trashed excluded), each with
	// its fields, markers, and pinned artwork. mediaID membership backs the
	// relation filters below: a relation is exported only when BOTH endpoints
	// resolve to a visible Media here.
	mediaRows, err := s.catalogR.QueryContext(ctx, `
		SELECT id, library_id, title, summary, year, duration_seconds, container, codec, media_class,
		       video_width, video_height, COALESCE(mutated_by, '')
		FROM media
		WHERE library_id IN `+inList+` AND trashed_at_ms IS NULL
		ORDER BY library_id, id`, libArgs...)
	if err != nil {
		return CatalogExport{}, err
	}
	mediaSet := map[MediaID]bool{}
	var ordered []Media
	var mediaIDs []MediaID
	for mediaRows.Next() {
		var m Media
		var summary sql.NullString
		var year sql.NullInt64
		if err := mediaRows.Scan(&m.ID, &m.LibraryID, &m.Title, &summary, &year,
			&m.DurationSeconds, &m.Container, &m.Codec, &m.Class,
			&m.VideoWidth, &m.VideoHeight, &m.MutatedBy); err != nil {
			mediaRows.Close()
			return CatalogExport{}, err
		}
		m.Summary = summary.String
		m.Year = int(year.Int64)
		mediaSet[m.ID] = true
		ordered = append(ordered, m)
		mediaIDs = append(mediaIDs, m.ID)
	}
	if err := mediaRows.Err(); err != nil {
		mediaRows.Close()
		return CatalogExport{}, err
	}
	mediaRows.Close()

	fieldsByMedia := make(map[MediaID][]MediaField, len(mediaIDs))
	if err := s.loadExportMediaFields(ctx, mediaIDs, func(id MediaID, field MediaField) {
		fieldsByMedia[id] = append(fieldsByMedia[id], field)
	}); err != nil {
		return CatalogExport{}, err
	}
	markersByMedia, err := s.loadExportMarkers(ctx, mediaIDs)
	if err != nil {
		return CatalogExport{}, err
	}
	pinsByMedia, err := s.loadExportArtworkPins(ctx, mediaIDs)
	if err != nil {
		return CatalogExport{}, err
	}

	for _, m := range ordered {
		fields := fieldsByMedia[m.ID]
		if fields == nil {
			fields = []MediaField{}
		}
		sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
		markers := markersByMedia[m.ID]
		if markers == nil {
			markers = []Marker{}
		}
		pins := pinsByMedia[m.ID]
		if pins == nil {
			pins = []ArtworkPin{}
		}
		export.Media = append(export.Media, ExportedMedia{
			Media:      m,
			Fields:     fields,
			Markers:    markers,
			ArtworkPin: pins,
		})
	}

	// Entities in visible Libraries, with their fields. Entity IDs back the
	// entity-relation filter below.
	entityRows, err := s.catalogR.QueryContext(ctx, `
		SELECT id, library_id, kind, title
		FROM entity
		WHERE library_id IN `+inList+`
		ORDER BY library_id, kind, id`, libArgs...)
	if err != nil {
		return CatalogExport{}, err
	}
	entitySet := map[EntityID]bool{}
	index := map[EntityID]int{}
	var ids []EntityID
	for entityRows.Next() {
		var e Entity
		if err := entityRows.Scan(&e.ID, &e.LibraryID, &e.Kind, &e.Title); err != nil {
			entityRows.Close()
			return CatalogExport{}, err
		}
		e.Fields = []EntityField{}
		entitySet[e.ID] = true
		index[e.ID] = len(export.Entities)
		ids = append(ids, e.ID)
		export.Entities = append(export.Entities, e)
	}
	if err := entityRows.Err(); err != nil {
		entityRows.Close()
		return CatalogExport{}, err
	}
	entityRows.Close()
	if err := s.loadEntityFields(ctx, ids, func(id EntityID, field EntityField) {
		export.Entities[index[id]].Fields = append(export.Entities[index[id]].Fields, field)
	}); err != nil {
		return CatalogExport{}, err
	}
	for i := range export.Entities {
		sortEntityFields(export.Entities[i].Fields)
	}

	// Entity relations whose SOURCE entity is visible. A relation is exported
	// only when its target is also visible: an entity→entity row needs both
	// entities in the visible set, an entity→Media row needs the Media in the
	// visible set. This keeps a restricted target from leaking through a visible
	// source's relation list.
	relRows, err := s.catalogR.QueryContext(ctx, `
		SELECT r.from_entity_id, r.relation, COALESCE(r.to_entity_id, ''),
		       COALESCE(r.to_media_id, ''), r.attributes
		FROM entity_relation r
		JOIN entity e ON e.id = r.from_entity_id
		WHERE e.library_id IN `+inList+`
		ORDER BY r.from_entity_id, r.relation, r.to_entity_id, r.to_media_id`, libArgs...)
	if err != nil {
		return CatalogExport{}, err
	}
	for relRows.Next() {
		var rel ExportedEntityRelation
		var attrs sql.NullString
		if err := relRows.Scan(&rel.FromEntityID, &rel.Relation, &rel.ToEntityID, &rel.ToMediaID, &attrs); err != nil {
			relRows.Close()
			return CatalogExport{}, err
		}
		if !entitySet[rel.FromEntityID] {
			continue
		}
		if rel.ToEntityID != "" && !entitySet[rel.ToEntityID] {
			continue
		}
		if rel.ToMediaID != "" && !mediaSet[rel.ToMediaID] {
			continue
		}
		if rel.Attributes, err = decodeRelationAttributes(attrs); err != nil {
			relRows.Close()
			return CatalogExport{}, err
		}
		export.EntityRelations = append(export.EntityRelations, rel)
	}
	if err := relRows.Err(); err != nil {
		relRows.Close()
		return CatalogExport{}, err
	}
	relRows.Close()

	// Media→Media relations where BOTH endpoints are visible Media.
	mediaRelRows, err := s.catalogR.QueryContext(ctx, `
		SELECT mr.from_media_id, mr.relation, mr.to_media_id, mr.attributes
		FROM media_relation mr
		JOIN media fm ON fm.id = mr.from_media_id
		WHERE fm.library_id IN `+inList+` AND fm.trashed_at_ms IS NULL
		ORDER BY mr.from_media_id, mr.relation, mr.to_media_id`, libArgs...)
	if err != nil {
		return CatalogExport{}, err
	}
	for mediaRelRows.Next() {
		var rel ExportedMediaRelation
		var attrs sql.NullString
		if err := mediaRelRows.Scan(&rel.FromMediaID, &rel.Relation, &rel.ToMediaID, &attrs); err != nil {
			mediaRelRows.Close()
			return CatalogExport{}, err
		}
		if !mediaSet[rel.FromMediaID] || !mediaSet[rel.ToMediaID] {
			continue
		}
		if rel.Attributes, err = decodeRelationAttributes(attrs); err != nil {
			mediaRelRows.Close()
			return CatalogExport{}, err
		}
		export.MediaRelations = append(export.MediaRelations, rel)
	}
	if err := mediaRelRows.Err(); err != nil {
		mediaRelRows.Close()
		return CatalogExport{}, err
	}
	mediaRelRows.Close()

	// Smart Collections in visible Libraries.
	collectionRows, err := s.catalogR.QueryContext(ctx, `
		SELECT id, library_id, name, source_expr, created_at_ms
		FROM smart_collection
		WHERE library_id IN `+inList+`
		ORDER BY library_id, created_at_ms, id`, libArgs...)
	if err != nil {
		return CatalogExport{}, err
	}
	for collectionRows.Next() {
		var c SmartCollection
		var createdAtMS int64
		if err := collectionRows.Scan(&c.ID, &c.LibraryID, &c.Name, &c.SourceExpr, &createdAtMS); err != nil {
			collectionRows.Close()
			return CatalogExport{}, err
		}
		c.CreatedAt = millisToTime(createdAtMS)
		export.Collections = append(export.Collections, c)
	}
	if err := collectionRows.Err(); err != nil {
		collectionRows.Close()
		return CatalogExport{}, err
	}
	collectionRows.Close()

	return export, nil
}

// Catalog export curation loads use the same 500-ID chunk size as the other
// bulk Media readers. Each chunk stays far below SQLite's bind-variable cap.
const catalogExportMediaBatchSize = 500

func (s *Store) loadExportMediaFields(ctx context.Context, ids []MediaID, assign func(MediaID, MediaField)) error {
	for _, batch := range mediaIDBatches(ids, catalogExportMediaBatchSize) {
		placeholders, args := mediaIDPlaceholders(batch)
		for _, table := range mediaFieldTables {
			column := "value"
			if table == "media_field_date" {
				column = "value_ms"
			}
			rows, err := s.catalogR.QueryContext(ctx,
				`SELECT media_id, field, field_type, `+column+` FROM `+table+
					` WHERE media_id IN (`+placeholders+`)`, args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id MediaID
				var field MediaField
				switch table {
				case "media_field_text":
					err = rows.Scan(&id, &field.Name, &field.Type, &field.Text)
				case "media_field_real":
					err = rows.Scan(&id, &field.Name, &field.Type, &field.Number)
				case "media_field_int":
					var value int64
					if err = rows.Scan(&id, &field.Name, &field.Type, &value); err == nil {
						field.Flag = value != 0
					}
				case "media_field_date":
					var millis int64
					if err = rows.Scan(&id, &field.Name, &field.Type, &millis); err == nil {
						field.Date = millisToTime(millis)
					}
				}
				if err != nil {
					rows.Close()
					return err
				}
				assign(id, field)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
		}
	}
	return nil
}

// loadExportMarkers loads Marker rows and their tags in two queries per Media
// batch. Visibility is already enforced by ExportCatalog's Media set.
func (s *Store) loadExportMarkers(ctx context.Context, ids []MediaID) (map[MediaID][]Marker, error) {
	markersByMedia := make(map[MediaID][]Marker, len(ids))
	type markerPosition struct {
		mediaID MediaID
		index   int
	}
	for _, batch := range mediaIDBatches(ids, catalogExportMediaBatchSize) {
		positions := make(map[MarkerID]markerPosition)
		placeholders, args := mediaIDPlaceholders(batch)
		rows, err := s.catalogR.QueryContext(ctx,
			markerColumns+` WHERE media_id IN (`+placeholders+`) ORDER BY media_id, start_seconds, id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			marker, err := scanMarkerRow(rows.Scan)
			if err != nil {
				rows.Close()
				return nil, err
			}
			marker.Tags = []string{}
			position := markerPosition{mediaID: marker.MediaID, index: len(markersByMedia[marker.MediaID])}
			positions[marker.ID] = position
			markersByMedia[marker.MediaID] = append(markersByMedia[marker.MediaID], marker)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()

		tagRows, err := s.catalogR.QueryContext(ctx, `
			SELECT mt.marker_id, mt.tag
			FROM marker_tag mt
			JOIN marker m ON m.id = mt.marker_id
			WHERE m.media_id IN (`+placeholders+`)
			ORDER BY mt.marker_id, mt.tag`, args...)
		if err != nil {
			return nil, err
		}
		for tagRows.Next() {
			var markerID MarkerID
			var tag string
			if err := tagRows.Scan(&markerID, &tag); err != nil {
				tagRows.Close()
				return nil, err
			}
			position, ok := positions[markerID]
			if !ok {
				continue
			}
			markers := markersByMedia[position.mediaID]
			markers[position.index].Tags = append(markers[position.index].Tags, tag)
			markersByMedia[position.mediaID] = markers
		}
		if err := tagRows.Err(); err != nil {
			tagRows.Close()
			return nil, err
		}
		tagRows.Close()
	}
	return markersByMedia, nil
}

// loadExportArtworkPins loads only applied candidates: the operator's
// materialized artwork choice per Media/slot.
func (s *Store) loadExportArtworkPins(ctx context.Context, ids []MediaID) (map[MediaID][]ArtworkPin, error) {
	pinsByMedia := make(map[MediaID][]ArtworkPin, len(ids))
	for _, batch := range mediaIDBatches(ids, catalogExportMediaBatchSize) {
		placeholders, args := mediaIDPlaceholders(batch)
		rows, err := s.catalogR.QueryContext(ctx, `
			SELECT media_id, slot, source_url, plugin_id, scraper_id
			FROM artwork_candidate
			WHERE media_id IN (`+placeholders+`) AND applied = 1
			ORDER BY media_id, slot`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id MediaID
			var pin ArtworkPin
			if err := rows.Scan(&id, &pin.Slot, &pin.SourceURL, &pin.PluginID, &pin.ScraperID); err != nil {
				rows.Close()
				return nil, err
			}
			pinsByMedia[id] = append(pinsByMedia[id], pin)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return pinsByMedia, nil
}
