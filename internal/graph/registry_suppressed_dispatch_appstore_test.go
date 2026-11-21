//go:build beamfall_appstore

package graph

var suppressedDispatchFields = map[string]bool{
	"capabilitystates":            true,
	"setcapabilityenabled":        true,
	"qbittorrentingest":           true,
	"setqbittorrentingest":        true,
	"jellyfincompat":              true,
	"setjellyfincompat":           true,
	"setprofileintegrationsignin": true,
	"jellyfincompatkeys":          true,
	"createjellyfincompatkey":     true,
	"revokejellyfincompatkey":     true,
	"pluginworker":                true,
	"approvepluginpermission":     true,
	"setpluginupdatesource":       true,
	"runpluginlifecycleaction":    true,
}
