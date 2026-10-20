package graph

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Bulk metadata edit resolvers. Dispatch for these fields is a
// self-contained, manager-gated group in graph.go's execute(); the methods live
// here to keep the chokepoint edit localized (a sibling agent is concurrently
// adding its own dispatch blocks).
//
// These are manage-area mutations over many Media at once, so the whole group is
// MANAGER-role gated like the other Console curation mutations. The dry-run
// preview (bulkEditPreview) returns the full old→new change-set; bulkEditApply
// commits it as one batch = one undo unit, returning the batch id; undoBatch
// (shared with file-operations, graph_fileops.go) reverses any batch by id.
// Every read and write is reveal-and-Library filtered through
// scope.LibraryFilter() — a Media the caller cannot see is silently absent from
// the change-set, never an existence oracle.

// BulkEditFieldInfo is one field value on the wire, stringified per type like
// EntityFieldInfo (the shared field-value shape).
type BulkEditFieldInfo struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

// BulkEditChangeInfo is one Media's preview row: its identity plus the field
// values before (only the touched fields that already had a value) and the new
// values every touched field will hold.
type BulkEditChangeInfo struct {
	MediaID   string              `json:"mediaID"`
	LibraryID string              `json:"libraryID"`
	Title     string              `json:"title"`
	Old       []BulkEditFieldInfo `json:"old"`
	New       []BulkEditFieldInfo `json:"new"`
}

// BatchResultInfo reports an applied (or undone) batch: the batch id (the undo
// handle; empty when nothing was affected), the op kind, and the affected
// count.
type BatchResultInfo struct {
	BatchID  string `json:"batchID"`
	Kind     string `json:"kind"`
	Affected int    `json:"affected"`
}

func bulkEditFieldInfo(field store.MediaField) BulkEditFieldInfo {
	return BulkEditFieldInfo{
		Name:  field.Name,
		Type:  string(field.Type),
		Value: fieldValueString(field.Type, field.Text, field.Number, field.Flag, field.Date),
	}
}

func bulkEditFieldInfos(fields []store.MediaField) []BulkEditFieldInfo {
	out := make([]BulkEditFieldInfo, len(fields))
	for i, f := range fields {
		out[i] = bulkEditFieldInfo(f)
	}
	return out
}

func batchResultInfo(batch store.BulkOpBatch) BatchResultInfo {
	return BatchResultInfo{BatchID: batch.ID, Kind: batch.Kind, Affected: batch.Affected}
}

// bulkEditPreview resolves the dry-run change-set a bulk edit WOULD produce.
// Pure read; no mutation, no journal.
func (h *Handler) bulkEditPreview(ctx context.Context, scope auth.Scope, req request) ([]BulkEditChangeInfo, error) {
	ids, fields, err := bulkEditArgs(req)
	if err != nil {
		return nil, err
	}
	changes, err := h.store.BulkEditPreview(ctx, ids, fields, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	out := make([]BulkEditChangeInfo, len(changes))
	for i, c := range changes {
		out[i] = BulkEditChangeInfo{
			MediaID:   string(c.MediaID),
			LibraryID: string(c.LibraryID),
			Title:     c.Title,
			Old:       bulkEditFieldInfos(c.Old),
			New:       bulkEditFieldInfos(c.New),
		}
	}
	return out, nil
}

// bulkEditApply commits a bulk edit as one undo-able batch and returns the
// batch id (the undo handle) and the affected count, logging one
// media.bulk_edit activity entry for the batch.
func (h *Handler) bulkEditApply(ctx context.Context, scope auth.Scope, req request) (BatchResultInfo, error) {
	ids, fields, err := bulkEditArgs(req)
	if err != nil {
		return BatchResultInfo{}, err
	}
	batch, err := h.store.BulkEditApply(ctx, ids, fields, scope.LibraryFilter(), time.Now().UTC())
	if err != nil {
		return BatchResultInfo{}, err
	}
	if batch.ID != "" {
		if err := h.logActivity(ctx, scope, store.ActivityMediaBulkEdit, "batch", batch.ID, ""); err != nil {
			return BatchResultInfo{}, err
		}
	}
	return batchResultInfo(batch), nil
}

// undoBatch reverses an applied batch by id (ADR-0009 §6, shared verb across
// bulk-edit and file-operations). Logs one media.batch_undo activity entry.
func (h *Handler) undoBatch(ctx context.Context, scope auth.Scope, req request) (BatchResultInfo, error) {
	batchID := trimmedVariable(req, "batchID")
	if batchID == "" {
		return BatchResultInfo{}, clientError("batchID is required")
	}
	batch, err := h.store.UndoBatch(ctx, batchID, scope.LibraryFilter(), time.Now().UTC())
	if err != nil {
		return BatchResultInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityBatchUndo, "batch", batch.ID, ""); err != nil {
		return BatchResultInfo{}, err
	}
	return batchResultInfo(batch), nil
}

// bulkEditArgs parses the shared (mediaIDs, fields) argument pair: a non-empty
// id list and at least one well-formed field value.
func bulkEditArgs(req request) ([]store.MediaID, []store.MediaField, error) {
	rawIDs := stringSliceVar(req, "mediaIDs")
	if len(rawIDs) == 0 {
		return nil, nil, clientError("mediaIDs is required")
	}
	ids := make([]store.MediaID, len(rawIDs))
	for i, id := range rawIDs {
		ids[i] = store.MediaID(id)
	}
	fields, err := parseBulkEditFields(req)
	if err != nil {
		return nil, nil, err
	}
	if len(fields) == 0 {
		return nil, nil, clientError("at least one field is required")
	}
	return ids, fields, nil
}

// parseBulkEditFields reads the `fields` argument — a list of {name, type,
// value} objects — into typed store.MediaField values. A malformed entry is a
// client error (a silently dropped field would quietly under-apply the edit).
func parseBulkEditFields(req request) ([]store.MediaField, error) {
	raw, _ := req.Variables["fields"].([]any)
	fields := make([]store.MediaField, 0, len(raw))
	for _, entry := range raw {
		obj, ok := entry.(map[string]any)
		if !ok {
			return nil, clientError("each field must be an object")
		}
		name, _ := obj["name"].(string)
		typeName, _ := obj["type"].(string)
		name = strings.TrimSpace(name)
		typeName = strings.TrimSpace(typeName)
		if name == "" || typeName == "" {
			return nil, clientError("each field requires name and type")
		}
		field := store.MediaField{Name: name, Type: store.EntityFieldType(typeName)}
		switch field.Type {
		case store.EntityFieldText, store.EntityFieldURL:
			field.Text, _ = obj["value"].(string)
		case store.EntityFieldNumber:
			n, err := numberValue(obj["value"])
			if err != nil {
				return nil, clientError("field " + name + " requires a numeric value")
			}
			field.Number = n
		case store.EntityFieldFlag:
			field.Flag, _ = obj["value"].(bool)
		case store.EntityFieldDate:
			t, err := dateValue(obj["value"])
			if err != nil {
				return nil, clientError("field " + name + " requires an ISO-8601 date value")
			}
			field.Date = t
		default:
			return nil, clientError("field " + name + " has an unknown type")
		}
		fields = append(fields, field)
	}
	return fields, nil
}

func numberValue(value any) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(strings.TrimSpace(v), 64)
	}
	return 0, clientError("not a number")
}

func dateValue(value any) (time.Time, error) {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return time.Time{}, clientError("not a date")
	}
	text = strings.TrimSpace(text)
	if t, err := time.Parse(time.RFC3339, text); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse("2006-01-02", text)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}
