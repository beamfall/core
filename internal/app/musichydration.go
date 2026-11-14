package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/plugin"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/serverrole"
	"github.com/beamfall/core/internal/store"
)

// hydrationStore is the write slice used by compiled hydration contributions.
type hydrationStore interface {
	InsertMediaFieldsIfAbsent(ctx context.Context, id store.MediaID, fields []store.MediaField) error
	UpsertEntity(ctx context.Context, entity store.Entity) error
	UpsertEntityRelation(ctx context.Context, relation store.EntityRelation) error
	AddMediaExternalID(ctx context.Context, mediaID store.MediaID, scheme string, value string) error
	EntityRelationTargets(ctx context.Context, libraryID store.LibraryID, id store.EntityID, relation string) ([]store.EntityID, bool, error)
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
	// entityScopes maps a scoped entity kind to the relation recording its scope.
	entityScopes map[string]string
	// scopeMu serialises a scoped plan's identity resolution with the relation
	// writes that record its scope, so two scans hydrating one Library at once
	// cannot both adopt the same title-only Entity for different scopes.
	scopeMu sync.Mutex
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
			entityScopes:          declaredEntityScopes(mapper),
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

func declaredEntityScopes(mapper HydrationMapper) map[string]string {
	scoped, ok := mapper.(HydrationScopeMapper)
	if !ok {
		return nil
	}
	scopes := map[string]string{}
	for _, scope := range scoped.EntityScopes() {
		if scope.Kind != "" && scope.Relation != "" {
			scopes[scope.Kind] = scope.Relation
		}
	}
	return scopes
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
	if err := a.writeRelations(ctx, input.MediaID, input.LibraryID, validated.Relations); err != nil {
		return err
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
	if entity.Scope != "" {
		relation, ok := a.entityScopes[entity.Kind]
		if !ok {
			return fmt.Errorf("hydration entity kind %q carries a scope but the mapper declares no scope relation for it", entity.Kind)
		}
		spec, ok := a.declaredRelationSpecs[entity.Kind+"\x00"+relation]
		if !ok || spec.ToKind == plugin.MediaKindName {
			return fmt.Errorf("hydration scope relation %q from entity kind %q is not a declared entity relation", relation, entity.Kind)
		}
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

// writeRelations writes one plan's relations. A scoped Entity's id is resolved
// once per plan, so every relation from it (its Media and its scope) lands on
// the same Entity, and under scopeMu, so the resolution and the scope relation
// that settles it are one step for concurrent scans.
func (a *hydrationAdapter) writeRelations(ctx context.Context, mediaID store.MediaID, libraryID store.LibraryID, relations []HydrationRelation) error {
	if len(a.entityScopes) > 0 {
		a.scopeMu.Lock()
		defer a.scopeMu.Unlock()
	}
	resolved := map[string]store.EntityID{}
	for _, relation := range relations {
		if err := a.writeRelation(ctx, mediaID, libraryID, relation, resolved); err != nil {
			return err
		}
	}
	return nil
}

func (a *hydrationAdapter) writeRelation(ctx context.Context, mediaID store.MediaID, libraryID store.LibraryID, mapped HydrationRelation, resolved map[string]store.EntityID) error {
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
	fromID, err := a.upsertEntity(ctx, libraryID, mapped.From, resolved)
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
		toID, err := a.upsertEntity(ctx, libraryID, *mapped.ToEntity, resolved)
		if err != nil {
			return err
		}
		relation.ToMediaID = ""
		relation.ToEntityID = toID
	}
	return a.store.UpsertEntityRelation(ctx, relation)
}

func (a *hydrationAdapter) upsertEntity(ctx context.Context, libraryID store.LibraryID, mapped HydrationEntity, resolved map[string]store.EntityID) (store.EntityID, error) {
	id, err := a.entityID(ctx, libraryID, mapped, resolved)
	if err != nil {
		return "", err
	}
	err = a.store.UpsertEntity(ctx, store.Entity{
		ID:        id,
		LibraryID: libraryID,
		Kind:      mapped.Kind,
		Title:     mapped.Title,
		Fields:    mapped.Fields,
	})
	return id, err
}

// entityID resolves the id a mapped Entity is written under. A scoped Entity
// uses its scoped id once that Entity exists. Until then a title-only Entity
// of the same kind and title (written before the kind was scoped) is kept when
// its scope relation points at nothing or only at this scope, so a rescan
// neither orphans nor duplicates it; one that records another scope is left
// alone and the scoped Entity is created beside it. resolved holds the ids
// already resolved for the plan being written, which win over a fresh lookup.
func (a *hydrationAdapter) entityID(ctx context.Context, libraryID store.LibraryID, mapped HydrationEntity, resolved map[string]store.EntityID) (store.EntityID, error) {
	if mapped.Scope == "" {
		return identify.EntityIDForName(libraryID, mapped.Kind, mapped.Title), nil
	}
	key := mapped.Kind + "\x00" + mapped.Scope + "\x00" + mapped.Title
	if id, ok := resolved[key]; ok {
		return id, nil
	}
	id, err := a.scopedEntityID(ctx, libraryID, mapped)
	if err == nil {
		resolved[key] = id
	}
	return id, err
}

func (a *hydrationAdapter) scopedEntityID(ctx context.Context, libraryID store.LibraryID, mapped HydrationEntity) (store.EntityID, error) {
	unscoped := identify.EntityIDForName(libraryID, mapped.Kind, mapped.Title)
	relation := a.entityScopes[mapped.Kind]
	scoped := identify.EntityIDForScopedName(libraryID, mapped.Kind, mapped.Scope, mapped.Title)
	if _, exists, err := a.store.EntityRelationTargets(ctx, libraryID, scoped, relation); err != nil || exists {
		return scoped, err
	}
	targets, exists, err := a.store.EntityRelationTargets(ctx, libraryID, unscoped, relation)
	if err != nil || !exists {
		return scoped, err
	}
	scope := identify.EntityIDForName(libraryID, a.declaredRelationSpecs[mapped.Kind+"\x00"+relation].ToKind, mapped.Scope)
	for _, target := range targets {
		if target != scope {
			return scoped, nil
		}
	}
	return unscoped, nil
}

// entityScopeSplitter is the store write behind the one-time scope repair and
// the backfill scans that re-hydrate what it reopened.
type entityScopeSplitter interface {
	SplitAmbiguousEntityScopes(ctx context.Context, pluginID, kind, relation, markerField string, now time.Time) (int, error)
	PendingEntityScopeBackfills(ctx context.Context) ([]store.LibraryID, error)
	ClearEntityScopeBackfill(ctx context.Context, libraryID store.LibraryID) error
	LibraryRoots(ctx context.Context, libraryID store.LibraryID) ([]string, error)
	EnqueueJob(ctx context.Context, job store.Job) (store.Job, error)
}

// splitAmbiguousHydrationScopes runs the one-time repair for every scoped kind
// an active mapper declares: Entities written by title alone before the kind
// was scoped are reopened so the next scan separates them (the store records
// the run, so this is a single cheap insert on every later start). An
// ordinary scan never hydrates an unchanged file again, so a backfill scan is
// then queued for every root of each Library the repair reopened Media in. A
// failure only delays the repair or the queueing to the next start; an
// Attached node never authors catalog rows and skips both.
func splitAmbiguousHydrationScopes(ctx context.Context, st entityScopeSplitter, hydrators map[string]scanpkg.MetadataHydrator, mappers map[string]HydrationMapper, logger *slog.Logger) {
	for pluginID := range hydrators {
		mapper := mappers[pluginID]
		scoped, ok := mapper.(HydrationScopeMapper)
		if !ok || mapper.MarkerField() == "" {
			continue
		}
		for _, scope := range scoped.EntityScopes() {
			reopened, err := st.SplitAmbiguousEntityScopes(ctx, pluginID, scope.Kind, scope.Relation, mapper.MarkerField(), time.Now().UTC())
			switch {
			case errors.Is(err, serverrole.ErrAttachedAuthorityRefused):
			case err != nil:
				logger.Warn("entity scope repair failed; retrying next start", "plugin", pluginID, "kind", scope.Kind, "error", err)
			case reopened > 0:
				logger.Info("entity scope repair reopened entities for the next scan", "plugin", pluginID, "kind", scope.Kind, "entities", reopened)
			}
		}
	}
	queueEntityScopeBackfills(ctx, st, logger)
}

// queueEntityScopeBackfills queues a backfill scan of every root of each
// Library a scope repair reopened Media in, then clears the Library's pending
// mark. The mark is cleared only after its scans are queued, so a crash in
// between queues them again on the next start (scan dedupe absorbs repeats).
func queueEntityScopeBackfills(ctx context.Context, st entityScopeSplitter, logger *slog.Logger) {
	libraries, err := st.PendingEntityScopeBackfills(ctx)
	switch {
	case errors.Is(err, serverrole.ErrAttachedAuthorityRefused):
		return
	case err != nil:
		logger.Warn("entity scope backfill lookup failed; retrying next start", "error", err)
		return
	}
	for _, libraryID := range libraries {
		if err := queueLibraryBackfill(ctx, st, libraryID); errors.Is(err, errNoLibraryRoots) {
			continue // nothing to scan yet; a later start queues it once a root exists
		} else if err != nil {
			logger.Warn("entity scope backfill scan not queued; retrying next start", "library", libraryID, "error", err)
			continue
		}
		if err := st.ClearEntityScopeBackfill(ctx, libraryID); err != nil {
			logger.Warn("entity scope backfill mark not cleared", "library", libraryID, "error", err)
		}
	}
}

// backfillQueuer is the store surface queueLibraryBackfill needs.
type backfillQueuer interface {
	LibraryRoots(ctx context.Context, libraryID store.LibraryID) ([]string, error)
	EnqueueJob(ctx context.Context, job store.Job) (store.Job, error)
}

// errNoLibraryRoots reports a Library with no root to scan: no backfill was
// queued, so the caller keeps its mark and checks again on a later start.
var errNoLibraryRoots = errors.New("library has no roots to scan")

func queueLibraryBackfill(ctx context.Context, st backfillQueuer, libraryID store.LibraryID) error {
	roots, err := st.LibraryRoots(ctx, libraryID)
	var invalid store.ValidationError
	if errors.As(err, &invalid) {
		return nil // the Library is gone: nothing left to hydrate
	}
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		return errNoLibraryRoots
	}
	for _, root := range roots {
		job, err := scanpkg.NewJob(scanpkg.JobRequest{LibraryID: libraryID, Root: root, CreatedAt: time.Now().UTC(), Backfill: true})
		if err != nil {
			return err
		}
		if _, err := st.EnqueueJob(ctx, job); err != nil {
			return err
		}
	}
	return nil
}

var _ scanpkg.MetadataHydrator = (*hydrationAdapter)(nil)
