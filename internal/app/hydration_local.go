package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/plugin"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
)

// hydrationLocalStore is the store support local sidecars need;
// *store.Store satisfies it.
type hydrationLocalStore interface {
	ApplyLocalMetadata(ctx context.Context, id store.MediaID, local store.LocalMetadata) error
	ReleaseLocalMetadata(ctx context.Context, id store.MediaID, signatureField string) error
}

// hydrationMediaFiles lists a Media's files, so a sidecar beside any of its
// versions is found; *store.Store satisfies it.
type hydrationMediaFiles interface {
	MediaFilesForMedia(ctx context.Context, id store.MediaID, filter store.LibraryFilter) ([]store.MediaFile, error)
}

var (
	_ hydrationLocalStore    = (*store.Store)(nil)
	_ hydrationMediaFiles    = (*store.Store)(nil)
	_ identify.EntityEnsurer = (*store.Store)(nil)
)

// errSidecarUnreadable marks a sidecar lookup that failed. It never reads as
// a removed sidecar: the seam releases and records nothing.
var errSidecarUnreadable = errors.New("hydration sidecar unreadable")

// sidecarIdentity is what the seam records for the sidecar of source: a
// digest of the media path with the mapper's signature, so a selection that
// moves to another file's sidecar refreshes even when the signatures match.
// The record is a Media text field clients may display, so it carries a
// digest, never the server path (CORE-3174).
func sidecarIdentity(source, signature string) string {
	sum := sha256.Sum256([]byte(source + "\n" + signature))
	return hex.EncodeToString(sum[:])
}

// sidecarState is a Media's recorded sidecar identity, the current one and
// the media path whose sidecar it is ("" when the Media has none).
type sidecarState struct {
	recorded string
	current  string
	source   string
}

func (s sidecarState) changed() bool {
	return s.recorded != s.current
}

// sidecarState reads the identity recorded at the Media's last sidecar
// apply and finds its sidecar now: the sidecar of the first of the Media's
// file paths, in path order, that has one. The order does not depend on which
// version a scan is at, so two versions with their own sidecars do not take
// turns. A failed lookup stops the search with errSidecarUnreadable rather
// than moving on, so it cannot change the selection.
func (a *hydrationAdapter) sidecarState(ctx context.Context, mediaID store.MediaID, path string) (sidecarState, error) {
	if declaredType, ok := a.declaredMediaFields[a.sidecarField]; a.sidecarField == "" || !ok || declaredType != string(store.EntityFieldText) {
		return sidecarState{}, fmt.Errorf("hydration sidecar signature field %q is not a declared text field", a.sidecarField)
	}
	fields, err := a.store.MediaFields(ctx, mediaID, metadataLibraryFilter)
	if err != nil {
		return sidecarState{}, err
	}
	var state sidecarState
	for _, field := range fields {
		if field.Name == a.sidecarField {
			state.recorded = field.Text
			break
		}
	}
	paths := []string{path}
	if files, ok := a.store.(hydrationMediaFiles); ok {
		stored, err := files.MediaFilesForMedia(ctx, mediaID, metadataLibraryFilter)
		if err != nil {
			return sidecarState{}, err
		}
		for _, file := range stored {
			paths = append(paths, file.Path)
		}
	}
	slices.Sort(paths)
	for _, candidate := range slices.Compact(paths) {
		if candidate == "" {
			continue
		}
		signature, err := a.sidecars.SidecarSignature(candidate)
		if err != nil {
			return sidecarState{}, fmt.Errorf("%w: %s: %w", errSidecarUnreadable, candidate, err)
		}
		if signature != "" {
			state.current, state.source = sidecarIdentity(candidate, signature), candidate
			break
		}
	}
	return state, nil
}

// SidecarsChanged reports whether the Media's sidecar changed since it was
// last applied, so a scan re-reads an unchanged file. A failed lookup is an
// error, which leaves the sidecar for the next scan.
func (a *hydrationAdapter) SidecarsChanged(ctx context.Context, mediaID store.MediaID, path string) (bool, error) {
	if a.sidecars == nil {
		return false, nil
	}
	state, err := a.sidecarState(ctx, mediaID, path)
	if err != nil {
		return false, err
	}
	return state.changed(), nil
}

// HydrateSidecars applies the Media's sidecar when it changed since it was
// last applied, without the rest of the hydration.
func (a *hydrationAdapter) HydrateSidecars(ctx context.Context, input scanpkg.MetadataHydrationInput) error {
	if !a.capability.Supports(input.Class) {
		return nil
	}
	return a.applySidecars(ctx, input)
}

