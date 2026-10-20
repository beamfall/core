package graph

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestTagsGraphManagerCRUDRestoreAndActivity(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	body := `{"query":"mutation C($name:String!){ createTag(name:$name){ id name } }","variables":{"name":"Mood"}}`
	_, errs := post(t, handler, body, authorizeViewer)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "manager role required") {
		t.Fatalf("viewer createTag errors = %#v, want manager gate", errs)
	}

	data, errs := post(t, handler, body, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("createTag errors = %#v", errs)
	}
	var created TagInfo
	mustUnmarshal(t, data["createTag"], &created)
	if created.ID == "" || created.Name != "Mood" {
		t.Fatalf("createTag = %#v", created)
	}
	data, errs = post(t, handler,
		`{"query":"mutation U($tagID:String!,$name:String!){ updateTag(tagID:$tagID,name:$name){ id name } }","variables":{"tagID":"`+created.ID+`","name":"Mood Updated"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("updateTag errors = %#v", errs)
	}
	var updated TagInfo
	mustUnmarshal(t, data["updateTag"], &updated)
	if updated.Name != "Mood Updated" {
		t.Fatalf("updateTag = %#v, want renamed Tag", updated)
	}

	data, errs = post(t, handler,
		`{"query":"mutation D($tagID:String!){ deleteTag(tagID:$tagID){ id trashedAt } }","variables":{"tagID":"`+created.ID+`"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("deleteTag errors = %#v", errs)
	}
	var deleted TagInfo
	mustUnmarshal(t, data["deleteTag"], &deleted)
	if deleted.TrashedAt == "" {
		t.Fatalf("deleteTag = %#v, want trashedAt", deleted)
	}

	data, errs = post(t, handler,
		`{"query":"query { tags { id } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("tags errors = %#v", errs)
	}
	var live []TagInfo
	mustUnmarshal(t, data["tags"], &live)
	if len(live) != 0 {
		t.Fatalf("live tags = %#v, want none", live)
	}

	data, errs = post(t, handler,
		`{"query":"mutation R($tagID:String!){ restoreTag(tagID:$tagID){ id trashedAt } }","variables":{"tagID":"`+created.ID+`"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("restoreTag errors = %#v", errs)
	}
	var restored TagInfo
	mustUnmarshal(t, data["restoreTag"], &restored)
	if restored.TrashedAt != "" {
		t.Fatalf("restoreTag = %#v, want live", restored)
	}

	data, errs = post(t, handler,
		`{"query":"query { activityLog { action targetType targetID } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("activityLog errors = %#v", errs)
	}
	var activity []ActivityEntryInfo
	mustUnmarshal(t, data["activityLog"], &activity)
	seen := map[string]bool{}
	for _, entry := range activity {
		if entry.TargetID == created.ID {
			seen[entry.Action] = true
		}
	}
	for _, action := range []string{store.ActivityTagCreate, store.ActivityTagDelete, store.ActivityTagRestore} {
		if !seen[action] {
			t.Fatalf("activity for %s missing in %#v", action, activity)
		}
	}
}

func TestTagsGraphAssignmentBulkAndReveal(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	first, err := st.CreateTag(ctx, "First", "", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateTag(ctx, "Second", "", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)

	assignRestricted := `{"query":"mutation A($tagID:String!,$targetType:String!,$targetID:String!){ assignTag(tagID:$tagID,targetType:$targetType,targetID:$targetID){ targetID } }","variables":{"tagID":"` + string(first.ID) + `","targetType":"media","targetID":"media-archive"}}`
	_, errs := post(t, handler, assignRestricted, authorizeManager)
	if len(errs) == 0 || !strings.Contains(strings.ToLower(errs[0].Message), "not found") {
		t.Fatalf("restricted assign errors = %#v, want reveal-gated not found", errs)
	}

	data, errs := post(t, handler,
		`{"query":"mutation A($tagID:String!,$targetType:String!,$targetID:String!){ assignTag(tagID:$tagID,targetType:$targetType,targetID:$targetID){ targetID libraryID tags { id name } } }","variables":{"tagID":"`+string(first.ID)+`","targetType":"media","targetID":"media-alpha"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("assignTag errors = %#v", errs)
	}
	var assignment TagAssignmentInfo
	mustUnmarshal(t, data["assignTag"], &assignment)
	if assignment.TargetID != "media-alpha" || assignment.LibraryID != "library-local" || len(assignment.Tags) != 1 || assignment.Tags[0].ID != string(first.ID) {
		t.Fatalf("assignTag = %#v", assignment)
	}

	data, errs = post(t, handler,
		`{"query":"mutation B($tagIDs:[String!]!,$targetType:String!,$targetIDs:[String!]!,$mode:String!){ bulkAssignTags(tagIDs:$tagIDs,targetType:$targetType,targetIDs:$targetIDs,mode:$mode){ affected tags { id } } }","variables":{"tagIDs":["`+string(second.ID)+`"],"targetType":"media","targetIDs":["media-alpha"],"mode":"replace"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("bulkAssignTags errors = %#v", errs)
	}
	var bulk TagBulkAssignmentInfo
	mustUnmarshal(t, data["bulkAssignTags"], &bulk)
	if bulk.Affected != 1 || len(bulk.Tags) != 1 || bulk.Tags[0].ID != string(second.ID) {
		t.Fatalf("bulkAssignTags = %#v", bulk)
	}

	data, errs = post(t, handler,
		`{"query":"query T($targetType:String!,$targetID:String!){ tagAssignments(targetType:$targetType,targetID:$targetID){ targetID tags { id } } }","variables":{"targetType":"media","targetID":"media-alpha"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("tagAssignments errors = %#v", errs)
	}
	mustUnmarshal(t, data["tagAssignments"], &assignment)
	if len(assignment.Tags) != 1 || assignment.Tags[0].ID != string(second.ID) {
		t.Fatalf("tagAssignments after replace = %#v", assignment)
	}
	data, errs = post(t, handler,
		`{"query":"mutation U($tagID:String!,$targetType:String!,$targetID:String!){ unassignTag(tagID:$tagID,targetType:$targetType,targetID:$targetID){ targetID tags { id } } }","variables":{"tagID":"`+string(second.ID)+`","targetType":"media","targetID":"media-alpha"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("unassignTag errors = %#v", errs)
	}
	mustUnmarshal(t, data["unassignTag"], &assignment)
	if len(assignment.Tags) != 0 {
		t.Fatalf("unassignTag = %#v, want no tags", assignment)
	}
}

func TestTagsGraphRestrictedAssignmentWorksWhenManagerRevealed(t *testing.T) {
	st := storetest.Open(t)
	if err := st.EnrollDevice(t.Context(),
		store.Device{ID: "device-mgr2", Name: "Manager 2", Role: store.DeviceRoleManager, SecretHash: store.HashDeviceSecret("mgr2-secret")},
		store.Session{ID: "session-mgr-revealed", DeviceID: "device-mgr2", ProfileID: "profile-default", Revealed: true},
	); err != nil {
		t.Fatal(err)
	}
	authorizeRevealedManager := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-mgr-revealed")
		req.Header.Set("X-Beamfall-Device", "device-mgr2")
		req.Header.Set("X-Beamfall-Device-Secret", "mgr2-secret")
	}
	tag, err := st.CreateTag(t.Context(), "Restricted", "", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)

	data, errs := post(t, handler,
		`{"query":"mutation A($tagID:String!,$targetType:String!,$targetID:String!){ assignTag(tagID:$tagID,targetType:$targetType,targetID:$targetID){ targetID libraryID tags { id } } }","variables":{"tagID":"`+string(tag.ID)+`","targetType":"media","targetID":"media-archive"}}`,
		authorizeRevealedManager)
	if len(errs) != 0 {
		t.Fatalf("revealed assignTag errors = %#v", errs)
	}
	var assignment TagAssignmentInfo
	mustUnmarshal(t, data["assignTag"], &assignment)
	if assignment.TargetID != "media-archive" || assignment.LibraryID != "library-archive" {
		t.Fatalf("revealed assignment = %#v", assignment)
	}
}
