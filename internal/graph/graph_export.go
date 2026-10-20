package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
)

// Full metadata JSON export (ADR-0005 §5, roadmap FEAT-5), graph half. This is
// the operator-gated, READ-only portability export: the operator's catalog
// curation — Media + plugin fields, Entities (plugin Kinds) + fields, entity
// and Media→Media relations, Collections, Markers, and pinned artwork — as a
// self-describing JSON document the operator can save. Dispatch is registered as
// an Admin-Session-gated field in graph.go's execute() (the same gate as the
// Library Health group); the resolver lives here to keep that chokepoint edit
// localized.
//
// REVEAL-FILTERED (mandatory — the Reveal contract outranks export
// completeness): every row passes through scope.LibraryFilter() at the store
// boundary, exactly like the rest of the read contract. A non-revealed export
// OMITS restricted-Library content; a revealed export includes it. The surface
// shape is identical either way — the only difference is what the caller's
// current reveal state lets the store return.
//
// INVARIANT #1 (domain-agnostic): every key here is generic — Media, entity
// Kind, relation, collection, marker, artwork pin. No domain vocabulary appears
// in the JSON. INVARIANT #7: this is local operator portability; nothing is sent
// anywhere.

// metadataExportSchema is the export payload's schema discriminant, the sibling
// of myEngagementSchema — it lets an operator (or a future importer) recognize
// the shape.
const metadataExportSchema = "beamfall.metadata.v1"

// MetadataExportInfo is the self-describing full metadata export.
type MetadataExportInfo struct {
	Schema      string                     `json:"schema"`
	ExportedAt  string                     `json:"exportedAt"`
	Libraries   []ExportLibraryInfo        `json:"libraries"`
	Media       []ExportMediaInfo          `json:"media"`
	Entities    []ExportEntityInfo         `json:"entities"`
	Relations   []ExportEntityRelationInfo `json:"relations"`
	MediaLinks  []ExportMediaRelationInfo  `json:"mediaLinks"`
	Collections []ExportCollectionInfo     `json:"collections"`
}

// ExportLibraryInfo is one exported Library: identity, title, owning plugin, and
// whether it is restricted (so a re-import knows the confidentiality posture).
type ExportLibraryInfo struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	PluginID   string `json:"pluginID,omitempty"`
	Restricted bool   `json:"restricted"`
}

// ExportMediaInfo is one exported Media with the curation attached to it:
// descriptive metadata, plugin fields, authored Markers, and pinned artwork.
type ExportMediaInfo struct {
	ID              string              `json:"id"`
	LibraryID       string              `json:"libraryID"`
	Title           string              `json:"title"`
	Summary         string              `json:"summary,omitempty"`
	Year            int                 `json:"year,omitempty"`
	DurationSeconds int                 `json:"durationSeconds"`
	Container       string              `json:"container,omitempty"`
	Codec           string              `json:"codec,omitempty"`
	Class           string              `json:"class"`
	MutatedBy       string              `json:"mutatedBy,omitempty"`
	Fields          []EntityFieldInfo   `json:"fields"`
	Markers         []ExportMarkerInfo  `json:"markers"`
	ArtworkPins     []ExportArtworkInfo `json:"artworkPins"`
}

// ExportMarkerInfo is one exported authored Marker.
type ExportMarkerInfo struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	StartSeconds float64  `json:"startSeconds"`
	EndSeconds   *float64 `json:"endSeconds,omitempty"`
	SegmentKind  string   `json:"segmentKind,omitempty"`
	Tags         []string `json:"tags"`
}

// ExportArtworkInfo is one pinned (applied) artwork choice: the slot and the
// third-party origin URL the pin was fetched from (curation provenance — never
// a servable URL).
type ExportArtworkInfo struct {
	Slot      string `json:"slot"`
	SourceURL string `json:"sourceURL,omitempty"`
	PluginID  string `json:"pluginID,omitempty"`
	ScraperID string `json:"scraperID,omitempty"`
}

// ExportEntityInfo is one exported Entity instance (plugin Kind), with its
// stringified field values.
type ExportEntityInfo struct {
	ID        string            `json:"id"`
	LibraryID string            `json:"libraryID"`
	Kind      string            `json:"kind"`
	Title     string            `json:"title"`
	Fields    []EntityFieldInfo `json:"fields"`
}

