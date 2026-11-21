//go:build !beamfall_appstore

package graph

func registerJellyfinCompatFields() {
	queryFields["jellyfincompat"] = true
	queryFields["jellyfincompatkeys"] = true
	mutationFields["createjellyfincompatkey"] = true
	mutationFields["revokejellyfincompatkey"] = true
	adminFields["jellyfincompatkeys"] = true
	adminFields["createjellyfincompatkey"] = true
	adminFields["revokejellyfincompatkey"] = true
	resultTypes["jellyfincompatkeys"] = []JellyfinCompatKeyInfo{}
	resultTypes["createjellyfincompatkey"] = CreatedJellyfinCompatKeyInfo{}
	resultTypes["revokejellyfincompatkey"] = JellyfinCompatKeyInfo{}
	mutationFields["setjellyfincompat"] = true
	mutationFields["setprofileintegrationsignin"] = true
	adminFields["jellyfincompat"] = true
	adminFields["setjellyfincompat"] = true
	adminFields["setprofileintegrationsignin"] = true
	resultTypes["jellyfincompat"] = JellyfinCompatInfo{}
	resultTypes["setjellyfincompat"] = JellyfinCompatInfo{}
	resultTypes["setprofileintegrationsignin"] = JellyfinCompatProfileInfo{}
}
