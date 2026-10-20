package plugin

// PluginProvenance is loader-derived authorship standing. Bundle data never
// supplies it, and it never grants capabilities.
type PluginProvenance string

const (
	ProvenanceFirstParty PluginProvenance = "First-Party"
	ProvenanceThirdParty PluginProvenance = "Third-Party"
	ProvenanceSideloaded PluginProvenance = "Sideloaded"
)

// PluginStanding is the pair of orthogonal loader-owned values exposed to
// operator surfaces.
type PluginStanding struct {
	Provenance PluginProvenance
	TrustState PluginTrustState
}

type pluginLoadPath uint8

const (
	loadPathSideloaded pluginLoadPath = iota
	loadPathBuiltin
	loadPathRegistryFirstParty
	loadPathRegistryThirdParty
)

func derivePluginProvenance(path pluginLoadPath, trustState PluginTrustState) PluginProvenance {
	switch path {
	case loadPathBuiltin:
		return ProvenanceFirstParty
	case loadPathRegistryFirstParty:
		if authenticatedPluginTrustState(trustState) {
			return ProvenanceFirstParty
		}
	case loadPathRegistryThirdParty:
		if authenticatedPluginTrustState(trustState) {
			return ProvenanceThirdParty
		}
	}
	return ProvenanceSideloaded
}

func authenticatedPluginTrustState(state PluginTrustState) bool {
	return state == PluginTrusted || state == PluginRevoked
}
