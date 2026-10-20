package graphshape

import (
	"slices"
	"testing"
	"time"
)

type EmbeddedShape struct {
	Title string `json:"title"`
}

type sampleShape struct {
	EmbeddedShape
	Blob    []byte            `json:"blob,omitempty"`
	Dynamic map[string]string `json:"dynamic"`
	Pointer *string           `json:"pointer"`
	When    time.Time         `json:"when"`
	Hidden  string            `json:"-"`
}

func TestShapeMatchesJSONSelectionShape(t *testing.T) {
	shape := Shape(sampleShape{})
	if shape.Kind != "object" {
		t.Fatalf("kind = %q, want object", shape.Kind)
	}
	children := shape.Children
	var names []string
	for _, child := range children {
		names = append(names, child.Name)
		if child.Name != "dynamic" && child.Kind != "scalar" {
			t.Fatalf("%s kind = %q, want scalar", child.Name, child.Kind)
		}
	}
	if !slices.Equal(names, []string{"blob", "dynamic", "pointer", "title", "when"}) {
		t.Fatalf("children = %v", names)
	}
	if !children[0].Optional {
		t.Fatal("blob optional = false, want true")
	}
	if !children[0].Nullable {
		t.Fatal("blob nullable = false, want true")
	}
	if !children[2].Nullable {
		t.Fatal("pointer nullable = false, want true")
	}
}

type recursiveShape struct {
	Children []recursiveShape `json:"children,omitempty"`
}

func TestShapeAddsMapAndRecursiveElementMetadata(t *testing.T) {
	shape := Shape(sampleShape{})
	dynamic := shape.Children[1]
	if dynamic.Kind != "map" || dynamic.Elem == nil || dynamic.Elem.Kind != "scalar" {
		t.Fatalf("candidate dynamic = %+v, want map with scalar element", dynamic)
	}

	recursive := Shape(recursiveShape{}).Children[0]
	if recursive.Kind != "list" || recursive.Elem == nil || recursive.Elem.Kind != "object" || !recursive.Elem.Recursive {
		t.Fatalf("candidate children = %+v, want recursive object element", recursive)
	}
}
