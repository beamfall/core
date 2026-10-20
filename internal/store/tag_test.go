package store

import (
	"context"
	"testing"
	"time"
)

func TestTagHierarchyDeleteRestoreAndCycle(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Unix(100, 0).UTC()

	parent, err := st.CreateTag(ctx, "Mood", "", now)
	if err != nil {
		t.Fatalf("CreateTag parent: %v", err)
	}
	child, err := st.CreateTag(ctx, "Noir", parent.ID, now)
	if err != nil {
		t.Fatalf("CreateTag child: %v", err)
	}
	if child.ParentID != parent.ID {
		t.Fatalf("child parent = %q, want %q", child.ParentID, parent.ID)
	}
	if _, err := st.UpdateTag(ctx, parent.ID, "Mood", child.ID, now); err == nil {
		t.Fatal("UpdateTag accepted a parent cycle")
	}
	deleted, err := st.DeleteTag(ctx, child.ID, now)
	if err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}
	if deleted.TrashedAt == nil {
		t.Fatal("deleted Tag missing trashed timestamp")
	}
	live, err := st.Tags(ctx, false)
	if err != nil {
		t.Fatalf("Tags live: %v", err)
	}
	if len(live) != 1 || live[0].ID != parent.ID {
		t.Fatalf("live tags = %#v, want only parent", live)
	}
	restored, err := st.RestoreTag(ctx, child.ID, now.Add(time.Second))
	if err != nil {
		t.Fatalf("RestoreTag: %v", err)
	}
	if restored.TrashedAt != nil {
		t.Fatalf("restored Tag still trashed: %#v", restored)
	}
}

func TestTagAssignmentsRevealGateAndBulkReplace(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}}
	revealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}
	now := time.Unix(200, 0).UTC()

	mood, err := st.CreateTag(ctx, "Mood", "", now)
	if err != nil {
		t.Fatalf("CreateTag mood: %v", err)
	}
	pace, err := st.CreateTag(ctx, "Pace", "", now)
	if err != nil {
		t.Fatalf("CreateTag pace: %v", err)
	}
	if _, err := st.AssignTag(ctx, mood.ID, TagTargetMedia, "media-archive", filter); err == nil {
		t.Fatal("AssignTag allowed restricted media without reveal")
	}
	if _, err := st.AssignTag(ctx, mood.ID, TagTargetMedia, "media-alpha", filter); err != nil {
		t.Fatalf("AssignTag media-alpha: %v", err)
	}
	assignment, err := st.TagAssignments(ctx, TagTargetMedia, "media-alpha", filter)
	if err != nil {
		t.Fatalf("TagAssignments: %v", err)
	}
	if len(assignment.Tags) != 1 || assignment.Tags[0].ID != mood.ID {
		t.Fatalf("assignment tags = %#v, want mood", assignment.Tags)
	}
	result, err := st.BulkAssignTags(ctx, []TagID{pace.ID}, TagTargetMedia, []string{"media-alpha", "media-archive"}, TagBulkReplace, revealed)
	if err != nil {
		t.Fatalf("BulkAssignTags replace: %v", err)
	}
	if result.Affected != 2 {
		t.Fatalf("bulk affected = %d, want 2", result.Affected)
	}
	assignment, err = st.TagAssignments(ctx, TagTargetMedia, "media-alpha", filter)
	if err != nil {
		t.Fatalf("TagAssignments after replace: %v", err)
	}
	if len(assignment.Tags) != 1 || assignment.Tags[0].ID != pace.ID {
		t.Fatalf("assignment after replace = %#v, want pace only", assignment.Tags)
	}
}

func TestMarkerCoreTagsDoNotReplaceMarkerDisplayTags(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	now := time.Unix(300, 0).UTC()
	marker, err := st.CreateMarker(ctx, Marker{
		MediaID:      "media-alpha",
		StartSeconds: 10,
		Tags:         []string{"display-tag"},
	}, filter, now)
	if err != nil {
		t.Fatalf("CreateMarker: %v", err)
	}
	tag, err := st.CreateTag(ctx, "Managed", "", now)
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	if _, err := st.AssignTag(ctx, tag.ID, TagTargetMarker, string(marker.ID), filter); err != nil {
		t.Fatalf("AssignTag marker: %v", err)
	}
	markers, err := st.MarkersForMedia(ctx, "media-alpha", filter)
	if err != nil {
		t.Fatalf("Markers: %v", err)
	}
	if len(markers) != 1 || len(markers[0].Tags) != 1 || markers[0].Tags[0] != "display-tag" {
		t.Fatalf("marker display tags changed: %#v", markers)
	}
	assignment, err := st.TagAssignments(ctx, TagTargetMarker, string(marker.ID), filter)
	if err != nil {
		t.Fatalf("TagAssignments marker: %v", err)
	}
	if len(assignment.Tags) != 1 || assignment.Tags[0].ID != tag.ID {
		t.Fatalf("core marker tags = %#v, want managed tag", assignment.Tags)
	}
}