// ExportEntityRelationInfo is one exported entity_relation row: a source Entity,
// the relation name, exactly one target (entity or Media), and the render-only
// attributes.
type ExportEntityRelationInfo struct {
	FromEntityID string            `json:"fromEntityID"`
	Relation     string            `json:"relation"`
	ToEntityID   string            `json:"toEntityID,omitempty"`
	ToMediaID    string            `json:"toMediaID,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
}

// ExportMediaRelationInfo is one exported Media→Media relation (ADR-0009 fixed
// shape: edition-of / extra-of).
type ExportMediaRelationInfo struct {
	FromMediaID string            `json:"fromMediaID"`
	Relation    string            `json:"relation"`
	ToMediaID   string            `json:"toMediaID"`
	Attributes  map[string]string `json:"attributes,omitempty"`
}

// ExportCollectionInfo is one exported Collection (Smart Collection): name plus
// the opaque membership Source expression, stored and returned verbatim.
type ExportCollectionInfo struct {
	ID         string `json:"id"`
	LibraryID  string `json:"libraryID"`
	Name       string `json:"name"`
	SourceExpr string `json:"sourceExpr"`
	CreatedAt  string `json:"createdAt"`
}

// metadataExport returns the caller's reveal-filtered full metadata export. The
// reveal filter is the caller's CURRENT scope (scope.LibraryFilter()): a
// restricted Library's content appears only while the session is revealed.
func (h *Handler) metadataExport(ctx context.Context, scope auth.Scope) (MetadataExportInfo, error) {
	if h.store == nil {
		return MetadataExportInfo{}, clientError("store unavailable")
	}
	export, err := h.store.ExportCatalog(ctx, scope.LibraryFilter())
	if err != nil {
		return MetadataExportInfo{}, err
	}
	info := MetadataExportInfo{
		Schema:      metadataExportSchema,
		ExportedAt:  time.Now().UTC().Format(time.RFC3339),
		Libraries:   make([]ExportLibraryInfo, 0, len(export.Libraries)),
		Media:       make([]ExportMediaInfo, 0, len(export.Media)),
		Entities:    make([]ExportEntityInfo, 0, len(export.Entities)),
		Relations:   make([]ExportEntityRelationInfo, 0, len(export.EntityRelations)),
		MediaLinks:  make([]ExportMediaRelationInfo, 0, len(export.MediaRelations)),
		Collections: make([]ExportCollectionInfo, 0, len(export.Collections)),
	}
	for _, lib := range export.Libraries {
		pluginID := ""
		if lib.PluginID != nil {
			pluginID = *lib.PluginID
		}
		info.Libraries = append(info.Libraries, ExportLibraryInfo{
			ID:         string(lib.ID),
			Title:      lib.Title,
			PluginID:   pluginID,
			Restricted: lib.IsRestricted,
		})
	}
	for _, m := range export.Media {
		mi := ExportMediaInfo{
			ID:              string(m.Media.ID),
			LibraryID:       string(m.Media.LibraryID),
			Title:           m.Media.Title,
			Summary:         m.Media.Summary,
			Year:            m.Media.Year,
			DurationSeconds: m.Media.DurationSeconds,
			Container:       m.Media.Container,
			Codec:           m.Media.Codec,
			Class:           string(m.Media.Class),
			MutatedBy:       string(m.Media.MutatedBy),
			Fields:          make([]EntityFieldInfo, 0, len(m.Fields)),
			Markers:         make([]ExportMarkerInfo, 0, len(m.Markers)),
			ArtworkPins:     make([]ExportArtworkInfo, 0, len(m.ArtworkPin)),
		}
		for _, f := range m.Fields {
			mi.Fields = append(mi.Fields, EntityFieldInfo{
				Name:  f.Name,
				Type:  string(f.Type),
				Value: fieldValueString(f.Type, f.Text, f.Number, f.Flag, f.Date),
			})
		}
		for _, mk := range m.Markers {
			mi.Markers = append(mi.Markers, ExportMarkerInfo{
				ID:           string(mk.ID),
				Title:        mk.Title,
				StartSeconds: mk.StartSeconds,
				EndSeconds:   mk.EndSeconds,
				SegmentKind:  mk.SegmentKind,
				Tags:         markerTags(mk.Tags),
			})
		}
		for _, pin := range m.ArtworkPin {
			mi.ArtworkPins = append(mi.ArtworkPins, ExportArtworkInfo{
				Slot:      pin.Slot,
				SourceURL: pin.SourceURL,
				PluginID:  pin.PluginID,
				ScraperID: pin.ScraperID,
			})
		}
		info.Media = append(info.Media, mi)
	}
	for _, e := range export.Entities {
		ei := ExportEntityInfo{
			ID:        string(e.ID),
			LibraryID: string(e.LibraryID),
			Kind:      e.Kind,
			Title:     e.Title,
			Fields:    make([]EntityFieldInfo, 0, len(e.Fields)),
		}
		for _, f := range e.Fields {
			ei.Fields = append(ei.Fields, EntityFieldInfo{
				Name:  f.Name,
				Type:  string(f.Type),
				Value: fieldValueString(f.Type, f.Text, f.Number, f.Flag, f.Date),
			})
		}
		info.Entities = append(info.Entities, ei)
	}
	for _, r := range export.EntityRelations {
		info.Relations = append(info.Relations, ExportEntityRelationInfo{
			FromEntityID: string(r.FromEntityID),
			Relation:     r.Relation,
			ToEntityID:   string(r.ToEntityID),
			ToMediaID:    string(r.ToMediaID),
			Attributes:   r.Attributes,
		})
	}
	for _, r := range export.MediaRelations {
		info.MediaLinks = append(info.MediaLinks, ExportMediaRelationInfo{
			FromMediaID: string(r.FromMediaID),
			Relation:    r.Relation,
			ToMediaID:   string(r.ToMediaID),
			Attributes:  r.Attributes,
		})
	}
	for _, c := range export.Collections {
		info.Collections = append(info.Collections, ExportCollectionInfo{
			ID:         string(c.ID),
			LibraryID:  string(c.LibraryID),
			Name:       c.Name,
			SourceExpr: c.SourceExpr,
			CreatedAt:  c.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return info, nil
}

// markerTags normalizes a Marker's tag slice to a non-nil JSON array.
func markerTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}
