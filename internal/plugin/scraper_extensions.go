package plugin

import sdk "github.com/beamfall/plugin-sdk"

// ScraperExtensions carries the scraper declarations plugin-sdk v0.3.7
// provides (ScraperSpec.CredentialHelp, ScraperSpec.ReferenceDocuments and
// ScraperSpec.LookupByIDRecordIDPaths, CORE-1127 and CORE-1128). The scrape
// engine and the Console read them through ScraperExtensionsOf, which tests
// can override.
type ScraperExtensions struct {
	// CredentialHelp presents the scraper's AuthRef credential; nil keeps
	// the default key presentation.
	CredentialHelp *CredentialHelp
	// ReferenceDocuments names static documents that map paths read through
	// {ref.<name>:<path>} placeholders.
	ReferenceDocuments map[string]ReferenceDocument
	// LookupByIDRecordIDPaths, keyed by LookupByIDTemplate provider, makes
	// that provider's template a resolver: its document is read only for the
	// scraper's own record id at the path, and the result is the
	// LookupTemplate lookup of that id.
	LookupByIDRecordIDPaths map[string]string
}

// CredentialHelp names what the owner enters for a scraper's AuthRef and
// where to obtain it.
type CredentialHelp = sdk.CredentialHelp

// ReferenceDocument is a static document fetched with the scraper's
// authorization and cached for CacheHours (zero means one week).
type ReferenceDocument = sdk.ReferenceDocument

// ScraperExtensionsOf returns spec's credential help, reference documents and
// id-lookup record paths.
func ScraperExtensionsOf(spec ScraperSpec) ScraperExtensions {
	return ScraperExtensions{
		CredentialHelp:          spec.CredentialHelp,
		ReferenceDocuments:      spec.ReferenceDocuments,
		LookupByIDRecordIDPaths: spec.LookupByIDRecordIDPaths,
	}
}

// CredentialDisplayLabel names a scraper credential for owner-facing status
// text as "<plugin>: <label>" (CORE-2187, CORE-2190), never by its internal
// ref: the label comes from help, else "provider key", and the plugin name
// falls back to its ID.
func CredentialDisplayLabel(bundle Bundle, help *CredentialHelp) string {
	name := bundle.DisplayName.Resolve("")
	if name == "" {
		name = string(bundle.ID)
	}
	label := "provider key"
	if help != nil {
		if resolved := help.Label.Resolve(""); resolved != "" {
			label = resolved
		}
	}
	if name == "" {
		return label
	}
	return name + ": " + label
}
