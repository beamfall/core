package conformance

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

func TestLabelMapsConformanceFixture(t *testing.T) {
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
	bundle = compatibleTestBundle(bundle)
	if err := plugin.Validate(bundle); err != nil {
		t.Fatal(err)
	}

	got, err := json.Marshal(plugin.ResolveLabelMaps(bundle, []plugin.Label{
		{Name: " SPACE   WALK ", Confidence: 0.9},
		{Name: "space", Confidence: 0.99},
		{Name: "space walk", Confidence: 0.6},
	}, 0.5))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"Tags":[{"Label":"Space Walk","Confidence":0.9,"Tag":"spacewalk","Kind":"","Relation":"","EntityName":""}],"Entities":[{"Label":"space walk","Confidence":0.9,"Tag":"","Kind":"navigator","Relation":"logged-by","EntityName":"EVA"}],"Unmatched":2}`
	if string(got) != want {
		t.Fatalf("LabelMaps fixture = %s, want %s", got, want)
	}

	st, err := store.Open(context.Background(), t.TempDir(), store.Options{SeedProfile: store.SeedProfileConformance})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	if _, err := app.ApplyLabelMaps(context.Background(), st, bundle, "media-alpha", []plugin.Label{
		{Name: " SPACE   WALK ", Confidence: 0.9},
		{Name: "space", Confidence: 0.99},
		{Name: "space walk", Confidence: 0.6},
	}, 0.5, filter); err != nil {
		t.Fatal(err)
	}
	fields, err := st.MediaFields(context.Background(), "media-alpha", filter)
	if err != nil {
		t.Fatal(err)
	}
	if fieldText(fields, "tag:spacewalk") != "spacewalk" {
		t.Fatalf("persisted tag fields = %#v, want tag:spacewalk=spacewalk", fields)
	}
	queue, err := st.PendingAITagQueue(context.Background(), filter, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 1 || queue[0].Tag.TagKind != "entity:navigator:logged-by" || queue[0].Tag.Value != "EVA" {
		t.Fatalf("entity confirm queue = %#v, want navigator/logged-by EVA", queue)
	}
	statuses, err := st.LibraryIndexStatuses(context.Background(), filter, store.EmbeddingKind("labelmap:"+string(bundle.ID)))
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].Failures != 1 {
		t.Fatalf("unmatched status = %#v, want one failure row", statuses)
	}
}

func fieldText(fields []store.MediaField, name string) string {
	for _, field := range fields {
		if field.Name == name {
			return field.Text
		}
	}
	return ""
}
