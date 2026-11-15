package l10n

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/text/language"
)

// identicalToSource lists entries a shipped locale may spell exactly as the
// source does because the word is the same in that language.
var identicalToSource = map[string]map[Key]bool{
	"fr": {WatchPartyParticipantFallback: true},
}

var placeholder = regexp.MustCompile(`\{[a-zA-Z]+\}`)

func placeholders(s string) []string {
	found := placeholder.FindAllString(s, -1)
	sort.Strings(found)
	return found
}

func TestDeclarationIsWellFormed(t *testing.T) {
	if len(Shipped()) == 0 {
		t.Fatal("no shipped locales declared")
	}
	if !slices.Contains(Shipped(), Source()) {
		t.Fatalf("source locale %q is not shipped", Source())
	}
	seen := map[string]bool{}
	for _, l := range Locales() {
		if seen[l.Tag] {
			t.Errorf("locale %q declared twice", l.Tag)
		}
		seen[l.Tag] = true
		if tag, err := language.Parse(l.Tag); err != nil || tag.String() != l.Tag {
			t.Errorf("locale %q is not a canonical BCP-47 tag (%v)", l.Tag, err)
		}
		if l.Direction != "ltr" && l.Direction != "rtl" {
			t.Errorf("locale %q has direction %q", l.Tag, l.Direction)
		}
		if l.EnglishName == "" || l.NativeName == "" {
			t.Errorf("locale %q is missing a name", l.Tag)
		}
	}
	entries, err := fs.ReadDir(catalogFiles, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if tag := strings.TrimSuffix(e.Name(), ".json"); !seen[tag] {
			t.Errorf("catalog/%s has no locale in locales.json", e.Name())
		}
	}
}

// TestVendoredDeclarationMatchesContract keeps internal/l10n/locales.json a
// byte-identical copy of the producer file in contracts/locales.
func TestVendoredDeclarationMatchesContract(t *testing.T) {
	contract, err := os.ReadFile(filepath.Join("..", "..", "contracts", "locales", "locales.json"))
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("contracts/locales/locales.json is not in this tree yet (CORE-2104)")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(contract, declarationJSON) {
		t.Fatal("internal/l10n/locales.json differs from contracts/locales/locales.json; copy the contract file byte for byte")
	}
}

func declaredKeys(t *testing.T) map[Key]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "keys.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[Key]bool{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for _, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				keys[Key(value)] = true
			}
		}
	}
	return keys
}

func TestCatalogKeysMatchDeclaredKeys(t *testing.T) {
	declared := declaredKeys(t)
	source := catalogs[Source()]
	for key := range declared {
		if _, ok := source[key]; !ok {
			t.Errorf("Key %q has no entry in catalog/%s.json", key, Source())
		}
	}
	for key := range source {
		if !declared[key] {
			t.Errorf("catalog/%s.json entry %q has no Key in keys.go", Source(), key)
		}
	}
}

func TestCatalogsAreCompleteAndConsistent(t *testing.T) {
	source := catalogs[Source()]
	for _, l := range Locales() {
		cat := catalogs[l.Tag]
		for key := range cat {
			if _, ok := source[key]; !ok {
				t.Errorf("catalog/%s.json entry %q is not in the source catalog", l.Tag, key)
			}
		}
		if l.Tag == Source() {
			continue
		}
		for key, want := range source {
			got, ok := cat[key]
			if !ok || strings.TrimSpace(got) == "" {
				if l.Status == StatusShipped {
					t.Errorf("shipped locale %s is missing %q", l.Tag, key)
				}
				continue
			}
			if !slices.Equal(placeholders(got), placeholders(want)) {
				t.Errorf("%s %q placeholders %v, source has %v", l.Tag, key, placeholders(got), placeholders(want))
			}
			if got == want && !identicalToSource[l.Tag][key] {
				t.Errorf("%s %q is the untranslated source text %q", l.Tag, key, got)
			}
		}
	}
}

func TestNegotiate(t *testing.T) {
	cases := []struct {
		preference, accept, want string
	}{
		{"", "", "en"},
		{"", "fr-CA,fr;q=0.9,en;q=0.8", "fr"},
		{"", "pt-BR,pt;q=0.9,en;q=0.5", "pt"},
		{"", "es-419", "es"},
		{"", "de-CH", "de"},
		{"", "ja", "en"},
		{"", "ja,de;q=0.4", "de"},
		{"", "fr;q=0, de", "de"},
		{"", "*", "en"},
		{"", "not a header;;q=x", "en"},
		{"", strings.Repeat("fr,", 400), "en"},
		{"es", "fr", "es"},
		{"ja", "fr", "fr"},
		{"garbage!", "de", "de"},
	}
	for _, tc := range cases {
		if got := Negotiate(tc.preference, tc.accept); got != tc.want {
			t.Errorf("Negotiate(%q, %q) = %q, want %q", tc.preference, tc.accept, got, tc.want)
		}
	}
}

func TestContextLocale(t *testing.T) {
	if got := FromContext(context.Background()); got != Source() {
		t.Fatalf("FromContext(empty) = %q", got)
	}
	if got := FromContext(WithLocale(context.Background(), "de")); got != "de" {
		t.Fatalf("FromContext(de) = %q", got)
	}
}

func TestTextFillsPlaceholdersAndFallsBack(t *testing.T) {
	if got, want := Text("de", LibraryRootNotFolder, Args{"path": "/m/{x}"}), "/m/{x} ist eine Datei, kein Ordner."; got != want {
		t.Fatalf("Text(de) = %q, want %q", got, want)
	}
	if got, want := Text("xx", WatchPartyParticipantFallback, nil), "Participant"; got != want {
		t.Fatalf("Text(undeclared locale) = %q, want source %q", got, want)
	}
}

type wrapped string

func (w wrapped) Error() string { return string(w) }

func TestErrorKeepsSourceTextAndLocalizes(t *testing.T) {
	args := Args{"path": "/media"}
	err := error(&Error{Key: LibraryRootMissing, Args: args, Err: wrapped(Text(Source(), LibraryRootMissing, args))})
	if got, want := err.Error(), "The folder /media doesn't exist on the server."; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	var w wrapped
	if !errors.As(err, &w) {
		t.Fatal("errors.As does not reach the wrapped error")
	}
	var le *Error
	if !errors.As(err, &le) || le.Localize("fr") != "Le dossier /media n’existe pas sur le serveur." {
		t.Fatalf("Localize(fr) = %q", le.Localize("fr"))
	}
}
