package plugin

// ScraperExtensions carries the scraper declarations plugin-sdk v0.3.6 adds
// (ScraperSpec.CredentialHelp, ScraperSpec.ReferenceDocuments and
// ScraperSpec.LookupByIDRecordIDPaths, CORE-1127 and CORE-1128) in core-owned
// types. Core pins plugin-sdk v0.3.5, which has
// neither field, so the scrape engine and the Console read them through
// ScraperExtensionsOf; the behaviour builds and is tested today, and the
// repin maps the SDK fields here.
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
type CredentialHelp struct {
	Label     LocaleString
	HelpURL   string
	HelpLabel LocaleString
}

// ReferenceDocument is a static document fetched with the scraper's
// authorization and cached for CacheHours (zero means one week).
type ReferenceDocument struct {
	Path       string
	CacheHours int
}

// ScraperExtensionsOf returns spec's v0.3.6 declarations. Against the pinned
// plugin-sdk v0.3.5 there are none.
func ScraperExtensionsOf(ScraperSpec) ScraperExtensions {
	return ScraperExtensions{}
}
