package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"slices"
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
	UpsertMediaFieldsWithSource(ctx context.Context, id store.MediaID, fields []store.MediaField, source store.MutationSource) error
	UpsertScannedMediaTag(ctx context.Context, id store.MediaID, name, value string) error
	MediaFields(ctx context.Context, id store.MediaID, filter store.LibraryFilter) ([]store.MediaField, error)
	MediaFieldLocks(ctx context.Context, id store.MediaID, filter store.LibraryFilter) ([]store.MediaFieldLock, error)
	UpsertEntity(ctx context.Context, entity store.Entity) error
	UpsertEntityRelation(ctx context.Context, relation store.EntityRelation) error
	ReplaceMediaRelations(ctx context.Context, mediaID store.MediaID, relations []store.EntityRelation) error
	AddMediaExternalID(ctx context.Context, mediaID store.MediaID, scheme string, value string) error
	EntityRelationTargets(ctx context.Context, libraryID store.LibraryID, id store.EntityID, relation string) ([]store.EntityID, bool, error)
}

// HydrationWrite is how a fingerprint-keyed hydration lands, decided by a
// HydrationRehydrator from the recorded and the current content fingerprint.
type HydrationWrite int

const (
	// HydrationWriteFresh writes absent plan fields and records the fingerprint.
	HydrationWriteFresh HydrationWrite = iota
	// HydrationWriteSkip skips the plan: the bytes were already hydrated.
	HydrationWriteSkip
	// HydrationWriteReplace replaces scan-tier values through the source-ranked
	// upsert (operator, manual and locked fields win) and records the fingerprint.
	HydrationWriteReplace
)

// HydrationRehydrator is the optional HydrationMapper extension that keys
// hydration on the file's content fingerprint, so a re-tagged file is
// re-hydrated and an unchanged one is not. FingerprintField names a declared
// text Media field holding the hydration key the last hydration recorded (the
// content fingerprint plus a digest of the mapped plan, see hydrationKey); a
// mapper without it keeps the absent-only write. DecideHydrationWrite
// compares the recorded and the current key.
type HydrationRehydrator interface {
	FingerprintField() string
	DecideHydrationWrite(recordedFingerprint, currentFingerprint string) HydrationWrite
}

