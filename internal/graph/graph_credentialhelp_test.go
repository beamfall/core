package graph

import (
	"strings"
	"testing"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestConsoleConfigCarriesScraperCredentialHelp pins CORE-1127: a scraper
// whose AuthRef is not a plain key (a subscriber PIN) carries the plugin's
// label and https subscribe link to the Console and setup, a scraper with no
// AuthRef carries none, and the stored secret never appears on the wire.
func TestConsoleConfigCarriesScraperCredentialHelp(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	authRef := plugin.ScopedCredentialRef("demo", "shared-api-key")
	secrets := &memoryScraperCredentials{values: map[string]string{authRef: "4821"}}
	handler.SetScraperCredentialStore(secrets)
	handler.scraperExtensions = func(plugin.ScraperSpec) plugin.ScraperExtensions {
		return plugin.ScraperExtensions{CredentialHelp: &plugin.CredentialHelp{
			Label:     plugin.L("Subscriber PIN"),
			HelpURL:   "https://subscribe.example.test",
			HelpLabel: plugin.L("Subscribe at example.test"),
		}}
	}
	openManagerAdminSession(t, st)

	data, errs := post(t, handler,
		`{"query":"query { consoleConfig { plugins { scrapers { scraperID authRef credentialConfigured credentialHelp { label helpURL helpLabel } } } } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("consoleConfig errors = %#v", errs)
	}
	if raw := string(data["consoleConfig"]); strings.Contains(raw, "4821") {
		t.Fatalf("consoleConfig echoed the stored secret: %s", raw)
	}
	var cfg ConsoleConfigInfo
	mustUnmarshal(t, data["consoleConfig"], &cfg)
	var helped, bare int
	for _, p := range cfg.Plugins {
		for _, scraper := range p.Scrapers {
			switch scraper.ScraperID {
			case "alpha", "beta":
				helped++
				help := scraper.CredentialHelp
				if help == nil || help.Label["en"] != "Subscriber PIN" || help.HelpURL != "https://subscribe.example.test" || help.HelpLabel["en"] != "Subscribe at example.test" {
					t.Fatalf("%s credentialHelp = %#v", scraper.ScraperID, help)
				}
				if !scraper.CredentialConfigured {
					t.Fatalf("%s credential not configured: %#v", scraper.ScraperID, scraper)
				}
			case "public":
				bare++
				if scraper.CredentialHelp != nil {
					t.Fatalf("public scraper without AuthRef carries credentialHelp %#v", scraper.CredentialHelp)
				}
			}
		}
	}
	if helped != 2 || bare != 1 {
		t.Fatalf("scraper rows helped=%d bare=%d, want 2 and 1", helped, bare)
	}
}

// TestConsoleConfigOmitsCredentialHelpByDefault proves the pinned SDK path:
// without declarations no scraper carries credentialHelp.
func TestConsoleConfigOmitsCredentialHelpByDefault(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	openManagerAdminSession(t, st)
	data, errs := post(t, handler,
		`{"query":"query { consoleConfig { plugins { scrapers { scraperID credentialHelp { label } } } } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("consoleConfig errors = %#v", errs)
	}
	var cfg ConsoleConfigInfo
	mustUnmarshal(t, data["consoleConfig"], &cfg)
	for _, p := range cfg.Plugins {
		for _, scraper := range p.Scrapers {
			if scraper.CredentialHelp != nil {
				t.Fatalf("%s credentialHelp = %#v, want none", scraper.ScraperID, scraper.CredentialHelp)
			}
		}
	}
}
