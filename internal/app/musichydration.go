package app

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/plugin"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
)

// hydrationStore is the write slice used by compiled hydration contributions.
type hydrationStore interface {
	InsertMediaFieldsIfAbsent(ctx context.Context, id store.MediaID, fields []store.MediaField) error
	UpsertEntity(ctx context.Context, entity store.Entity) error
	UpsertEntityRelation(ctx context.Context, relation store.EntityRelation) error
	AddMediaExternalID(ctx context.Context, mediaID store.MediaID, scheme string, value string) error
}

// hydrationAdapter validates a plugin-supplied write plan against the active
// bundle before adapting it to scan's local-metadata seam.
type hydrationAdapter struct {
	store      hydrationStore
	mapper     HydrationMapper
	capability scanpkg.MetadataHydrationCapability

	declaredMediaFields   map[string]string
	declaredEntityFields  map[string]map[string]string
	declaredRelationSpecs map[string]plugin.RelationSpec
	declaredExternalIDs   map[string]bool
}

func newHydrationAdapters(registry *plugin.Registry, st hydrationStore, mappers map[string]HydrationMapper) map[string]scanpkg.MetadataHydrator {
	if registry == nil {
		return nil
	}
	if st == nil {
		return nil
	}
	out := map[string]scanpkg.MetadataHydrator{}
	for pluginID, mapper := range mappers {
		if mapper == nil {
			continue
		}
		bundle, ok := registry.Bundle(plugin.ID(pluginID))
		if !ok {
			continue
		}
		out[pluginID] = &hydrationAdapter{
			store:                 st,
			mapper:                mapper,
			capability:            metadataHydrationCapability(bundle, mapper.MarkerField()),
			declaredMediaFields:   bundle.MediaFieldTypes(),
			declaredEntityFields:  declaredEntityFieldTypes(bundle),
			declaredRelationSpecs: declaredRelationSpecs(bundle),
			declaredExternalIDs:   declaredExternalIDSet(mapper.ExternalIDSchemes()),
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func metadataHydrationCapability(bundle plugin.Bundle, markerField string) scanpkg.MetadataHydrationCapability {
	classes := make([]store.MediaClass, 0, len(bundle.MediaClasses))
	for _, class := range bundle.MediaClasses {
		classes = append(classes, store.MediaClass(class))
	}
	return scanpkg.MetadataHydrationCapability{
		MediaClasses: classes,
		MarkerField:  markerField,
	}
}

func declaredEntityFieldTypes(bundle plugin.Bundle) map[string]map[string]string {
	types := make(map[string]map[string]string, len(bundle.EntityKinds))
	for _, kind := range bundle.EntityKinds {
		types[kind.Name] = bundle.EntityFieldTypes(kind.Name)
	}
	return types
}

func declaredExternalIDSet(schemes []string) map[string]bool {
	set := make(map[string]bool, len(schemes))
	for _, scheme := range schemes {
		if scheme != "" {
			set[scheme] = true
		}
	}
	return set
}

func declaredRelationSpecs(bundle plugin.Bundle) map[string]plugin.RelationSpec {
	specs := map[string]plugin.RelationSpec{}
	for _, kind := range bundle.EntityKinds {
		for _, rel := range kind.Relations {
			specs[kind.Name+"\x00"+rel.Name] = rel
		}
	}
	return specs
}

func (a *hydrationAdapter) Capability() scanpkg.MetadataHydrationCapability {
	capability := a.capability
	capability.MediaClasses = append([]store.MediaClass(nil), capability.MediaClasses...)
	return capability
}

func (a *hydrationAdapter) HydrateMetadata(ctx context.Context, input scanpkg.MetadataHydrationInput) error {
	if !a.capability.Supports(input.Class) {
		return nil
	}
	plan := a.mapper.MapHydration(input.Metadata, input.Path)
	if plan.Empty() {
		return nil
	}
	marker := a.capability.MarkerField
	validated, err := a.validatePlan(plan, marker)
	if err != nil {
		return err
	}
	if validated.Empty() {
		return nil
	}
	if err := a.writeMediaFields(ctx, input.MediaID, validated.MediaFields, validated.MarkComplete, marker); err != nil {
		return err
	}
	for _, relation := range validated.Relations {
		if err := a.writeRelation(ctx, input.MediaID, input.LibraryID, relation); err != nil {
			return err
		}
	}
	for _, externalID := range validated.ExternalIDs {
		if err := a.store.AddMediaExternalID(ctx, input.MediaID, externalID.Scheme, externalID.Value); err != nil {
			return err
		}
	}
	return nil
}

func (a *hydrationAdapter) validatePlan(plan HydrationPlan, marker string) (HydrationPlan, error) {
	validated := HydrationPlan{MarkComplete: plan.MarkComplete}
	for _, field := range plan.MediaFields {
		if field.Name == "" {
			continue
		}
		if marker != "" && field.Name == marker {
			return HydrationPlan{}, fmt.Errorf("hydration completion marker %q must be controlled by MarkComplete", marker)
		}
		declaredType, ok := a.mediaFieldType(field.Name)
		if !ok {
			return HydrationPlan{}, fmt.Errorf("hydration media field %q is not declared", field.Name)
		}
		if declaredType != string(field.Type) {
			return HydrationPlan{}, fmt.Errorf("hydration media field %q has type %q, want declared type %q", field.Name, field.Type, declaredType)
		}
		validated.MediaFields = append(validated.MediaFields, field)
	}
	for _, relation := range plan.Relations {
		if relation.From.Kind == "" || relation.From.Title == "" || relation.Name == "" {
			continue
		}
		declared, ok := a.declaredRelationSpecs[relation.From.Kind+"\x00"+relation.Name]
		if !ok {
			return HydrationPlan{}, fmt.Errorf("hydration relation %q from entity kind %q is not declared", relation.Name, relation.From.Kind)
		}
		if relation.RequiresRelation != "" {
			if _, ok := a.declaredRelationSpecs[relation.From.Kind+"\x00"+relation.RequiresRelation]; !ok {
				return HydrationPlan{}, fmt.Errorf("hydration prerequisite relation %q from entity kind %q is not declared", relation.RequiresRelation, relation.From.Kind)
			}
		}
		if err := validateRelationTarget(relation, declared); err != nil {
			return HydrationPlan{}, err
		}
		if hydrationRelationCardinality(relation.Cardinality) != hydrationRelationCardinality(declared.Cardinality) {
			return HydrationPlan{}, fmt.Errorf("hydration relation %q from entity kind %q has cardinality %q, want declared cardinality %q", relation.Name, relation.From.Kind, hydrationRelationCardinality(relation.Cardinality), hydrationRelationCardinality(declared.Cardinality))
		}
		if err := validateRelationAttributes(relation, declared); err != nil {
			return HydrationPlan{}, err
		}
		if err := a.validateEntity(relation.From); err != nil {
			return HydrationPlan{}, err
		}
		if relation.ToEntity != nil {
			if err := a.validateEntity(*relation.ToEntity); err != nil {
				return HydrationPlan{}, err
			}
		}
		validated.Relations = append(validated.Relations, relation)
	}
	for _, externalID := range plan.ExternalIDs {
		if externalID.Scheme == "" || externalID.Value == "" {
			continue
		}
		if !a.declaredExternalIDs[externalID.Scheme] {
			return HydrationPlan{}, fmt.Errorf("hydration external-ID scheme %q is not declared by the compiled mapper", externalID.Scheme)
		}
		validated.ExternalIDs = append(validated.ExternalIDs, externalID)
	}
	if plan.MarkComplete {
		declaredType, ok := a.declaredMediaFields[marker]
		if marker == "" || !ok {
			return HydrationPlan{}, fmt.Errorf("hydration completion marker %q is not declared", marker)
		}
		if declaredType != string(store.EntityFieldFlag) {
			return HydrationPlan{}, fmt.Errorf("hydration completion marker %q has declared type %q, want %q", marker, declaredType, store.EntityFieldFlag)
		}
	}
	return validated, nil
}

func validateRelationTarget(relation HydrationRelation, declared plugin.RelationSpec) error {
	if declared.ToKind == plugin.MediaKindName {
		if relation.ToEntity != nil {
			return fmt.Errorf("hydration relation %q from entity kind %q targets Media but supplied a target Entity", relation.Name, relation.From.Kind)
		}
		return nil
	}
	if relation.ToEntity == nil {
		return fmt.Errorf("hydration relation %q from entity kind %q targets entity kind %q but supplied Media", relation.Name, relation.From.Kind, declared.ToKind)
	}
	if relation.ToEntity.Kind != declared.ToKind {
		return fmt.Errorf("hydration relation %q from entity kind %q targets entity kind %q, want declared kind %q", relation.Name, relation.From.Kind, relation.ToEntity.Kind, declared.ToKind)
	}
	if relation.ToEntity.Title == "" {
		return fmt.Errorf("hydration relation %q from entity kind %q has an empty target Entity title", relation.Name, relation.From.Kind)
	}
	return nil
}

func hydrationRelationCardinality(cardinality string) string {
	if cardinality == "" {
		return "many"
	}
	return cardinality
}

func validateRelationAttributes(relation HydrationRelation, declared plugin.RelationSpec) error {
	untyped := make(map[string]bool, len(declared.Attributes))
	for _, name := range declared.Attributes {
		untyped[name] = true
	}
	typed := make(map[string]string, len(declared.TypedAttributes))
	for _, attribute := range declared.TypedAttributes {
		typed[attribute.Name] = attribute.Type
	}
	names := make([]string, 0, len(relation.Attributes))
	for name := range relation.Attributes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if untyped[name] {
			continue
		}
		fieldType, ok := typed[name]
		if !ok {
			return fmt.Errorf("hydration relation %q from entity kind %q uses undeclared attribute %q", relation.Name, relation.From.Kind, name)
		}
		if !validTypedRelationAttribute(fieldType, relation.Attributes[name]) {
			return fmt.Errorf("hydration relation %q from entity kind %q attribute %q has value that is not valid %q", relation.Name, relation.From.Kind, name, fieldType)
		}
	}
	return nil
}

func validTypedRelationAttribute(fieldType, raw string) bool {
	value := strings.TrimSpace(raw)
	if value == "" {
		return false
	}
	switch store.EntityFieldType(fieldType) {
	case store.EntityFieldText:
		return true
	case store.EntityFieldURL:
		parsed, err := url.ParseRequestURI(value)
		if err != nil || !parsed.IsAbs() {
			return false
		}
		if strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https") {
			return parsed.Host != ""
		}
		return parsed.Opaque != "" || parsed.Host != "" || parsed.Path != ""
	case store.EntityFieldNumber:
		number, err := strconv.ParseFloat(value, 64)
		return err == nil && !math.IsNaN(number) && !math.IsInf(number, 0)
	case store.EntityFieldFlag:
		_, err := strconv.ParseBool(value)
		return err == nil
	case store.EntityFieldDate:
		for _, layout := range []string{"2006-01-02", time.RFC3339, time.RFC3339Nano} {
			if _, err := time.Parse(layout, value); err == nil {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (a *hydrationAdapter) mediaFieldType(name string) (string, bool) {
	if name == "title" {
		return string(store.EntityFieldText), true
	}
	fieldType, ok := a.declaredMediaFields[name]
	return fieldType, ok
}

func (a *hydrationAdapter) validateEntity(entity HydrationEntity) error {
	declared, ok := a.declaredEntityFields[entity.Kind]
	if !ok {
		return fmt.Errorf("hydration entity kind %q is not declared", entity.Kind)
	}
	for _, field := range entity.Fields {
		declaredType, ok := declared[field.Name]
		if !ok {
			return fmt.Errorf("hydration entity field %q on kind %q is not declared", field.Name, entity.Kind)
		}
		if declaredType != string(field.Type) {
			return fmt.Errorf("hydration entity field %q on kind %q has type %q, want declared type %q", field.Name, entity.Kind, field.Type, declaredType)
		}
	}
	return nil
}

func (a *hydrationAdapter) writeMediaFields(ctx context.Context, mediaID store.MediaID, mapped []store.MediaField, markComplete bool, marker string) error {
	fields := append([]store.MediaField(nil), mapped...)
	if markComplete {
		fields = append(fields, store.MediaField{Name: marker, Type: store.EntityFieldFlag, Flag: true})
	}
	if len(fields) == 0 {
		return nil
	}
	return a.store.InsertMediaFieldsIfAbsent(ctx, mediaID, fields)
}

func (a *hydrationAdapter) writeRelation(ctx context.Context, mediaID store.MediaID, libraryID store.LibraryID, mapped HydrationRelation) error {
	if mapped.From.Kind == "" {
		return nil
	}
	if mapped.From.Title == "" {
		return nil
	}
	if mapped.Name == "" {
		return nil
	}
	if _, ok := a.declaredRelationSpecs[mapped.From.Kind+"\x00"+mapped.Name]; !ok {
		return nil
	}
	if mapped.RequiresRelation != "" {
		if _, ok := a.declaredRelationSpecs[mapped.From.Kind+"\x00"+mapped.RequiresRelation]; !ok {
			return nil
		}
	}
	fromID, err := a.upsertEntity(ctx, libraryID, mapped.From)
	if err != nil {
		return err
	}
	relation := store.EntityRelation{
		FromEntityID: fromID,
		Name:         mapped.Name,
		ToMediaID:    mediaID,
		Attributes:   mapped.Attributes,
		Cardinality:  mapped.Cardinality,
		SortOrder:    mapped.SortOrder,
	}
	if mapped.ToEntity != nil {
		toID, err := a.upsertEntity(ctx, libraryID, *mapped.ToEntity)
		if err != nil {
			return err
		}
		relation.ToMediaID = ""
		relation.ToEntityID = toID
	}
	return a.store.UpsertEntityRelation(ctx, relation)
}

func (a *hydrationAdapter) upsertEntity(ctx context.Context, libraryID store.LibraryID, mapped HydrationEntity) (store.EntityID, error) {
	id := identify.EntityIDForName(libraryID, mapped.Kind, mapped.Title)
	err := a.store.UpsertEntity(ctx, store.Entity{
		ID:        id,
		LibraryID: libraryID,
		Kind:      mapped.Kind,
		Title:     mapped.Title,
		Fields:    mapped.Fields,
	})
	return id, err
}

var _ scanpkg.MetadataHydrator = (*hydrationAdapter)(nil)
