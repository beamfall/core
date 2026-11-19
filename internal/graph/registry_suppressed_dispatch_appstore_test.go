//go:build beamfall_appstore

package graph

var suppressedDispatchFields = map[string]bool{
	"capabilitystates":         true,
	"workflowcapabilitystates": true,
	"setcapabilityenabled":     true,
	"enqueueconvertjob":        true,
	"qbittorrentingest":        true,
	"setqbittorrentingest":     true,
	"pluginworker":             true,
	"approvepluginpermission":  true,
	"setpluginupdatesource":    true,
	"runpluginlifecycleaction": true,
}
