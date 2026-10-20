package scan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"

	"github.com/beamfall/core/internal/store"
)

func extractLocalMetadata(ctx context.Context, root, path string, extractors []LocalExtractor) []LocalMetadata {
	metadata := make([]LocalMetadata, 0, len(extractors))
	for _, extractor := range extractors {
		if extractor == nil {
			continue
		}
		value, err := extractor.ExtractLocal(ctx, string(store.MediaClassImage), root, path)
		if err != nil {
			slog.Debug("scan: local image metadata extraction failed", "path", path, "error", err.Error())
			continue
		}
		metadata = append(metadata, value)
	}
	return metadata
}

func applyLocalMetadata(ctx context.Context, st *store.Store, libraryID store.LibraryID, mediaID store.MediaID, metadata []LocalMetadata) error {
	for _, metadata := range metadata {
		fields := mediaFieldsFromLocalMetadata(metadata)
		if len(fields) > 0 {
			if err := st.UpsertMediaFields(ctx, mediaID, fields); err != nil {
				return err
			}
		}
		for _, entity := range metadata.Entities {
			if err := upsertLocalEntity(ctx, st, libraryID, entity, mediaID); err != nil {
				return err
			}
		}
	}
	return nil
}

func mediaFieldsFromLocalMetadata(metadata LocalMetadata) []store.MediaField {
	fields := []store.MediaField{}
	seen := map[string]bool{}
	add := func(field store.MediaField) {
		if field.Name == "" || seen[field.Name] {
			return
		}
		seen[field.Name] = true
		fields = append(fields, field)
	}
	for name, value := range metadata.ImpliedMediaFields {
		name = strings.TrimPrefix(strings.TrimSpace(name), "media.")
		if name != "" && strings.TrimSpace(value) != "" {
			add(store.MediaField{Name: name, Type: store.EntityFieldText, Text: strings.TrimSpace(value)})
		}
	}
	if raw, ok := stringLocalField(metadata.Fields, "capture_date"); ok {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil && !parsed.IsZero() {
			add(store.MediaField{Name: "capture-date", Type: store.EntityFieldDate, Date: parsed.UTC()})
		}
	}
	if value, ok := stringLocalField(metadata.Fields, "camera_make"); ok {
		add(store.MediaField{Name: "camera-make", Type: store.EntityFieldText, Text: value})
	}
	if value, ok := stringLocalField(metadata.Fields, "camera_model"); ok {
		add(store.MediaField{Name: "camera-model", Type: store.EntityFieldText, Text: value})
	}
	if value, ok := numberLocalField(metadata.Fields, "width"); ok {
		add(store.MediaField{Name: "width", Type: store.EntityFieldNumber, Number: value})
	}
	if value, ok := numberLocalField(metadata.Fields, "height"); ok {
		add(store.MediaField{Name: "height", Type: store.EntityFieldNumber, Number: value})
	}
	if value, ok := numberLocalField(metadata.Fields, "orientation"); ok {
		add(store.MediaField{Name: "orientation", Type: store.EntityFieldNumber, Number: value})
	}
	if value, ok := stringLocalField(metadata.Fields, "place"); ok {
		add(store.MediaField{Name: "place", Type: store.EntityFieldText, Text: value})
	}
	return fields
}

func mediaFieldsFromParsedText(fields []ParsedTextField) []store.MediaField {
	out := []store.MediaField{}
	seen := map[string]bool{}
	for _, field := range fields {
		name := strings.TrimSpace(field.Name)
		text := strings.TrimSpace(field.Text)
		if name == "" || text == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, store.MediaField{Name: name, Type: store.EntityFieldText, Text: text})
	}
	return out
}

func mediaFieldsFromParsedTyped(fields []ParsedField) []store.MediaField {
	out := []store.MediaField{}
	seen := map[string]bool{}
	for _, field := range fields {
		name := strings.TrimSpace(field.Name)
		if name == "" || seen[name] {
			continue
		}
		if !field.Date.IsZero() {
			seen[name] = true
			out = append(out, store.MediaField{Name: name, Type: store.EntityFieldDate, Date: field.Date.UTC()})
			continue
		}
		text := strings.TrimSpace(field.Text)
		if text == "" {
			continue
		}
		seen[name] = true
		out = append(out, store.MediaField{Name: name, Type: store.EntityFieldText, Text: text})
	}
	return out
}

func parserMediaFields(parser FilenameParser, path string, probe ParseProbe) []store.MediaField {
	if fieldParser, ok := parser.(TypedMediaFieldParser); ok {
		return mediaFieldsFromParsedTyped(fieldParser.ParseTypedMediaFields(path, probe))
	}
	fieldParser, ok := parser.(MediaFieldParser)
	if !ok {
		return nil
	}
	return mediaFieldsFromParsedText(fieldParser.ParseMediaFields(path, probe))
}

func stringLocalField(fields map[string]any, key string) (string, bool) {
	if fields == nil {
		return "", false
	}
	value, ok := fields[key]
	if !ok {
		return "", false
	}
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return "", false
		}
		return strings.TrimSpace(v), true
	default:
		return "", false
	}
}

func numberLocalField(fields map[string]any, key string) (float64, bool) {
	if fields == nil {
		return 0, false
	}
	value, ok := fields[key]
	if !ok {
		return 0, false
	}
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case float32:
		return float64(v), true
	default:
		return 0, false
	}
}

func upsertLocalEntity(ctx context.Context, st *store.Store, libraryID store.LibraryID, entity LocalEntity, mediaID store.MediaID) error {
	kind := strings.TrimSpace(entity.Kind)
	title := strings.TrimSpace(entity.Title)
	relation := strings.TrimSpace(entity.Relation)
	if kind == "" || title == "" || relation == "" {
		return nil
	}
	id := entityIDForName(libraryID, kind, title)
	if err := st.UpsertEntity(ctx, store.Entity{
		ID:        id,
		LibraryID: libraryID,
		Kind:      kind,
		Title:     title,
	}); err != nil {
		return err
	}
	return st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: id,
		Name:         relation,
		ToMediaID:    mediaID,
		Cardinality:  entity.Cardinality,
		SortOrder:    entity.SortOrder,
	})
}

func entityIDForName(library store.LibraryID, kind, name string) store.EntityID {
	sum := sha256.Sum256([]byte(string(library) + "\x00" + kind + "\x00" + name))
	return store.EntityID("entity-" + hex.EncodeToString(sum[:16]))
}
