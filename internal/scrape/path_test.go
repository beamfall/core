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
		"kinds": [{"id": 4, "slug": "cover", "group": "b"}, {"id": 7, "slug": "cover", "group": "a"}],
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
		{"kinds[slug=cover].id", "4", true},         // first match wins
		{"kinds[slug=cover,group=a].id", "7", true}, // every condition must hold
		{"kinds[id=7,slug=cover].group", "a", true}, // numbers compare as text
		{"kinds[slug=cover,group=c].id", "", false}, // no element matches all
		{"kinds[slug=cover,=a].id", "", false},      // malformed condition
		{"empty", "", false},                        // null is not a value
		{"nested", "", false},                       // objects are not scalars
		{"list", "", false},                         // arrays are not scalars
		{"absent", "", false},                       // missing key
		{"list.2.name", "", false},                  // index out of range
		{"list.x.name", "", false},                  // non-numeric index
		{"name.deeper", "", false},                  // descend into scalar
		{"nested.absent", "", false},                // missing nested key
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

// TestResolveAlternatives pins "||" map-path fallbacks (CORE-1128): the first
// alternative with a non-empty value wins, and the last is taken as is.
func TestResolveAlternatives(t *testing.T) {
	root, err := decodeJSON([]byte(`{"blank": "", "name": "Vega", "translations": [{"lang": "x", "name": "Wega"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(path string) (any, bool) { return resolveJSONScalar(root, path) }
	cases := []struct {
		path string
		want string
		ok   bool
	}{
		{"translations[lang=x].name||name", "Wega", true},
		{"translations[lang=y].name||name", "Vega", true},
		{"blank||name", "Vega", true},
		{"absent || name", "Vega", true},
		{"name||blank", "Vega", true},
		{"absent||blank", "", true},
		{"blank", "", true},
		{"absent||missing", "", false},
	}
	for _, tc := range cases {
		raw, ok := resolveAlternatives(tc.path, resolve)
		got, _ := scalarString(raw)
		if got != tc.want || ok != tc.ok {
			t.Errorf("resolveAlternatives(%q) = (%q, %v), want (%q, %v)", tc.path, got, ok, tc.want, tc.ok)
		}
	}
}
