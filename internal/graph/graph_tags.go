package graph

import (
	"context"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// TagInfo is one first-class Core Tag exposed to Manage.
type TagInfo struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ParentTagID     string `json:"parentTagID,omitempty"`
	TrashedAt       string `json:"trashedAt,omitempty"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
	AssignmentCount int    `json:"assignmentCount"`
}

// TagAssignmentInfo is the live first-class Tag set for one target.
type TagAssignmentInfo struct {
	TargetType string    `json:"targetType"`
	TargetID   string    `json:"targetID"`
	LibraryID  string    `json:"libraryID"`
	Tags       []TagInfo `json:"tags"`
}

// TagBulkAssignmentInfo summarizes one selected-target bulk Tag mutation.
type TagBulkAssignmentInfo struct {
	Affected int       `json:"affected"`
	Tags     []TagInfo `json:"tags"`
}

func (h *Handler) tags(ctx context.Context, req request) ([]TagInfo, error) {
	tags, err := h.store.Tags(ctx, boolVar(req, "includeTrashed"))
	if err != nil {
		return nil, err
	}
	return tagInfos(tags), nil
}

func (h *Handler) tagAssignments(ctx context.Context, scope auth.Scope, req request) (TagAssignmentInfo, error) {
	assignment, err := h.store.TagAssignments(ctx, tagTargetKind(req), trimmedVariable(req, "targetID"), scope.LibraryFilter())
	if err != nil {
		return TagAssignmentInfo{}, err
	}
	return tagAssignmentInfo(assignment), nil
}

func (h *Handler) createTag(ctx context.Context, scope auth.Scope, req request) (TagInfo, error) {
	tag, err := h.store.CreateTag(ctx, trimmedVariable(req, "name"), store.TagID(trimmedVariable(req, "parentTagID")), time.Now().UTC())
	if err != nil {
		return TagInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityTagCreate, "tag", string(tag.ID), ""); err != nil {
		return TagInfo{}, err
	}
	return tagInfo(tag), nil
}

func (h *Handler) updateTag(ctx context.Context, scope auth.Scope, req request) (TagInfo, error) {
	tag, err := h.store.UpdateTag(ctx, store.TagID(trimmedVariable(req, "tagID")), trimmedVariable(req, "name"), store.TagID(trimmedVariable(req, "parentTagID")), time.Now().UTC())
	if err != nil {
		return TagInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityTagUpdate, "tag", string(tag.ID), ""); err != nil {
		return TagInfo{}, err
	}
	return tagInfo(tag), nil
}

func (h *Handler) deleteTag(ctx context.Context, scope auth.Scope, req request) (TagInfo, error) {
	tag, err := h.store.DeleteTag(ctx, store.TagID(trimmedVariable(req, "tagID")), time.Now().UTC())
	if err != nil {
		return TagInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityTagDelete, "tag", string(tag.ID), ""); err != nil {
		return TagInfo{}, err
	}
	return tagInfo(tag), nil
}

func (h *Handler) restoreTag(ctx context.Context, scope auth.Scope, req request) (TagInfo, error) {
	tag, err := h.store.RestoreTag(ctx, store.TagID(trimmedVariable(req, "tagID")), time.Now().UTC())
	if err != nil {
		return TagInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityTagRestore, "tag", string(tag.ID), ""); err != nil {
		return TagInfo{}, err
	}
	return tagInfo(tag), nil
}

func (h *Handler) assignTag(ctx context.Context, scope auth.Scope, req request) (TagAssignmentInfo, error) {
	assignment, err := h.store.AssignTag(ctx, store.TagID(trimmedVariable(req, "tagID")), tagTargetKind(req), trimmedVariable(req, "targetID"), scope.LibraryFilter())
	if err != nil {
		return TagAssignmentInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityTagAssign, string(assignment.TargetKind), assignment.TargetID, assignment.LibraryID); err != nil {
		return TagAssignmentInfo{}, err
	}
	return tagAssignmentInfo(assignment), nil
}

func (h *Handler) unassignTag(ctx context.Context, scope auth.Scope, req request) (TagAssignmentInfo, error) {
	assignment, err := h.store.UnassignTag(ctx, store.TagID(trimmedVariable(req, "tagID")), tagTargetKind(req), trimmedVariable(req, "targetID"), scope.LibraryFilter())
	if err != nil {
		return TagAssignmentInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityTagAssign, string(assignment.TargetKind), assignment.TargetID, assignment.LibraryID); err != nil {
		return TagAssignmentInfo{}, err
	}
	return tagAssignmentInfo(assignment), nil
}

func (h *Handler) bulkAssignTags(ctx context.Context, scope auth.Scope, req request) (TagBulkAssignmentInfo, error) {
	rawTagIDs := stringSliceVar(req, "tagIDs")
	tagIDs := make([]store.TagID, 0, len(rawTagIDs))
	for _, id := range rawTagIDs {
		tagIDs = append(tagIDs, store.TagID(id))
	}
	result, err := h.store.BulkAssignTags(ctx, tagIDs, tagTargetKind(req), stringSliceVar(req, "targetIDs"), store.TagBulkMode(strings.TrimSpace(stringVar(req, "mode"))), scope.LibraryFilter())
	if err != nil {
		return TagBulkAssignmentInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityTagBulkAssign, "batch", stringVar(req, "mode"), ""); err != nil {
		return TagBulkAssignmentInfo{}, err
	}
	return TagBulkAssignmentInfo{Affected: result.Affected, Tags: tagInfos(result.Tags)}, nil
}

func tagTargetKind(req request) store.TagTargetKind {
	return store.TagTargetKind(strings.TrimSpace(stringVar(req, "targetType")))
}

func tagInfos(tags []store.Tag) []TagInfo {
	out := make([]TagInfo, len(tags))
	for i, tag := range tags {
		out[i] = tagInfo(tag)
	}
	return out
}

func tagInfo(tag store.Tag) TagInfo {
	info := TagInfo{
		ID:              string(tag.ID),
		Name:            tag.Name,
		ParentTagID:     string(tag.ParentID),
		CreatedAt:       tag.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:       tag.UpdatedAt.UTC().Format(time.RFC3339),
		AssignmentCount: tag.AssignmentCount,
	}
	if tag.TrashedAt != nil {
		info.TrashedAt = tag.TrashedAt.UTC().Format(time.RFC3339)
	}
	return info
}

func tagAssignmentInfo(assignment store.TagAssignment) TagAssignmentInfo {
	return TagAssignmentInfo{
		TargetType: string(assignment.TargetKind),
		TargetID:   assignment.TargetID,
		LibraryID:  string(assignment.LibraryID),
		Tags:       tagInfos(assignment.Tags),
	}
}
