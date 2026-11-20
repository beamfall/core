//go:build !beamfall_appstore

package graph

func registerJellyfinCompatFields() {
	queryFields["jellyfincompat"] = true
	mutationFields["setjellyfincompat"] = true
	mutationFields["setprofileintegrationsignin"] = true
	adminFields["jellyfincompat"] = true
	adminFields["setjellyfincompat"] = true
	adminFields["setprofileintegrationsignin"] = true
	resultTypes["jellyfincompat"] = JellyfinCompatInfo{}
	resultTypes["setjellyfincompat"] = JellyfinCompatInfo{}
	resultTypes["setprofileintegrationsignin"] = JellyfinCompatProfileInfo{}
}
