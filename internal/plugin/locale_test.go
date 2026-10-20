package plugin

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLShorthand(t *testing.T) {
	if got := L("Fresh Entries"); len(got) != 1 || got["en"] != "Fresh Entries" {
		t.Fatalf(`L("Fresh Entries") = %#v, want {"en": "Fresh Entries"}`, got)
	}
	if got := L(""); got != nil {
		t.Fatalf(`L("") = %#v, want nil`, got)
	}
}

func TestLocaleStringResolve(t *testing.T) {
	full := LocaleString{"en": "Fresh Entries", "fr": "Entrées récentes", "fr-ca": "Entrées fraîches"}
	cases := []struct {
		name   string
		value  LocaleString
		locale string
		want   string
	}{
		{"empty locale means English", full, "", "Fresh Entries"},
		{"exact match", full, "fr", "Entrées récentes"},
		{"exact regional match", full, "fr-CA", "Entrées fraîches"},
		{"best-match truncation fr-FR to fr", full, "fr-FR", "Entrées récentes"},
		{"multi-subtag truncation", full, "fr-Latn-FR", "Entrées récentes"},
		{"underscore form", full, "fr_CA", "Entrées fraîches"},
		{"unknown locale falls back to English", full, "de-DE", "Fresh Entries"},
		{"shorthand serves every locale", L("Fresh Entries"), "fr-CA", "Fresh Entries"},
		{"absent map resolves empty", nil, "fr", ""},
		// Invalid per Validate, but resolution stays deterministic: the
		// lexicographically smallest tag wins when "en" is missing.
		{"no English anchor", LocaleString{"fr": "Entrées", "pt": "Entradas"}, "de", "Entrées"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.value.Resolve(c.locale); got != c.want {
				t.Fatalf("Resolve(%q) = %q, want %q", c.locale, got, c.want)
			}
		})
	}
}

func TestLocaleStringJSON(t *testing.T) {
	cases := []struct {
		name  string
		value LocaleString
		want  string
	}{
		{"English-only marshals as the plain-string shorthand", L("Fresh Entries"), `"Fresh Entries"`},
		{"translations marshal as the full map", LocaleString{"fr": "Entrées récentes", "en": "Fresh Entries"},
			`{"en":"Fresh Entries","fr":"Entrées récentes"}`},
		{"absent marshals as the empty string", nil, `""`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data, err := json.Marshal(c.value)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != c.want {
				t.Fatalf("marshal = %s, want %s", data, c.want)
			}
			// Both wire forms decode back to an equivalent value.
			var back LocaleString
			if err := json.Unmarshal(data, &back); err != nil {
				t.Fatal(err)
			}
			if back.Resolve("") != c.value.Resolve("") || back.Resolve("fr") != c.value.Resolve("fr") {
				t.Fatalf("round-trip = %#v, want equivalent of %#v", back, c.value)
			}
		})
	}

	var invalid LocaleString
	if err := json.Unmarshal([]byte(`42`), &invalid); err == nil {
		t.Fatal("numeric locale string accepted")
	}
}

func TestValidateLocaleString(t *testing.T) {
	cases := []struct {
		name  string
		value LocaleString
		want  string // "" means valid
	}{
		{"absent is legal", nil, ""},
		{"shorthand is legal", L("Fresh Entries"), ""},
		{"full map is legal", LocaleString{"en": "Fresh Entries", "fr-ca": "Entrées fraîches"}, ""},
		{"missing English anchor", LocaleString{"fr": "Entrées"}, `"en" entry`},
		{"blank English anchor", LocaleString{"en": "  "}, `"en" entry`},
		{"blank translation", LocaleString{"en": "Fresh Entries", "fr": " "}, "blank"},
		{"uppercase tag", LocaleString{"en": "Fresh Entries", "FR": "Entrées"}, "not a lowercase BCP-47 tag"},
		{"malformed tag", LocaleString{"en": "Fresh Entries", "f": "Entrées"}, "not a lowercase BCP-47 tag"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateLocaleString(c.value)
			if c.want == "" {
				if err != nil {
					t.Fatalf("validateLocaleString = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("validateLocaleString = %v, want error containing %q", err, c.want)
			}
		})
	}
}
