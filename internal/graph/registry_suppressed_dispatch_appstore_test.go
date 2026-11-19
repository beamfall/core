//go:build beamfall_appstore

package graph

var suppressedDispatchFields = map[string]bool{
	"capabilitystates":         true,
	"setcapabilityenabled":     true,
	"qbittorrentingest":        true,
	"setqbittorrentingest":     true,
	"pluginworker":             true,
	"approvepluginpermission":  true,
	"setpluginupdatesource":    true,
	"runpluginlifecycleaction": true,
}