// applySidecars brings the Media's local tier in line with its sidecar. A
// changed sidecar is parsed and applied in one store transaction that records
// the parsed bytes' identity; a removed one is released. A sidecar that
// cannot be read or looked up is not a removal: nothing is written or
// released and the identity stays, so the next scan retries. An unchanged signature skips the
// apply, so an operator's unlock holds until the sidecar changes.
func (a *hydrationAdapter) applySidecars(ctx context.Context, input scanpkg.MetadataHydrationInput) error {
	if a.sidecars == nil {
		return nil
	}
	state, err := a.sidecarState(ctx, input.MediaID, input.Path)
	if errors.Is(err, errSidecarUnreadable) {
		slog.Warn("hydration: sidecar lookup failed, keeping the previous local values", "media", input.MediaID, "error", err.Error())
		return nil
	}
	if err != nil || !state.changed() {
		return err
	}
	localStore, ok := a.store.(hydrationLocalStore)
	if !ok {
		return errors.New("hydration store cannot write local metadata")
	}
	if state.source == "" {
		return localStore.ReleaseLocalMetadata(ctx, input.MediaID, a.sidecarField)
	}
	parsed := a.sidecars.MapSidecar(state.source)
	if parsed == nil {
		return nil // gone between the stat and the read; the next scan releases it
	}
	if parsed.Unreadable {
		slog.Warn("hydration: sidecar unreadable, keeping the previous local values", "media", input.MediaID, "path", state.source)
		return nil
	}
	if parsed.Signature == "" {
		return errors.New("hydration sidecar parse carries no signature")
	}
	local, err := a.validateLocal(parsed, a.capability.MarkerField)
	if err != nil {
		return err
	}
	relations, err := a.resolveLocalRelations(ctx, input.MediaID, input.LibraryID, local.Relations)
	if err != nil {
		return err
	}
	return localStore.ApplyLocalMetadata(ctx, input.MediaID, store.LocalMetadata{
		Metadata:       local.metadata(),
		Fields:         local.MediaFields,
		Relations:      relations,
		OwnedRelations: local.OwnedRelations,
		SignatureField: a.sidecarField,
		Signature:      sidecarIdentity(state.source, parsed.Signature),
	})
}

// metadata returns the built-in values a validated parse states.
func (l *HydrationLocal) metadata() store.MediaMetadata {
	var metadata store.MediaMetadata
	if l.Title != "" {
		metadata.Title = &l.Title
	}
	if l.SortTitle != "" {
		metadata.SortTitle = &l.SortTitle
	}
	if l.Summary != "" {
		metadata.Summary = &l.Summary
	}
	if l.Year > 0 {
		metadata.Year = &l.Year
	}
	return metadata
}

// validateLocal checks a sidecar parse against the bundle like a plan. The
// mapper's hydrated-field bound does not apply: local values are not
// scan-tier values. Every owned relation must be a declared Media relation
// and every stated relation one of them.
func (a *hydrationAdapter) validateLocal(local *HydrationLocal, marker string) (*HydrationLocal, error) {
	validated := &HydrationLocal{
		Title:     strings.TrimSpace(local.Title),
		SortTitle: strings.TrimSpace(local.SortTitle),
		Summary:   strings.TrimSpace(local.Summary),
		Year:      max(local.Year, 0),
	}
	reserved := map[string]string{marker: "completion marker", a.fingerprintField: "fingerprint field", a.sidecarField: "sidecar signature field"}
	for _, field := range local.MediaFields {
		if field.Name == "" {
			continue
		}
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
	owned := map[string]bool{}
	for _, relation := range local.OwnedRelations {
		declared, ok := a.declaredRelationSpecs[relation.Kind+"\x00"+relation.Name]
		if !ok || declared.ToKind != plugin.MediaKindName {
			return nil, fmt.Errorf("hydration local relation %q from entity kind %q is not a declared Media relation", relation.Name, relation.Kind)
		}
		owned[relation.Kind+"\x00"+relation.Name] = true
		validated.OwnedRelations = append(validated.OwnedRelations, relation)
	}
	relations, err := a.validatePlan(HydrationPlan{Relations: local.Relations}, marker)
	if err != nil {
		return nil, err
	}
	for _, relation := range relations.Relations {
		if relation.ToEntity != nil {
			return nil, fmt.Errorf("hydration local relation %q must target the Media", relation.Name)
		}
		if relation.RequiresRelation != "" {
			return nil, fmt.Errorf("hydration local relation %q cannot require another relation", relation.Name)
		}
		if !owned[relation.From.Kind+"\x00"+relation.Name] {
			return nil, fmt.Errorf("hydration local relation %q from entity kind %q is not one the sidecar owns", relation.Name, relation.From.Kind)
		}
	}
	validated.Relations = relations.Relations
	return validated, nil
}

// resolveLocalRelations ensures each stated relation's source Entity and
// returns the rows to write. The Entities are shared, so one left behind by a
// failed apply is harmless.
func (a *hydrationAdapter) resolveLocalRelations(ctx context.Context, mediaID store.MediaID, libraryID store.LibraryID, relations []HydrationRelation) ([]store.EntityRelation, error) {
	if len(relations) == 0 {
		return nil, nil
	}
	if len(a.entityScopes) > 0 || a.foldStore != nil {
		a.scopeMu.Lock()
		defer a.scopeMu.Unlock()
	}
	resolved := map[string]store.EntityID{}
	out := make([]store.EntityRelation, 0, len(relations))
	for _, mapped := range relations {
		relation, ok, err := a.resolveRelation(ctx, mediaID, libraryID, mapped, resolved)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, relation)
		}
	}
	return out, nil
}
