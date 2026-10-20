// Package conformance holds the enforcement suites; this file exports the
// constants both the vocab scan and the plugin-firewall checks consume
// (ARCHITECTURE §0: one shared spelling, never two).
package conformance

// VocabExemptPrefixes are the ONLY subtrees where domain vocabulary may appear
// in the server. After ADR-0114 the first-party plugin bundles and compiled
// hooks live in their own modules (beamfall-plugin-*) outside this repo. As of
// ORG-52 the last exemption (internal/filmtvimport) moved into the
// beamfall-plugin-filmtv module, so the firewall now bans the domain/source
// vocabulary everywhere in core: there are zero exemptions.
var VocabExemptPrefixes = []string{}

func allowedPluginIDLiteral(path, value string) bool {
	switch path {
	case "internal/app/app.go", "internal/store/store.go":
		// "adult" is ProfileKind policy vocabulary in core, not a plugin id.
		return value == "adult"
	case "internal/conformance/exemptions.go":
		// This definition must spell the ProfileKind policy literal it exempts;
		// self-allowing keeps the firewall's own source from tripping the scan.
		return value == "adult"
	default:
		return false
	}
}
