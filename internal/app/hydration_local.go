package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/store"
)

// hydrationLocalStore is the store support a plan's local part needs;
// *store.Store satisfies it.
type hydrationLocalStore interface {
	UpdateMediaMetadata(ctx context.Context, id store.MediaID, metadata store.MediaMetadata) error
	ReleaseMediaFieldSource(ctx context.Context, id store.MediaID, source store.MutationSource) error
}

// hydrationEntityReader lets an Entity write merge its fields over the stored
// ones instead of replacing the whole set; *store.Store satisfies it.
type hydrationEntityReader interface {
	Entity(ctx context.Context, id store.EntityID, filter store.LibraryFilter) (store.Entity, error)
}

var (
	_ hydrationLocalStore    = (*store.Store)(nil)
	_ hydrationEntityReader  = (*store.Store)(nil)
	_ identify.EntityEnsurer = (*store.Store)(nil)
)

// sidecarState is a Media's recorded and current sidecar signatures; tracked
// is false when the mapper reads no sidecars.
type sidecarState struct {
	tracked  bool
	recorded string
	current  string
}

func (s sidecarState) changed() bool {
	return s.tracked && s.recorded != s.current
}

// sidecarState reads the signature recorded at the Media's last hydration and
// the current one for path.
func (a *hydrationAdapter) sidecarState(ctx context.Context, mediaID store.MediaID, path string) (sidecarState, error) {
	if a.sidecars == nil {
		return sidecarState{}, nil
	}
	if declaredType, ok := a.declaredMediaFields[a.sidecarField]; a.sidecarField == "" || !ok || declaredType != string(store.EntityFieldText) {
		return sidecarState{}, fmt.Errorf("hydration sidecar signature field %q is not a declared text field", a.sidecarField)
	}
	fields, err := a.store.MediaFields(ctx, mediaID, metadataLibraryFilter)
	if err != nil {
		return sidecarState{}, err
	}
	state := sidecarState{tracked: true, current: a.sidecars.SidecarSignature(path)}
	for _, field := range fields {
		if field.Name == a.sidecarField {
			state.recorded = field.Text
			break
		}
	}
	return state, nil
}

// SidecarsChanged reports whether the sidecars of an unchanged file changed
// since its last hydration, so the scan re-hydrates it.
func (a *hydrationAdapter) SidecarsChanged(ctx context.Context, mediaID store.MediaID, path string) (bool, error) {
	state, err := a.sidecarState(ctx, mediaID, path)
	if err != nil {
		return false, err
	}
	return state.changed(), nil
}

// recordSidecarSignature records the current signature after every other
// hydration write landed, so a failed write leaves the old one and the next
// scan retries.
func (a *hydrationAdapter) recordSidecarSignature(ctx context.Context, mediaID store.MediaID, state sidecarState) error {
	if !state.changed() {
		return nil
	}
	field := []store.MediaField{{Name: a.sidecarField, Type: store.EntityFieldText, Text: state.current}}
	return a.store.UpsertMediaFieldsWithSource(ctx, mediaID, field, store.MutationSourceScan)
}