// hydrationAdapter validates a plugin-supplied write plan against the active
// bundle before adapting it to scan's local-metadata seam.
type hydrationAdapter struct {
	store      hydrationStore
	mapper     HydrationMapper
	capability scanpkg.MetadataHydrationCapability

	rehydrator       HydrationRehydrator
	fingerprintField string
	// hydratedMediaFields bounds the plan's Media fields when the mapper
	// names them (HydrationMediaFieldsMapper); nil allows any declared field.
	hydratedMediaFields map[string]bool

	tag                   HydrationTag
	declaredMediaFields   map[string]string
	declaredEntityFields  map[string]map[string]string
	declaredRelationSpecs map[string]plugin.RelationSpec
	declaredExternalIDs   map[string]bool
	// entityScopes maps a scoped entity kind to the relation recording its scope.
	entityScopes map[string]string
	// foldKinds holds the kinds whose title variants merge on rehydration
	// (HydrationTitleFoldMapper); foldStore is nil when the store cannot merge.
	foldKinds map[string]bool
	foldStore hydrationFoldStore
	// foldArtwork copies a merged-away variant's artwork to the survivor; nil
	// when no artwork store is wired.
	foldArtwork hydrationFoldArtwork
	// foldChecked holds the Entities mergeFoldSiblings already checked; it is
	// guarded by scopeMu.
	foldChecked map[store.EntityID]bool
	// foldRecordsSettled is true once no title-fold merge is known to owe its
	// activity entry (recordOwedTitleFoldMerges); guarded by scopeMu.
	foldRecordsSettled bool
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
		adapter := &hydrationAdapter{
			store:                 st,
			mapper:                mapper,
			capability:            metadataHydrationCapability(bundle, mapper.MarkerField()),
			declaredMediaFields:   bundle.MediaFieldTypes(),
			declaredEntityFields:  declaredEntityFieldTypes(bundle),
			declaredRelationSpecs: declaredRelationSpecs(bundle),
			declaredExternalIDs:   declaredExternalIDSet(mapper.ExternalIDSchemes()),
			entityScopes:          declaredEntityScopes(mapper),
			foldKinds:             declaredTitleFoldKinds(mapper),
		}
		if foldStore, ok := st.(hydrationFoldStore); ok && len(adapter.foldKinds) > 0 {
			adapter.foldStore = foldStore
		}
		if rehydrator, ok := mapper.(HydrationRehydrator); ok {
			adapter.rehydrator = rehydrator
			adapter.fingerprintField = rehydrator.FingerprintField()
		}
		if tagged, ok := mapper.(HydrationTagMapper); ok {
			tag := tagged.HydrationTag()
			if tag.Field != "" {
				if adapter.declaredMediaFields[tag.Field] != string(store.EntityFieldText) || tag.Field == adapter.fingerprintField || tag.Field == adapter.capability.MarkerField {
					slog.Warn("hydration: invalid standalone tag declaration", "plugin", pluginID, "field", tag.Field)
				} else {
					adapter.tag = tag
					adapter.capability.TrackFilePath = true
				}
			}
		}
		if bounded, ok := mapper.(HydrationMediaFieldsMapper); ok {
			fields := slices.Clone(bounded.HydratedMediaFields())
			adapter.boundMediaFields(append(fields, adapter.tag.Field))
		}
		out[pluginID] = adapter
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

// boundMediaFields records the only Media fields the mapper's plans may carry
// and declares them, with the fingerprint field the seam records, as every
// scan-tier Media value this hydrator writes besides its marker flag.
func (a *hydrationAdapter) boundMediaFields(names []string) {
	a.hydratedMediaFields = make(map[string]bool, len(names))
	written := make([]string, 0, len(names)+1)
	for _, name := range names {
		if name != "" && !a.hydratedMediaFields[name] {
			a.hydratedMediaFields[name] = true
			written = append(written, name)
		}
	}
	if a.fingerprintField != "" && !a.hydratedMediaFields[a.fingerprintField] {
		written = append(written, a.fingerprintField)
	}
	a.capability.MediaFieldsBounded = true
	a.capability.MediaFields = written
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
	capability.MediaFields = append([]string(nil), capability.MediaFields...)
	return capability
}

func (a *hydrationAdapter) HydrateMetadata(ctx context.Context, input scanpkg.MetadataHydrationInput) error {
	if !a.capability.Supports(input.Class) {
		return nil
	}
	plan := a.mapper.MapHydration(input.Metadata, input.Path)
	key, err := hydrationKey(input.ContentFingerprint, plan)
	if err != nil {
		return err
	}
	write, err := a.decideWrite(ctx, input, key)
	if err != nil {
		return err
	}
	// Keep the standalone tag out of the plan digest so adding it does
	// not replay an already hydrated Entity plan.
	var tag *store.MediaField
	if input.MetadataProbed && a.tag.Field != "" {
		tag = &store.MediaField{
			Name: a.tag.Field, Type: store.EntityFieldText, Text: a.tag.Read(input.Metadata),
		}
	}
	if write == HydrationWriteSkip {
		return a.upsertStandaloneTag(ctx, input.MediaID, tag)
	}
	replacing := write == HydrationWriteReplace
	var locked map[string]bool
	if replacing {
		if locked, err = a.lockedFields(ctx, input.MediaID); err != nil {
			// Fail closed for the locks: without them a replace could clobber a
			// locked field, so only absent fields are written and the recorded
			// fingerprint stays, leaving the replace to a later scan.
			slog.Warn("hydration: field locks unreadable, writing absent fields only", "media", input.MediaID, "error", err)
			write = HydrationWriteFresh
		}
	}
	if plan.Empty() && tag == nil {
		return nil
	}
	marker := a.capability.MarkerField
	validated, err := a.validatePlan(plan, marker)
	if err != nil {
		return err
	}
	if validated.Empty() && tag == nil {
		return nil
	}
	// Relations first: a failed relation write (a title-variant merge among
	// them) leaves the completion marker unset, so a later scan retries. A
	// Media hydrated before carries its marker already, so where merges run
	// the marker is cleared and a backfill scan queued for the next start.
	if err := a.writeRelations(ctx, input.MediaID, input.LibraryID, validated.Relations, write == HydrationWriteReplace); err != nil {
		if a.foldStore != nil && marker != "" {
			reopenErr := a.foldStore.ReopenMediaHydration(context.WithoutCancel(ctx), input.LibraryID, input.MediaID, marker, time.Now().UTC())
			if reopenErr != nil && !errors.Is(reopenErr, serverrole.ErrAttachedAuthorityRefused) {
				return errors.Join(err, fmt.Errorf("reopen hydration: %w", reopenErr))
			}
		}
		return err
	}
	if replacing && tag != nil {
		// Keep replacement writes subject to the plan's field lock handling.
		validated.MediaFields = append(validated.MediaFields, *tag)
	} else if err := a.upsertStandaloneTag(ctx, input.MediaID, tag); err != nil {
		return err
	}
	if err := a.writeMediaFields(ctx, input.MediaID, validated, marker, write, locked); err != nil {
		return err
	}
	for _, externalID := range validated.ExternalIDs {
		if err := a.store.AddMediaExternalID(ctx, input.MediaID, externalID.Scheme, externalID.Value); err != nil {
			return err
		}
	}
	return a.recordFingerprint(ctx, input.MediaID, write, key)
}

func (a *hydrationAdapter) upsertStandaloneTag(ctx context.Context, mediaID store.MediaID, tag *store.MediaField) error {
	if tag == nil {
		return nil
	}
	return a.store.UpsertScannedMediaTag(ctx, mediaID, tag.Name, tag.Text)
}

// hydrationKey is the value the skip decision compares and the fingerprint
// field records: the content fingerprint plus a digest of the mapped plan. The
// plan carries what the mapper derived from the file's path (a disc folder,
// say), so a file moved with the same bytes is re-hydrated when the move
// changes a derived fact, and skipped when neither bytes nor derived facts
// changed. An empty fingerprint stays empty so the mapper's no-fingerprint
// rule still applies.
func hydrationKey(fingerprint string, plan HydrationPlan) (string, error) {
	if fingerprint == "" {
		return "", nil
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return fingerprint + ";plan=" + hex.EncodeToString(sum[:8]), nil
}

// recordFingerprint records the hydrated content fingerprint after every other
// hydration write has landed, so a failed write leaves the old fingerprint and
// the next scan retries instead of skipping.
func (a *hydrationAdapter) recordFingerprint(ctx context.Context, mediaID store.MediaID, write HydrationWrite, fingerprint string) error {
	if a.fingerprintField == "" || fingerprint == "" {
		return nil
	}
	field := []store.MediaField{{Name: a.fingerprintField, Type: store.EntityFieldText, Text: fingerprint}}
	if write != HydrationWriteReplace {
		return a.store.InsertMediaFieldsIfAbsent(ctx, mediaID, field)
	}
	return a.store.UpsertMediaFieldsWithSource(ctx, mediaID, field, store.MutationSourceScan)
}

// decideWrite asks an opted-in mapper how to land this hydration, from the
// hydration key recorded at the last hydration and the current one.
// A mapper without a fingerprint field always gets the absent-only write.
func (a *hydrationAdapter) decideWrite(ctx context.Context, input scanpkg.MetadataHydrationInput, key string) (HydrationWrite, error) {
	if a.rehydrator == nil {
		return HydrationWriteFresh, nil
	}
	if declaredType, ok := a.declaredMediaFields[a.fingerprintField]; a.fingerprintField == "" || !ok || declaredType != string(store.EntityFieldText) {
		return HydrationWriteFresh, fmt.Errorf("hydration fingerprint field %q is not a declared text field", a.fingerprintField)
	}
	fields, err := a.store.MediaFields(ctx, input.MediaID, metadataLibraryFilter)
	if err != nil {
		return HydrationWriteFresh, err
	}
	recorded := ""
	for _, field := range fields {
		if field.Name == a.fingerprintField {
			recorded = field.Text
			break
		}
	}
	// The old v2 plan included the release value. For tagged mappers
	// an unchanged file at a known path needs only the standalone tag.
	// A new path can change derived facts even when the bytes match.
	if a.tag.Field != "" && !input.FilePathChanged && input.ContentFingerprint != "" {
		if legacy, ok := strings.CutPrefix(recorded, "v2:"); ok {
			content, _, _ := strings.Cut(legacy, ";plan=")
			if content == input.ContentFingerprint {
				return HydrationWriteSkip, nil
			}
		}
	}
	return a.rehydrator.DecideHydrationWrite(recorded, key), nil
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
		if a.fingerprintField != "" && field.Name == a.fingerprintField {
			return HydrationPlan{}, fmt.Errorf("hydration fingerprint field %q is recorded by the hydration seam", a.fingerprintField)
		}
		declaredType, ok := a.mediaFieldType(field.Name)
		if !ok {
			return HydrationPlan{}, fmt.Errorf("hydration media field %q is not declared", field.Name)
		}
		if a.hydratedMediaFields != nil && !a.hydratedMediaFields[field.Name] {
			return HydrationPlan{}, fmt.Errorf("hydration media field %q is not one the mapper names", field.Name)
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

// writeMediaFields lands the validated Media fields. Fresh is absent-only.
// Replace upserts at the scan tier — the tier the absent-only path and the
// import record — so tag values replace earlier tag or filename values while
// operator and manual values keep winning in canWriteFieldTx, and fields the
// operator locked (ADR-0072) are left out.
func (a *hydrationAdapter) writeMediaFields(ctx context.Context, mediaID store.MediaID, validated HydrationPlan, marker string, write HydrationWrite, locked map[string]bool) error {
	fields := append([]store.MediaField(nil), validated.MediaFields...)
	if validated.MarkComplete {
		fields = append(fields, store.MediaField{Name: marker, Type: store.EntityFieldFlag, Flag: true})
	}
	if len(fields) == 0 {
		return nil
	}
	if write != HydrationWriteReplace {
		return a.store.InsertMediaFieldsIfAbsent(ctx, mediaID, fields)
	}
	kept := fields[:0]
	for _, field := range fields {
		if !locked[field.Name] {
			kept = append(kept, field)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return a.store.UpsertMediaFieldsWithSource(ctx, mediaID, kept, store.MutationSourceScan)
}

// lockedFields reads the Media's source-precedence locks (ADR-0072).
func (a *hydrationAdapter) lockedFields(ctx context.Context, mediaID store.MediaID) (map[string]bool, error) {
	locks, err := a.store.MediaFieldLocks(ctx, mediaID, metadataLibraryFilter)
	if err != nil {
		return nil, err
	}
	locked := map[string]bool{}
	for _, lock := range locks {
		if lock.Locked {
			locked[lock.Field] = true
		}
	}
	return locked, nil
}

// writeRelations writes one plan's relations. A scoped Entity's id is resolved
// once per plan, so every relation from it (its Media and its scope) lands on
// the same Entity, and under scopeMu, so the resolution and the scope relation
// that settles it are one step for concurrent scans.
// writeRelations writes the plan's relations. A replace collects the
// Media-target relations and rewrites them together, so the Media's previous
// rows for every relation name the plan carries — single- and many-valued
// alike — are dropped and relation names the plan does not carry are kept.
func (a *hydrationAdapter) writeRelations(ctx context.Context, mediaID store.MediaID, libraryID store.LibraryID, relations []HydrationRelation, replace bool) error {
	if len(a.entityScopes) > 0 || a.foldStore != nil {
		a.scopeMu.Lock()
		defer a.scopeMu.Unlock()
	}
	fold, err := a.titleFoldOn(ctx, libraryID)
	if err != nil {
		return err
	}
	written := make([]store.EntityRelation, len(relations))
	resolved := map[string]store.EntityID{}
	var holders []foldHolder
	if fold {
		if err := a.recordOwedTitleFoldMerges(ctx); err != nil {
			return err
		}
		var parents []foldMerge
		if relations, parents, err = a.redirectToEstablishedFoldTitles(ctx, libraryID, relations, resolved); err != nil {
			return err
		}
		if err := a.mergeFoldParents(ctx, libraryID, parents); err != nil {
			return err
		}
		if holders, err = a.priorFoldHolders(ctx, mediaID, libraryID, relations); err != nil {
			return err
		}
	}
	var mediaRelations []store.EntityRelation
	for i, mapped := range relations {
		relation, ok, err := a.resolveRelation(ctx, mediaID, libraryID, mapped, resolved)
		if err != nil {
			return err
		}
		if ok {
			written[i] = relation
		}
	}
	// Merge title variants while the Media is still on its previous holder, so
	// the merge journals and moves it with the rest (undo restores it) and a
	// failed merge leaves it there for the next hydration to retry.
	if err := a.mergeFoldedHolders(ctx, libraryID, relations, written, holders); err != nil {
		return err
	}
	for _, relation := range written {
		if relation.FromEntityID == "" {
			continue
		}
		if replace && relation.ToMediaID != "" {
			mediaRelations = append(mediaRelations, relation)
			continue
		}
		if err := a.store.UpsertEntityRelation(ctx, relation); err != nil {
			return err
		}
	}
	if len(mediaRelations) > 0 {
		if err := a.store.ReplaceMediaRelations(ctx, mediaID, mediaRelations); err != nil {
			return err
		}
	}
	if !fold {
		return nil
	}
	// A failed merge here leaves the completion marker unset, so the next
	// hydration retries it.
	return a.mergeFoldSiblings(ctx, libraryID, relations, written)
}

// resolveRelation upserts a mapped relation's Entities and returns the store
// relation to write; ok is false for a relation the bundle does not declare.
func (a *hydrationAdapter) resolveRelation(ctx context.Context, mediaID store.MediaID, libraryID store.LibraryID, mapped HydrationRelation, resolved map[string]store.EntityID) (store.EntityRelation, bool, error) {
	if mapped.From.Kind == "" || mapped.From.Title == "" || mapped.Name == "" {
		return store.EntityRelation{}, false, nil
	}
	if _, ok := a.declaredRelationSpecs[mapped.From.Kind+"\x00"+mapped.Name]; !ok {
		return store.EntityRelation{}, false, nil
	}
	if mapped.RequiresRelation != "" {
		if _, ok := a.declaredRelationSpecs[mapped.From.Kind+"\x00"+mapped.RequiresRelation]; !ok {
			return store.EntityRelation{}, false, nil
		}
	}
	fromID, err := a.upsertEntity(ctx, libraryID, mapped.From, resolved)
	if err != nil {
		return store.EntityRelation{}, false, err
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
			return store.EntityRelation{}, false, err
		}
		relation.ToMediaID = ""
		relation.ToEntityID = toID
	}
	return relation, true, nil
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
// already resolved for the plan being written (including the established
// title variants redirectToEstablishedFoldTitles settled), which win over a
// fresh lookup.
func (a *hydrationAdapter) entityID(ctx context.Context, libraryID store.LibraryID, mapped HydrationEntity, resolved map[string]store.EntityID) (store.EntityID, error) {
	key := mapped.Kind + "\x00" + mapped.Scope + "\x00" + mapped.Title
	if id, ok := resolved[key]; ok {
		return id, nil
	}
	if mapped.Scope == "" {
		return identify.EntityIDForName(libraryID, mapped.Kind, mapped.Title), nil
	}
	id, err := a.scopedEntityID(ctx, libraryID, mapped, resolved)
	if err == nil {
		resolved[key] = id
	}
	return id, err
}

// scopedEntityID is entityID's lookup for a scoped Entity not yet resolved in
// the plan. The plan's relations are written only after every id resolves, so
// a title-only Entity one scope keeps is claimed in resolved (under its id
// behind a \x01 prefix, naming the scope) and another scope of the same plan
// gets its scoped id instead of keeping the same Entity.
func (a *hydrationAdapter) scopedEntityID(ctx context.Context, libraryID store.LibraryID, mapped HydrationEntity, resolved map[string]store.EntityID) (store.EntityID, error) {
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
	claim := "\x01" + string(unscoped)
	if claimant, claimed := resolved[claim]; claimed && claimant != scope {
		return scoped, nil
	}
	resolved[claim] = scope
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
// Attached node never authors catalog rows and skips both. The one-time
// title-fold repair (reopenFoldedHydrationTitles) runs here too and shares
// the backfill queue.
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
	for pluginID := range hydrators {
		if mapper := mappers[pluginID]; mapper != nil {
			reopenFoldedHydrationTitles(ctx, st, pluginID, mapper, logger)
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
