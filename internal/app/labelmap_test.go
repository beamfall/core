package app

import (
	"context"
	"testing"
	"time"

	"github.com/beamfall/core/internal/apptest/plugintest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

func TestApplyLabelMapsPromotesTagsQueuesEntitiesAndRecordsFailures(t *testing.T) {
	st := enrichOpenSeed(t)
	ctx := context.Background()
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	bundle := plugin.Bundle{
		ID:          "starlog",
		Version:     "1.0.0",
		DisplayName: plugin.L("Starlog"),
		MediaLabel:  plugin.L("Entry"),
		EntityKinds: []plugin.EntityKind{{
			Name:      "navigator",
			Archetype: "person",
			Relations: []plugin.RelationSpec{{
				Name: "logged-by", ToKind: plugin.MediaKindName,
			}},
		}},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
		LabelMaps: []plugin.LabelMapSpec{
			{Label: "Space Walk", Tag: "spacewalk", MinConfidence: 0.7},
			{Label: "space walk", Kind: "navigator", Relation: "logged-by", EntityName: "EVA", MinConfidence: 0.7},
		},
	}
	bundle = plugintest.Compatible(bundle)

	got, err := ApplyLabelMaps(ctx, st, bundle, "media-alpha", []plugin.Label{
		{Name: "SPACE   WALK", Confidence: 0.9},
		{Name: "space", Confidence: 0.99},
		{Name: "space walk", Confidence: 0.6},
	}, 0.5, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 1 || len(got.Entities) != 1 || got.Unmatched != 2 {
		t.Fatalf("result = %#v, want 1 tag, 1 entity proposal, 2 unmatched", got)
	}
	fields, err := st.MediaFields(ctx, "media-alpha", filter)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := appFieldText(fields, "tag:spacewalk"); !ok || v != "spacewalk" {
		t.Fatalf("tag:spacewalk = %q (ok=%v), want spacewalk", v, ok)
	}
	media, err := st.Media(ctx, "media-alpha", filter)
	if err != nil {
		t.Fatal(err)
	}
	if media.MutatedBy != store.MutationSourceAI {
		t.Fatalf("mutatedBy = %q, want %q", media.MutatedBy, store.MutationSourceAI)
	}
	queue, err := st.PendingAITagQueue(ctx, filter, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 1 || queue[0].Tag.TagKind != "entity:navigator:logged-by" || queue[0].Tag.Value != "EVA" || queue[0].Tag.RunID == "" {
		t.Fatalf("pending queue = %#v, want navigator/logged-by EVA", queue)
	}
	statuses, err := st.LibraryIndexStatuses(ctx, filter, labelMapStatusKind(bundle.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].Failures != 1 {
		t.Fatalf("labelmap statuses = %#v, want one visible failure row", statuses)
	}
}

func TestApplyLabelMapsRunRevertTombstonesAutoAcceptedTags(t *testing.T) {
	st := enrichOpenSeed(t)
	ctx := context.Background()
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	bundle := plugin.Bundle{
		ID:          "starlog",
		Version:     "1.0.0",
		DisplayName: plugin.L("Starlog"),
		MediaLabel:  plugin.L("Entry"),
		Layouts:     []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
		LabelMaps:   []plugin.LabelMapSpec{{Label: "Space Walk", Tag: "spacewalk", MinConfidence: 0.7}},
	}
	bundle = plugintest.Compatible(bundle)

	if _, err := ApplyLabelMaps(ctx, st, bundle, "media-alpha", []plugin.Label{{Name: "space walk", Confidence: 0.9}}, 0.5, filter); err != nil {
		t.Fatal(err)
	}
	pending, err := st.PendingAITags(ctx, "media-alpha", filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].RunID == "" || pending[0].Status != store.PendingAITagApproved {
		t.Fatalf("pending tags = %#v, want one approved tag with run id", pending)
	}
	runID := pending[0].RunID
	if _, err := st.RevertEnrichmentRun(ctx, runID, filter, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	fields, err := st.MediaFields(ctx, "media-alpha", filter)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := appFieldText(fields, "tag:spacewalk"); ok {
		t.Fatalf("tag:spacewalk survived revert: %#v", fields)
	}

	if _, err := ApplyLabelMaps(ctx, st, bundle, "media-alpha", []plugin.Label{{Name: "space walk", Confidence: 0.9}}, 0.5, filter); err != nil {
		t.Fatal(err)
	}
	fields, err = st.MediaFields(ctx, "media-alpha", filter)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := appFieldText(fields, "tag:spacewalk"); ok {
		t.Fatalf("tombstoned tag:spacewalk was reapplied: %#v", fields)
	}
}

func appFieldText(fields []store.MediaField, name string) (string, bool) {
	for _, field := range fields {
		if field.Name == name {
			return field.Text, true
		}
	}
	return "", false
}
