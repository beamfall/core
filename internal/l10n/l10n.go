// Package l10n renders the server's own user-facing text in the request's
// locale (CORE-2102). The locale list comes from the vendored shared locale
// declaration (locales.json, CORE-2104 schema); each locale has a flat catalog
// of Key to template in catalog/<tag>.json, with {name} placeholders.
//
// Most server text is not rendered here: GraphQL errors and REST diagnostics
// carry stable codes and stable English text that clients branch on. See
// docs/architecture/server-text-localization.md for the classification.
package l10n

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strings"

	"golang.org/x/text/language"
)

// Locale status values from the shared declaration.
const (
	StatusShipped    = "shipped"
	StatusInProgress = "in-progress"
)

// Locale is one entry of the shared locale declaration.
type Locale struct {
	Tag         string `json:"tag"`
	EnglishName string `json:"englishName"`
	NativeName  string `json:"nativeName"`
	Direction   string `json:"direction"`
	Status      string `json:"status"`
}

// Declaration is the shared locale declaration.
type Declaration struct {
	SchemaVersion string   `json:"schemaVersion"`
	SourceLocale  string   `json:"sourceLocale"`
	Locales       []Locale `json:"locales"`
}

//go:embed locales.json
var declarationJSON []byte

//go:embed catalog/*.json
var catalogFiles embed.FS

var (
	declaration Declaration
	catalogs    map[string]map[Key]string
	shipped     []string
)

func init() {
	decl, cats, err := load()
	if err != nil {
		panic("l10n: embedded locale data: " + err.Error())
	}
	declaration, catalogs = decl, cats
	for _, l := range decl.Locales {
		if l.Status == StatusShipped {
			shipped = append(shipped, l.Tag)
		}
	}
}

func load() (Declaration, map[string]map[Key]string, error) {
	var decl Declaration
	if err := json.Unmarshal(declarationJSON, &decl); err != nil {
		return decl, nil, fmt.Errorf("locales.json: %w", err)
	}
	if decl.SchemaVersion != "1" {
		return decl, nil, fmt.Errorf("locales.json: unsupported schemaVersion %q", decl.SchemaVersion)
	}
	cats := make(map[string]map[Key]string, len(decl.Locales))
	sourceDeclared := false
	for _, l := range decl.Locales {
		if l.Status != StatusShipped && l.Status != StatusInProgress {
			return decl, nil, fmt.Errorf("locales.json: %s has unknown status %q", l.Tag, l.Status)
		}
		if l.Tag == decl.SourceLocale {
			sourceDeclared = true
		}
		raw, err := catalogFiles.ReadFile("catalog/" + l.Tag + ".json")
		if err != nil {
			return decl, nil, fmt.Errorf("catalog for %s: %w", l.Tag, err)
		}
		var cat map[Key]string
		if err := json.Unmarshal(raw, &cat); err != nil {
			return decl, nil, fmt.Errorf("catalog/%s.json: %w", l.Tag, err)
		}
		cats[l.Tag] = cat
	}
	if !sourceDeclared {
		return decl, nil, fmt.Errorf("locales.json: source locale %q is not declared", decl.SourceLocale)
	}
	return decl, cats, nil
}

// Source is the source locale every catalog falls back to.
func Source() string { return declaration.SourceLocale }

// Shipped lists the locales offered to users, in declaration order.
func Shipped() []string { return append([]string(nil), shipped...) }

// Locales returns the whole declaration's locale list, in-progress included.
func Locales() []Locale { return append([]Locale(nil), declaration.Locales...) }

// maxAcceptLanguage bounds the header bytes parsed; real headers are far shorter.
const maxAcceptLanguage = 512

// Negotiate picks the locale for server-rendered text: the profile preference
// when it names a shipped locale, else the first acceptable Accept-Language
// entry that does, else the source locale. A regional tag matches its language
// (pt-BR resolves to pt), and an entry with q=0 is never chosen.
func Negotiate(preference, acceptLanguage string) string {
	if tag, err := language.Parse(strings.TrimSpace(preference)); err == nil {
		if locale, ok := match(tag); ok {
			return locale
		}
	}
	if len(acceptLanguage) > maxAcceptLanguage {
		return Source()
	}
	tags, weights, err := language.ParseAcceptLanguage(acceptLanguage)
	if err != nil {
		return Source()
	}
	for i, tag := range tags {
		if weights[i] <= 0 {
			continue
		}
		if locale, ok := match(tag); ok {
			return locale
		}
	}
	return Source()
}

// match maps a requested tag to a shipped locale: an exact tag first, then
// the shipped locale with the same base language and no region or script.
func match(tag language.Tag) (string, bool) {
	if tag == language.Und {
		return "", false
	}
	for _, locale := range shipped {
		if language.Make(locale) == tag {
			return locale, true
		}
	}
	base, confidence := tag.Base()
	if confidence == language.No {
		return "", false
	}
	for _, locale := range shipped {
		candidate := language.Make(locale)
		if candidateBase, _ := candidate.Base(); candidateBase == base && candidate.String() == candidateBase.String() {
			return locale, true
		}
	}
	return "", false
}

type localeKey struct{}

// WithLocale returns ctx carrying the negotiated locale.
func WithLocale(ctx context.Context, locale string) context.Context {
	return context.WithValue(ctx, localeKey{}, locale)
}

// FromContext returns the locale WithLocale stored, or the source locale.
func FromContext(ctx context.Context) string {
	if locale, ok := ctx.Value(localeKey{}).(string); ok && locale != "" {
		return locale
	}
	return Source()
}

// Args fills a template's {name} placeholders.
type Args map[string]string

// Text renders key in locale, falling back per key to the source locale.
// An unknown key renders as itself; tests keep that from shipping.
func Text(locale string, key Key, args Args) string {
	template, ok := catalogs[locale][key]
	if !ok || template == "" {
		template, ok = catalogs[Source()][key]
	}
	if !ok {
		return string(key)
	}
	if len(args) == 0 {
		return template
	}
	pairs := make([]string, 0, 2*len(args))
	for name, value := range args {
		pairs = append(pairs, "{"+name+"}", value)
	}
	return strings.NewReplacer(pairs...).Replace(template)
}

// LocaleMap renders key in every shipped locale, for wire fields that carry a
// locale map the client resolves itself (the manifest's titles).
func LocaleMap(key Key) map[string]string {
	out := make(map[string]string, len(shipped))
	for _, locale := range shipped {
		out[locale] = Text(locale, key, nil)
	}
	return out
}

// Error is a refusal whose message is server-localized text. Error() is the
// source-locale text from Err, so logs, error matching and code mapping keep
// working on the English message; a response renders Localize instead.
type Error struct {
	Key  Key
	Args Args
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }

// Unwrap exposes the wrapped error to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Err }

// Localize renders the message in locale.
func (e *Error) Localize(locale string) string { return Text(locale, e.Key, e.Args) }
