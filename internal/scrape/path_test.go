package scrape

import (
	"testing"
)

func TestResolveJSONPath(t *testing.T) {
	root, err := decodeJSON([]byte(`{
		"name": "Vega",
		"made": 1997,
		"big": 9007199254740993,
		"active": true,
		"empty": null,
		"nested": {"inner": {"deep": "value"}},
		"list": [{"name": "first"}, {"name": "second"}],
		"releases": [{"region": "US", "rating": "PG-13"}, {"region": "CA", "rating": "14A"}],
		"plain": ["a", "b"]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path string
		want string
		ok   bool
	}{
		{"name", "Vega", true},
		{"made", "1997", true},
		{"big", "9007199254740993", true}, // json.Number survives intact
		{"active", "true", true},
		{"nested.inner.deep", "value", true},
		{"list.0.name", "first", true},
		{"list.1.name", "second", true},
		{"plain.1", "b", true},
		{"releases[region=CA].rating", "14A", true},
		{"releases[region=GB].rating", "", false},
		{"empty", "", false},         // null is not a value
		{"nested", "", false},        // objects are not scalars
		{"list", "", false},          // arrays are not scalars
		{"absent", "", false},        // missing key
		{"list.2.name", "", false},   // index out of range
		{"list.x.name", "", false},   // non-numeric index
		{"name.deeper", "", false},   // descend into scalar
		{"nested.absent", "", false}, // missing nested key
	}
	for _, tc := range cases {
		got, ok := resolveJSONPath(root, tc.path)
		if got != tc.want || ok != tc.ok {
			t.Errorf("resolveJSONPath(%q) = (%q, %v), want (%q, %v)", tc.path, got, ok, tc.want, tc.ok)
		}
	}
}

func TestResolveXMLPath(t *testing.T) {
	root, err := parseXML([]byte(testFeed))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path string
		want string
		ok   bool
	}{
		{"channel.title", "Signal Watch", true},
		{"title", "Signal Watch", true},      // nearest-descendant match
		{"item.0.title", "Aurora Run", true}, // without spelling out channel.
		{"channel.item.1.title", "Beacon Drift", true},
		{"item.0.enclosure.url", "https://cdn.invalid/aurora.mp3", true}, // attribute fallback
		{"item.0.enclosure@type", "audio/mpeg", true},                    // explicit attribute
		{"itunes:image@href", "https://img.invalid/cover.jpg", true},     // prefixed name
		{"item.5.title", "", false},                                      // index out of range
		{"item.0.absent", "", false},                                     // no such child or attribute
		{"itunes:image@absent", "", false},                               // no such attribute
		{"item.0.enclosure", "", false},                                  // element has no text
		{"channel.title@x", "", false},                                   // attribute on text element
	}
	for _, tc := range cases {
		got, ok := resolveXMLPath(root, tc.path)
		if got != tc.want || ok != tc.ok {
			t.Errorf("resolveXMLPath(%q) = (%q, %v), want (%q, %v)", tc.path, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseXMLRejectsGarbage(t *testing.T) {
	if _, err := parseXML([]byte("<rss><unclosed")); err == nil {
		t.Fatal("expected parse error")
	}
}