// validateLocal checks a plan's local part against the bundle like the rest of
// the plan. The mapper's hydrated-field bound does not apply: local values are
// not scan-tier values.
func (a *hydrationAdapter) validateLocal(local *HydrationLocal, marker string) (*HydrationLocal, error) {
	if local.empty() {
		return nil, nil
	}
	validated := &HydrationLocal{
		Title:     strings.TrimSpace(local.Title),
		SortTitle: strings.TrimSpace(local.SortTitle),
		Summary:   strings.TrimSpace(local.Summary),
		Year:      max(local.Year, 0),
	}
	for _, field := range local.MediaFields {
		if field.Name == "" {
			continue
		}
		reserved := map[string]string{marker: "completion marker", a.fingerprintField: "fingerprint field", a.sidecarField: "sidecar signature field"}
		if role, ok := reserved[field.Name]; ok {
			return nil, fmt.Errorf("hydration local field %q is the seam's %s", field.Name, role)
		}
		declaredType, ok := a.declaredMediaFields[field.Name]
		if !ok {
			return nil, fmt.Errorf("hydration local field %q is not declared", field.Name)
		}
		if declaredType != string(field.Type) {
			return nil, fmt.Errorf("hydration local field %q has type %q, want declared type %q", field.Name, field.Type, declaredType)
		}
		validated.MediaFields = append(validated.MediaFields, field)
	}
	relations, err := a.validatePlan(HydrationPlan{Relations: local.Relations}, marker)
	if err != nil {
		return nil, err
	}
	for _, relation := range relations.Relations {
		if relation.ToEntity != nil {
			return nil, fmt.Errorf("hydration local relation %q must target the Media", relation.Name)
		}
	}
	validated.Relations = relations.Relations
	return validated, nil
}

// writeLocal releases the Media's previous local values to the scan tier, then
// writes the validated local part at the local tier. The release runs whenever
// local values may exist (a local part now or sidecars recorded before), so a
// value a sidecar no longer carries, or every value of a removed sidecar, stays
// on the Media unlocked.
func (a *hydrationAdapter) writeLocal(ctx context.Context, mediaID store.MediaID, libraryID store.LibraryID, local *HydrationLocal, sidecars sidecarState) error {
	if local == nil && sidecars.recorded == "" {
		return nil
	}
	localStore, ok := a.store.(hydrationLocalStore)
	if !ok {
		return errors.New("hydration store cannot write local metadata")
	}
	if err := localStore.ReleaseMediaFieldSource(ctx, mediaID, store.MutationSourceLocal); err != nil {
		return err
	}
	if local == nil {
		return nil
	}
	metadata := store.MediaMetadata{Source: store.MutationSourceLocal}
	if local.Title != "" {
		metadata.Title = &local.Title
	}
	if local.SortTitle != "" {
		metadata.SortTitle = &local.SortTitle
	}
	if local.Summary != "" {
		metadata.Summary = &local.Summary
	}
	if local.Year > 0 {
		metadata.Year = &local.Year
	}
	if metadata.Title != nil || metadata.SortTitle != nil || metadata.Summary != nil || metadata.Year != nil {
		if err := localStore.UpdateMediaMetadata(ctx, mediaID, metadata); err != nil {
			return err
		}
	}
	if len(local.MediaFields) > 0 {
		if err := a.store.UpsertMediaFieldsWithSource(ctx, mediaID, local.MediaFields, store.MutationSourceLocal); err != nil {
			return err
		}
	}
	if len(local.Relations) == 0 {
		return nil
	}
	return a.writeRelations(ctx, mediaID, libraryID, local.Relations, true)
}

// mergedEntityFields overlays planned fields on the Entity's stored ones by
// name, so a hydration that knows only some of an Entity's fields (a container's
// number, say) keeps the rest an identification stored. A store that cannot
// read Entities, or an Entity not stored yet, takes the planned fields as is.
func (a *hydrationAdapter) mergedEntityFields(ctx context.Context, id store.EntityID, planned []store.EntityField) ([]store.EntityField, error) {
	reader, ok := a.store.(hydrationEntityReader)
	if !ok {
		return planned, nil
	}
	stored, err := reader.Entity(ctx, id, metadataLibraryFilter)
	if store.IsEntityNotFound(err) {
		return planned, nil
	}
	if err != nil {
		return nil, err
	}
	overlay := make(map[string]bool, len(planned))
	for _, field := range planned {
		overlay[field.Name] = true
	}
	merged := make([]store.EntityField, 0, len(stored.Fields)+len(planned))
	for _, field := range stored.Fields {
		if !overlay[field.Name] {
			merged = append(merged, field)
		}
	}
	return append(merged, planned...), nil
}
