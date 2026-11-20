package graph

import "slices"

// fieldArgumentRegistry lists, for every top-level field in
// fieldHandlerRegistry, the request arguments its resolvers read: name, kind
// and whether the field runs without it (CORE-2107). agentCatalog reports it to
// agents. Fields in the wire nested-schema argument map (fieldArguments) start
// from that entry and append the arguments it does not describe.
//
// Kinds are those of NestedArgument plus "boolean". TestFieldArgumentRegistry
// CoversResolverReads parses this package's resolvers and fails when one reads
// an argument its field's entry does not list, so add the argument here when a
// resolver starts reading it.
var fieldArgumentRegistry = map[string][]NestedArgument{
	"acceptpendingaitag": {
		{Name: "limit", Kind: "int", Optional: true},
		{Name: "tagID", Kind: "string"},
	},
	"acceptwatchparty": {
		{Name: "clockOffsetMS", Kind: "number", Optional: true},
		{Name: "partyID", Kind: "string"},
		{Name: "renderLatencyMS", Kind: "number", Optional: true},
	},
	"accessgrantsforprofile": {
		{Name: "profileID", Kind: "string"},
	},
	"accessrequest": {
		{Name: "id", Kind: "string"},
	},
	"accessschedule": {
		{Name: "profileID", Kind: "string"},
	},
	"activity": {
		{Name: "id", Kind: "string"},
	},
	"activitylog": {
		{Name: "limit", Kind: "int", Optional: true},
		{Name: "offset", Kind: "int", Optional: true},
	},
	"addcollectionmedia": {
		{Name: "collectionID", Kind: "string", Optional: true},
		{Name: "id", Kind: "string", Optional: true},
		{Name: "mediaIDs", Kind: "list"},
	},
	"addmediaexternalid": {
		{Name: "mediaID", Kind: "string"},
		{Name: "scheme", Kind: "string"},
		{Name: "value", Kind: "string"},
	},
	"addpathpickerroot": {
		{Name: "path", Kind: "string"},
	},
	"addsubscription": {
		{Name: "authHeader", Kind: "string", Optional: true},
		{Name: "feedURL", Kind: "string"},
		{Name: "libraryID", Kind: "string"},
		{Name: "root", Kind: "string", Optional: true},
		{Name: "title", Kind: "string", Optional: true},
	},
	"adminsession": {},
	"agentcatalog": {},
	"ambientqueue": sourceItemsRegistryArguments(),
	"apikeys":      {},
	"apitokens":    {},
	"approvepluginpermission": {
		{Name: "actionID", Kind: "string", Optional: true},
		{Name: "allLibraries", Kind: "boolean", Optional: true},
		{Name: "approved", Kind: "boolean", Optional: true},
		{Name: "boundProfileID", Kind: "string", Optional: true},
		{Name: "descriptorID", Kind: "string", Optional: true},
		{Name: "kind", Kind: "string"},
		{Name: "pluginID", Kind: "string"},
	},
	"artworkcandidates": {
		{Name: "mediaID", Kind: "string"},
		{Name: "slot", Kind: "string"},
	},
	"assigntag": {
		{Name: "tagID", Kind: "string"},
		{Name: "targetID", Kind: "string"},
		{Name: "targetType", Kind: "string"},
	},
	"associatesubscriptionentry": {
		{Name: "guid", Kind: "string"},
		{Name: "mediaID", Kind: "string"},
		{Name: "subscriptionID", Kind: "string"},
	},
	// Two lines of distance from the feed-subscription entry above: the ADR-0115
	// firewall reads a subscriptionID within three lines of the next key as billing.
	"backup": {
		{Name: "dest", Kind: "string"},
	},
	"backuphistory": {
		{Name: "dest", Kind: "string"},
	},
	"batchrenamemanagedpaths": {
		{Name: "libraryID", Kind: "string"},
		{Name: "renames", Kind: "list"},
	},
	"browseserverfolders": {
		{Name: "depth", Kind: "number", Optional: true},
		{Name: "path", Kind: "string", Optional: true},
	},
	"bulkassigntags": {
		{Name: "mode", Kind: "string", Optional: true},
		{Name: "tagIDs", Kind: "list"},
		{Name: "targetIDs", Kind: "list"},
		{Name: "targetType", Kind: "string"},
	},
	"bulkeditapply": {
		{Name: "fields", Kind: "list"},
		{Name: "mediaIDs", Kind: "list"},
	},
	"bulkeditpreview": {
		{Name: "fields", Kind: "list"},
		{Name: "mediaIDs", Kind: "list"},
	},
	"bulkfetchmissingartwork": {
		{Name: "libraryID", Kind: "string"},
	},
	"bulkfetchmissingartworkselected": {
		{Name: "libraryID", Kind: "string"},
		{Name: "mediaIDs", Kind: "list"},
	},
	"bulkmergeduplicates": {
		{Name: "keepMediaID", Kind: "string"},
		{Name: "libraryID", Kind: "string"},
	},
	"bulkmergeduplicatesselected": {
		{Name: "libraryID", Kind: "string"},
		{Name: "mediaIDs", Kind: "list"},
	},
	"bulkrescanunmatched": {
		{Name: "libraryID", Kind: "string"},
	},
	"bulkrescanunmatchedselected": {
		{Name: "libraryID", Kind: "string"},
		{Name: "mediaIDs", Kind: "list"},
	},
	"bulkrescrapescrapefailures": {
		{Name: "libraryID", Kind: "string"},
	},
	"bulkrescrapescrapefailuresselected": {
		{Name: "libraryID", Kind: "string"},
		{Name: "mediaIDs", Kind: "list"},
	},
	"bulktrashbroken": {
		{Name: "libraryID", Kind: "string"},
	},
	"bulktrashbrokenselected": {
		{Name: "libraryID", Kind: "string"},
		{Name: "mediaIDs", Kind: "list"},
	},
	"cancelstagedrestore": {},
	"acknowledgebackupkey": {
		{Name: "escrowID", Kind: "string"},
	},
	"capabilitystates": {},
	"channels": {
		{Name: "libraryID", Kind: "string"},
	},
	"checkremoteaccessreachability": {},
	"clearallmutes":                 {},
	"clearmute": {
		{Name: "includeDescendants", Kind: "boolean", Optional: true},
		{Name: "kind", Kind: "string"},
		{Name: "targetID", Kind: "string"},
	},
	"clientprefs": {},
	"collections": {
		{Name: "includeTrashed", Kind: "boolean", Optional: true},
		{Name: "libraryID", Kind: "string"},
	},
	"commitscanpreview": {
		{Name: "previewToken", Kind: "string"},
	},
	"connectionendpoints": {},
	"consoleconfig":       {},
	"controlsession": {
		{Name: "command", Kind: "string"},
		{Name: "direction", Kind: "string", Optional: true},
		{Name: "mediaID", Kind: "string", Optional: true},
		{Name: "positionMs", Kind: "int", Optional: true},
		{Name: "sessionID", Kind: "string"},
		{Name: "text", Kind: "string", Optional: true},
		{Name: "trackID", Kind: "string", Optional: true},
		{Name: "volume", Kind: "int", Optional: true},
	},
	"createapikey": {
		{Name: "name", Kind: "string"},
		{Name: "profileID", Kind: "string", Optional: true},
		{Name: "scopes", Kind: "list", Optional: true},
	},
	"createapitoken": {
		{Name: "name", Kind: "string"},
	},
	"createchannel": {
		{Name: "libraryID", Kind: "string"},
		{Name: "name", Kind: "string"},
		{Name: "ordering", Kind: "string", Optional: true},
		{Name: "repeat", Kind: "string", Optional: true},
		{Name: "sourceExpr", Kind: "string", Optional: true},
	},
	"createcollection": {
		{Name: "libraryID", Kind: "string"},
		{Name: "name", Kind: "string"},
		{Name: "parentID", Kind: "string", Optional: true},
		{Name: "sortOrder", Kind: "int", Optional: true},
	},
	"createlibrary": {
		{Name: "familyScope", Kind: "string", Optional: true},
		{Name: "pluginID", Kind: "string", Optional: true},
		{Name: "restricted", Kind: "boolean", Optional: true},
		{Name: "roots", Kind: "list", Optional: true},
		{Name: "title", Kind: "string"},
	},
	"createmanagedfolder": {
		{Name: "libraryID", Kind: "string"},
		{Name: "name", Kind: "string"},
		{Name: "parentPath", Kind: "string"},
	},
	"createmarker": {
		{Name: "endSeconds", Kind: "number", Optional: true},
		{Name: "mediaID", Kind: "string"},
		{Name: "segmentKind", Kind: "string", Optional: true},
		{Name: "startSeconds", Kind: "number", Optional: true},
		{Name: "tags", Kind: "list", Optional: true},
		{Name: "title", Kind: "string"},
	},
	"createmediafromunmatched": {
		{Name: "libraryID", Kind: "string"},
		{Name: "recordID", Kind: "string"},
		{Name: "title", Kind: "string"},
		{Name: "year", Kind: "int", Optional: true},
	},
	"createofflinepack": {
		{Name: "deviceAttestation", Kind: "string", Optional: true},
		{Name: "devicePublicKey", Kind: "string", Optional: true},
		{Name: "quality", Kind: "string", Optional: true},
		{Name: "selectedVersions", Kind: "object", Optional: true},
		{Name: "targetKind", Kind: "string"},
		{Name: "targetRef", Kind: "string"},
	},
	"createprofile": {
		{Name: "audioLanguages", Kind: "list", Optional: true},
		{Name: "disabled", Kind: "boolean", Optional: true},
		{Name: "kind", Kind: "string"},
		{Name: "maxConcurrentStreams", Kind: "number", Optional: true},
		{Name: "name", Kind: "string"},
		{Name: "preferSDH", Kind: "boolean", Optional: true},
		{Name: "remoteMaxBitrate", Kind: "number", Optional: true},
		{Name: "subtitleLanguages", Kind: "list", Optional: true},
		{Name: "subtitleMode", Kind: "string", Optional: true},
		{Name: "transcodeAllowed", Kind: "boolean", Optional: true},
	},
	"createretentionrule": {
		{Name: "enabled", Kind: "boolean", Optional: true},
		{Name: "graceDays", Kind: "number", Optional: true},
		{Name: "name", Kind: "string"},
		{Name: "revealedAtAuthoring", Kind: "boolean", Optional: true},
		{Name: "sourceExpr", Kind: "string"},
		{Name: "targetLibraryID", Kind: "string"},
	},
	"createsavedfilter": {
		{Name: "filterJSON", Kind: "string", Optional: true},
		{Name: "libraryID", Kind: "string"},
		{Name: "name", Kind: "string"},
	},
	"createsmartcollection": {
		{Name: "libraryID", Kind: "string"},
		{Name: "name", Kind: "string"},
		{Name: "sortExpr", Kind: "string", Optional: true},
		{Name: "sourceExpr", Kind: "string", Optional: true},
	},
	"createtag": {
		{Name: "name", Kind: "string"},
		{Name: "parentTagID", Kind: "string", Optional: true},
	},
	"createwatchparty": {
		{Name: "clockOffsetMS", Kind: "number", Optional: true},
		{Name: "content", Kind: "string"},
		{Name: "lead", Kind: "boolean", Optional: true},
		{Name: "relaxed", Kind: "boolean", Optional: true},
		{Name: "renderLatencyMS", Kind: "number", Optional: true},
	},
	"currentsession": {},
	"deletechannel": {
		{Name: "id", Kind: "string"},
	},
	"deletecollection": {
		{Name: "id", Kind: "string"},
	},
	"deletelibrary": {
		{Name: "libraryID", Kind: "string"},
	},
	"deletemanagedpath": {
		{Name: "libraryID", Kind: "string"},
		{Name: "path", Kind: "string"},
	},
	"deletemarker": {
		{Name: "markerID", Kind: "string"},
	},
	"deletemyengagement": {
		{Name: "pin", Kind: "string", Optional: true},
		{Name: "scope", Kind: "string"},
	},
	"deletemypacktokens": {},
	"deleteretentionrule": {
		{Name: "id", Kind: "string"},
	},
	"deletesavedfilter": {
		{Name: "id", Kind: "string"},
	},
	"deletesmartcollection": {
		{Name: "id", Kind: "string"},
	},
	"deletetag": {
		{Name: "tagID", Kind: "string"},
	},
	"deletewebhook": {
		{Name: "id", Kind: "string"},
	},
	"devices":           {},
	"diagnosticsbundle": fieldArguments["diagnosticsbundle"],
	"disablescrapercredential": {
		{Name: "authRef", Kind: "string"},
	},
	"dismissunmatched": {
		{Name: "mediaID", Kind: "string"},
	},
	"editmediametadata": {
		{Name: "mediaID", Kind: "string"},
		{Name: "sortTitle", Kind: "string", Optional: true},
		{Name: "summary", Kind: "string", Optional: true},
		{Name: "title", Kind: "string", Optional: true},
		{Name: "year", Kind: "number", Optional: true},
	},
	"encryptedlibrarystatus": {
		{Name: "libraryID", Kind: "string"},
	},
	"endadminsession": {},
	"engagementcounter": {
		{Name: "entityID", Kind: "string", Optional: true},
		{Name: "history", Kind: "boolean", Optional: true},
		{Name: "kind", Kind: "string"},
		{Name: "mediaID", Kind: "string", Optional: true},
	},
	"entitiesformedia": fieldArguments["entitiesformedia"],
	"entity":           fieldArguments["entity"],
	"entityrelations": slices.Concat(fieldArguments["entityrelations"], []NestedArgument{
		{Name: "limit", Kind: "int", Optional: true},
		{Name: "offset", Kind: "int", Optional: true},
	}),
	"evictplaybackrenditioncache": {},
	"extensionrequest": {
		{Name: "id", Kind: "string"},
	},
	"fixmatch": fieldArguments["fixmatch"],
	"fixmatchcandidates": {
		{Name: "mediaID", Kind: "string", Optional: true},
		{Name: "pluginID", Kind: "string"},
		{Name: "query", Kind: "string", Optional: true},
		{Name: "scraperID", Kind: "string"},
	},
	"fixmatchpreview": {
		{Name: "candidateID", Kind: "string"},
		{Name: "mediaID", Kind: "string"},
		{Name: "pluginID", Kind: "string"},
		{Name: "scraperID", Kind: "string"},
	},
	"foldertree": {
		{Name: "depth", Kind: "number", Optional: true},
		{Name: "libraryID", Kind: "string"},
		{Name: "root", Kind: "string", Optional: true},
	},
	"formgroup":   {},
	"hostmetrics": {},
	"identifyapply": {
		{Name: "candidateID", Kind: "string"},
		{Name: "mediaID", Kind: "string"},
		{Name: "pluginID", Kind: "string"},
		{Name: "scraperID", Kind: "string"},
	},
	"identifylookup": {
		{Name: "candidateID", Kind: "string"},
		{Name: "mediaID", Kind: "string", Optional: true},
		{Name: "pluginID", Kind: "string"},
		{Name: "scraperID", Kind: "string"},
	},
	"identifysearch": {
		{Name: "mediaID", Kind: "string", Optional: true},
		{Name: "pluginID", Kind: "string"},
		{Name: "query", Kind: "string", Optional: true},
		{Name: "scraperID", Kind: "string"},
	},
	"importers": {},
	"importmanagedfile": {
		{Name: "class", Kind: "string", Optional: true},
		{Name: "codec", Kind: "string", Optional: true},
		{Name: "container", Kind: "string", Optional: true},
		{Name: "destinationParentPath", Kind: "string"},
		{Name: "importToken", Kind: "string"},
		{Name: "libraryID", Kind: "string"},
	},
	"importstatus": fieldArguments["importstatus"],
	"inspectbackup": {
		{Name: "backupKey", Kind: "string"},
		{Name: "envelope", Kind: "string"},
	},
	"invitetowatchparty": {
		{Name: "displayName", Kind: "string"},
		{Name: "partyID", Kind: "string"},
	},
	"itemstate": fieldArguments["itemstate"],
	"job": {
		{Name: "id", Kind: "string"},
	},
	"jobcancel": {
		{Name: "id", Kind: "string"},
	},
	"jobevents": {
		{Name: "beforeID", Kind: "string", Optional: true},
		{Name: "id", Kind: "string"},
		{Name: "limit", Kind: "int", Optional: true},
	},
	"jobretry": {
		{Name: "id", Kind: "string"},
	},
	"jobs": {},
	"joingroup": {
		{Name: "clockOffsetMS", Kind: "number", Optional: true},
		{Name: "groupID", Kind: "string"},
		{Name: "renderLatencyMS", Kind: "number", Optional: true},
	},
	"leavegroup": {
		{Name: "groupID", Kind: "string"},
	},
	"leavewatchparty": {
		{Name: "end", Kind: "boolean", Optional: true},
		{Name: "partyID", Kind: "string"},
	},
	"libraries": {},
	"libraryfolder": {
		{Name: "libraryID", Kind: "string"},
		{Name: "limit", Kind: "int", Optional: true},
		{Name: "offset", Kind: "int", Optional: true},
		{Name: "path", Kind: "string", Optional: true},
	},
	"libraryduplicategroups": {
		{Name: "libraryID", Kind: "string"},
	},
	"libraryhealth": {},
	"libraryhealthitems": {
		{Name: "dimension", Kind: "string"},
		{Name: "libraryID", Kind: "string"},
		{Name: "limit", Kind: "int", Optional: true},
	},
	"libraryindexstatus": {},
	"libraryentities":    fieldArguments["libraryentities"],
	"libraryitems":       fieldArguments["libraryitems"],
	"linkmediarelation": {
		{Name: "fromMediaID", Kind: "string"},
		{Name: "relation", Kind: "string"},
		{Name: "toMediaID", Kind: "string"},
	},
	"linkscrobble": {
		{Name: "accountLabel", Kind: "string", Optional: true},
		{Name: "credential", Kind: "string"},
		{Name: "service", Kind: "string"},
	},
	"livesessions": {},
	"manifest":     {},
	"markers": {
		{Name: "mediaID", Kind: "string"},
	},
	"marknotificationread": {
		{Name: "id", Kind: "string"},
	},
	"media": fieldArguments["media"],
	"mediafieldlocks": {
		{Name: "mediaID", Kind: "string"},
	},
	"mediafields":    fieldArguments["mediafields"],
	"mediafiles":     fieldArguments["mediafiles"],
	"mediatracks":    fieldArguments["mediatracks"],
	"relatedmedia":   fieldArguments["relatedmedia"],
	"mediaforentity": fieldArguments["mediaforentity"],
	"mediametadata": {
		{Name: "mediaID", Kind: "string"},
	},
	"mediarelations": slices.Concat(fieldArguments["mediarelations"], []NestedArgument{
		{Name: "limit", Kind: "int", Optional: true},
		{Name: "offset", Kind: "int", Optional: true},
	}),
	"mergeentities": {
		{Name: "loserEntityID", Kind: "string"},
		{Name: "winnerEntityID", Kind: "string"},
	},
	"mergemedia": {
		{Name: "keepMediaID", Kind: "string"},
		{Name: "libraryID", Kind: "string"},
	},
	"metadatacredits": {},
	"metadataexport":  {},
	"movemanagedpath": {
		{Name: "destinationParentPath", Kind: "string"},
		{Name: "libraryID", Kind: "string"},
		{Name: "path", Kind: "string"},
	},
	"mutes":         {},
	"mydatasummary": {},
	"myengagement": {
		{Name: "pin", Kind: "string", Optional: true},
	},
	"myengagementlog": {
		{Name: "from", Kind: "string", Optional: true},
		{Name: "libraryID", Kind: "string", Optional: true},
		{Name: "limit", Kind: "int", Optional: true},
		{Name: "offset", Kind: "int", Optional: true},
		{Name: "to", Kind: "string", Optional: true},
	},
	"mysessions":         {},
	"navigationmanifest": {},
	"navoverride":        {},
	"navoverridehistory": {
		{Name: "limit", Kind: "int", Optional: true},
	},
	"notifications": {
		{Name: "limit", Kind: "int", Optional: true},
		{Name: "unreadOnly", Kind: "boolean", Optional: true},
	},
	"offlinepacks":       {},
	"operationsgovernor": {},
	"operatordeleteprofiledata": {
		{Name: "profileID", Kind: "string"},
	},
	"organizeapply": {
		{Name: "libraryID", Kind: "string"},
		{Name: "template", Kind: "string"},
	},
	"organizepreview": {
		{Name: "libraryID", Kind: "string"},
		{Name: "template", Kind: "string"},
	},
	"pathpicker": {
		{Name: "depth", Kind: "number", Optional: true},
		{Name: "distributionClass", Kind: "string", Optional: true},
		{Name: "libraryID", Kind: "string", Optional: true},
		{Name: "path", Kind: "string", Optional: true},
		{Name: "role", Kind: "string", Optional: true},
	},
	"pendingaccessrequests": {},
	"pendingaitagqueue": {
		{Name: "limit", Kind: "int", Optional: true},
	},
	"pinartworkcandidate": {
		{Name: "candidateID", Kind: "string"},
		{Name: "mediaID", Kind: "string"},
		{Name: "slot", Kind: "string"},
	},
	"playbackrenditioncache": {},
	"playhistory": {
		{Name: "libraryID", Kind: "string", Optional: true},
		{Name: "limit", Kind: "int", Optional: true},
		{Name: "profileID", Kind: "string", Optional: true},
		{Name: "since", Kind: "string", Optional: true},
		{Name: "until", Kind: "string", Optional: true},
	},
	"playstats": {
		{Name: "windowDays", Kind: "int"},
	},
	"plugins": {},
	"pluginworker": {
		{Name: "actionID", Kind: "string", Optional: true},
		{Name: "descriptorID", Kind: "string", Optional: true},
		{Name: "pluginID", Kind: "string"},
	},
	"precomputeplaybackrenditions": {
		{Name: "mediaID", Kind: "string"},
	},
	"preplayclips":    {},
	"preplayqueue":    fieldArguments["preplayqueue"],
	"profiles":        {},
	"profilesessions": {},
	"provisionencryptedlibrary": {
		{Name: "libraryID", Kind: "string"},
		{Name: "passphrase", Kind: "string", Optional: true},
		{Name: "unsealMode", Kind: "string", Optional: true},
	},
	"purgelibrary": {
		{Name: "confirm", Kind: "boolean"},
		{Name: "libraryID", Kind: "string"},
	},
	"purgetrash": {
		{Name: "before", Kind: "string"},
		{Name: "confirm", Kind: "boolean"},
	},
	"jellyfincompat":    {},
	"qbittorrentingest": {},
	"qualitypolicy": {
		{Name: "libraryID", Kind: "string"},
	},
	"queueadd": {
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "mediaID", Kind: "string"},
		{Name: "position", Kind: "number", Optional: true},
	},
	"queueclear": {
		{Name: "expectedProfileID", Kind: "string", Optional: true},
	},
	"queuemove": {
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "mediaID", Kind: "string"},
		{Name: "position", Kind: "number"},
	},
	"queueremove": {
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "mediaID", Kind: "string"},
	},
	"recordengagement": {
		{Name: "entityID", Kind: "string", Optional: true},
		{Name: "kind", Kind: "string"},
		{Name: "mediaID", Kind: "string", Optional: true},
	},
	"refreshmediametadata": {
		{Name: "mediaID", Kind: "string"},
	},
	"registerdevicepushtoken": {
		{Name: "platform", Kind: "string"},
		{Name: "token", Kind: "string"},
	},
	"registerimportsource": {
		{Name: "sourcePath", Kind: "string"},
	},
	"rejectpendingaitag": {
		{Name: "limit", Kind: "int", Optional: true},
		{Name: "tagID", Kind: "string"},
	},
	"relock":       {},
	"remoteaccess": {},
	"removecollectionmedia": {
		{Name: "collectionID", Kind: "string", Optional: true},
		{Name: "id", Kind: "string", Optional: true},
		{Name: "mediaIDs", Kind: "list"},
	},
	"removelastengagement": {
		{Name: "entityID", Kind: "string", Optional: true},
		{Name: "kind", Kind: "string"},
		{Name: "mediaID", Kind: "string", Optional: true},
	},
	"removesubscription": {
		{Name: "id", Kind: "string"},
	},
	"renamemanagedpath": {
		{Name: "libraryID", Kind: "string"},
		{Name: "name", Kind: "string"},
		{Name: "path", Kind: "string"},
	},
	"reordercollectionmedia": {
		{Name: "collectionID", Kind: "string", Optional: true},
		{Name: "id", Kind: "string", Optional: true},
		{Name: "mediaIDs", Kind: "list"},
	},
	"reportdrift": {
		{Name: "atMS", Kind: "number", Optional: true},
		{Name: "driftMS", Kind: "number", Optional: true},
		{Name: "groupID", Kind: "string"},
		{Name: "positionMS", Kind: "number", Optional: true},
		{Name: "ratePermille", Kind: "int", Optional: true},
	},
	"endplayback": {
		{Name: "mediaId", Kind: "string"},
	},
	"reportplayback": {
		{Name: "ambient", Kind: "boolean", Optional: true},
		{Name: "durationSeconds", Kind: "int", Optional: true},
		{Name: "eventTimeMS", Kind: "number", Optional: true},
		{Name: "finished", Kind: "boolean", Optional: true},
		{Name: "mediaID", Kind: "string"},
		{Name: "positionSeconds", Kind: "int", Optional: true},
	},
	"requestaccess": {
		{Name: "mediaID", Kind: "string"},
	},
	"requestextension": {
		{Name: "remainingMinutes", Kind: "int", Optional: true},
		{Name: "triggerSource", Kind: "string", Optional: true},
	},
	"requestwatchpartytransport": {
		{Name: "item", Kind: "string"},
		{Name: "partyID", Kind: "string"},
		{Name: "paused", Kind: "boolean", Optional: true},
		{Name: "positionMS", Kind: "number", Optional: true},
	},
	"resetnavoverride": {},
	"resetunmatchedsuppression": {
		{Name: "candidateID", Kind: "string", Optional: true},
		{Name: "fingerprint", Kind: "string", Optional: true},
		{Name: "recordID", Kind: "string", Optional: true},
		{Name: "scope", Kind: "string"},
		{Name: "sourceHandle", Kind: "string", Optional: true},
	},
	"resolveaccessrequest": {
		{Name: "grant", Kind: "boolean", Optional: true},
		{Name: "id", Kind: "string"},
		{Name: "verdict", Kind: "string", Optional: true},
	},
	"respondextension": {
		{Name: "approve", Kind: "boolean", Optional: true},
		{Name: "grant", Kind: "string", Optional: true},
		{Name: "id", Kind: "string"},
	},
	"restorecollection": {
		{Name: "id", Kind: "string"},
	},
	"restorelibrary": {
		{Name: "libraryID", Kind: "string"},
	},
	"restoremanagedpath": {
		{Name: "libraryID", Kind: "string"},
		{Name: "trashID", Kind: "string"},
	},
	"restoremarker": {
		{Name: "markerID", Kind: "string"},
	},
	"restoremedia": {
		{Name: "mediaID", Kind: "string"},
	},
	"restorenavoverride": {
		{Name: "historyID", Kind: "scalar"},
	},
	"restoretag": {
		{Name: "tagID", Kind: "string"},
	},
	"retentionrules": {},
	"retryofflinepack": {
		{Name: "id", Kind: "string"},
	},
	"reveal": {
		{Name: "pin", Kind: "string"},
	},
	"revokeaccessgrant": {
		{Name: "mediaID", Kind: "string"},
		{Name: "profileID", Kind: "string"},
	},
	"revokealldevices": {},
	"revokeapikey": {
		{Name: "id", Kind: "string"},
	},
	"revokeapitoken": {
		{Name: "id", Kind: "string"},
	},
	"revokedevice": {
		{Name: "deviceID", Kind: "string"},
	},
	"revokeofflinepack": {
		{Name: "id", Kind: "string"},
	},
	"rewrapencryptedlibrary": {
		{Name: "libraryID", Kind: "string"},
		{Name: "passphrase", Kind: "string", Optional: true},
		{Name: "unsealMode", Kind: "string", Optional: true},
	},
	"runeventscompaction": {},
	"runimport": {
		{Name: "categories", Kind: "list", Optional: true},
		{Name: "dryRun", Kind: "boolean", Optional: true},
		{Name: "importerID", Kind: "string"},
		{Name: "libraryID", Kind: "string", Optional: true},
		{Name: "pluginID", Kind: "string"},
		{Name: "profileID", Kind: "string", Optional: true},
		{Name: "root", Kind: "string", Optional: true},
		{Name: "sourcePath", Kind: "string", Optional: true},
		{Name: "sourceUserMappings", Kind: "list", Optional: true},
	},
	"runjob": {},
	"runpluginlifecycleaction": {
		{Name: "action", Kind: "string"},
		{Name: "actionID", Kind: "string", Optional: true},
		{Name: "descriptorID", Kind: "string", Optional: true},
		{Name: "pluginID", Kind: "string"},
	},
	"savedfilters": {
		{Name: "libraryID", Kind: "string"},
	},
	"savewebhook": {
		{Name: "allowPrivateNetwork", Kind: "boolean", Optional: true},
		{Name: "disabled", Kind: "boolean", Optional: true},
		{Name: "eventKinds", Kind: "list", Optional: true},
		{Name: "format", Kind: "string", Optional: true},
		{Name: "from", Kind: "string", Optional: true},
		{Name: "id", Kind: "string", Optional: true},
		{Name: "name", Kind: "string", Optional: true},
		{Name: "secret", Kind: "string", Optional: true},
		{Name: "to", Kind: "list", Optional: true},
		{Name: "url", Kind: "string", Optional: true},
		{Name: "username", Kind: "string", Optional: true},
	},
	"scanestimate": {
		{Name: "libraryID", Kind: "string"},
		{Name: "pluginID", Kind: "string"},
		{Name: "root", Kind: "string"},
	},
	"refreshlibrarymetadata": {
		{Name: "libraryID", Kind: "string"},
	},
	"scanlibrary": {
		{Name: "backfill", Kind: "boolean", Optional: true},
		{Name: "libraryID", Kind: "string"},
		{Name: "root", Kind: "string", Optional: true},
	},
	"scanpreview": {
		{Name: "libraryID", Kind: "string"},
		{Name: "limit", Kind: "number", Optional: true},
		{Name: "root", Kind: "string", Optional: true},
	},
	"schedules":           {},
	"scrobbleeligibility": {},
	"scrobblelinks":       {},
	"scrobblestatus":      {},
	"sealencryptedlibrary": {
		{Name: "libraryID", Kind: "string"},
	},
	"search":             fieldArguments["search"],
	"searchentities":     fieldArguments["searchentities"],
	"searchfeeds":        fieldArguments["searchfeeds"],
	"searchresult":       fieldArguments["searchresult"],
	"semanticcapability": {},
	"serverinfo":         {},
	"serverlogs":         fieldArguments["serverlogs"],
	"setaccessschedule": {
		{Name: "profileID", Kind: "string"},
		{Name: "windows", Kind: "list", Optional: true},
	},
	"setcapabilityenabled": {
		{Name: "capability", Kind: "string"},
		{Name: "enabled", Kind: "boolean"},
	},
	"setcertificationceiling": {
		{Name: "allowUnrated", Kind: "boolean", Optional: true},
		{Name: "ceiling", Kind: "string", Optional: true},
		{Name: "pluginID", Kind: "string"},
		{Name: "profileID", Kind: "string"},
	},
	"setclientpref": {
		{Name: "key", Kind: "string"},
		{Name: "value", Kind: "string", Optional: true},
	},
	"setconsoleconfig": {
		{Name: "adminSessionMinutes", Kind: "number", Optional: true},
		{Name: "backgroundWorkers", Kind: "number", Optional: true},
		{Name: "eventsRetentionDays", Kind: "number", Optional: true},
		{Name: "libraryIcon", Kind: "string", Optional: true},
		{Name: "libraryID", Kind: "string", Optional: true},
		{Name: "observabilityEnabled", Kind: "boolean", Optional: true},
		{Name: "remoteEnrollmentEnabled", Kind: "boolean", Optional: true},
		{Name: "remoteMaxBitrate", Kind: "number", Optional: true},
		{Name: "sessionLifetimeDays", Kind: "number", Optional: true},
		{Name: "updateCheckChannel", Kind: "string", Optional: true},
		{Name: "updateCheckEnabled", Kind: "boolean", Optional: true},
		{Name: "viewerPairingCodesDisabled", Kind: "boolean", Optional: true},
	},
	"setdevicerole": {
		{Name: "deviceID", Kind: "string"},
		{Name: "role", Kind: "string"},
	},
	"setextensionpreauthorization": {
		{Name: "enabled", Kind: "boolean", Optional: true},
		{Name: "profileID", Kind: "string"},
	},
	"setfavorite": {
		{Name: "favorite", Kind: "boolean"},
		{Name: "mediaID", Kind: "string"},
	},
	"setfollow": {
		{Name: "id", Kind: "string", Optional: true},
		{Name: "on", Kind: "boolean", Optional: true},
		{Name: "subscriptionID", Kind: "string", Optional: true},
	},
	"setlibraryaiindexing": {
		{Name: "enabled", Kind: "boolean"},
		{Name: "libraryID", Kind: "string"},
	},
	"setlibrarybacking": {
		{Name: "custodial", Kind: "boolean"},
		{Name: "encrypted", Kind: "boolean"},
		{Name: "libraryID", Kind: "string"},
	},
	"setlibrarypreviewclips": {
		{Name: "enabled", Kind: "boolean"},
		{Name: "libraryID", Kind: "string"},
	},
	"setlibraryshowonhome": {
		{Name: "enabled", Kind: "boolean"},
		{Name: "libraryID", Kind: "string"},
	},
	"setlibraryrestricted": {
		{Name: "libraryID", Kind: "string"},
		{Name: "restricted", Kind: "boolean"},
	},
	"setlibraryroots": {
		{Name: "libraryID", Kind: "string"},
		{Name: "roots", Kind: "list"},
	},
	"setmediafieldlock": {
		{Name: "field", Kind: "string"},
		{Name: "locked", Kind: "boolean"},
		{Name: "mediaID", Kind: "string"},
	},
	"setmute": {
		{Name: "includeDescendants", Kind: "boolean", Optional: true},
		{Name: "kind", Kind: "string"},
		{Name: "targetID", Kind: "string"},
	},
	"setnavoverride": {
		{Name: "hiddenNodeIDs", Kind: "list", Optional: true},
		{Name: "layoutSectionOverrides", Kind: "object", Optional: true},
		{Name: "nodeLabelOverrides", Kind: "map", Optional: true},
		{Name: "nodeOrder", Kind: "list", Optional: true},
		{Name: "themeAccentColor", Kind: "string", Optional: true},
	},
	"setpluginactive": {
		{Name: "active", Kind: "boolean"},
		{Name: "pluginID", Kind: "string"},
	},
	"setpluginconfig": {
		{Name: "clear", Kind: "list", Optional: true},
		{Name: "pluginID", Kind: "string"},
		{Name: "values", Kind: "object", Optional: true},
	},
	"setpluginupdatesource": {
		{Name: "actionID", Kind: "string", Optional: true},
		{Name: "descriptorID", Kind: "string", Optional: true},
		{Name: "kind", Kind: "string", Optional: true},
		{Name: "location", Kind: "string", Optional: true},
		{Name: "pin", Kind: "string", Optional: true},
		{Name: "pinKind", Kind: "string", Optional: true},
		{Name: "pluginID", Kind: "string"},
		{Name: "signature", Kind: "string", Optional: true},
		{Name: "signingKey", Kind: "string", Optional: true},
	},
	"setpreplayclips": {
		{Name: "mediaIDs", Kind: "list"},
	},
	"setprofilelibraries": {
		{Name: "libraryIDs", Kind: "list"},
		{Name: "profileID", Kind: "string"},
	},
	"setjellyfincompat": {
		{Name: "allowedCIDRs", Kind: "list", Optional: true},
		{Name: "enabled", Kind: "boolean", Optional: true},
		{Name: "libraryIDs", Kind: "list", Optional: true},
		{Name: "trustedProxyCIDRs", Kind: "list", Optional: true},
	},
	"setprofileintegrationsignin": {
		{Name: "enabled", Kind: "boolean"},
		{Name: "profileID", Kind: "string"},
	},
	"setqbittorrentingest": {
		{Name: "password", Kind: "string", Optional: true},
		{Name: "routes", Kind: "list", Optional: true},
		{Name: "url", Kind: "string"},
		{Name: "username", Kind: "string", Optional: true},
	},
	"setqualitypolicy": {
		{Name: "cutoff", Kind: "string"},
		{Name: "libraryID", Kind: "string"},
		{Name: "order", Kind: "list"},
	},
	"setrating": {
		{Name: "entityID", Kind: "string", Optional: true},
		{Name: "mediaID", Kind: "string", Optional: true},
		{Name: "rating", Kind: "number"},
	},
	"setremoteaccessportmapping": {
		{Name: "enabled", Kind: "boolean"},
	},
	"setretentioncandidatehold": {
		{Name: "hold", Kind: "boolean", Optional: true},
		{Name: "mediaID", Kind: "string"},
		{Name: "ruleID", Kind: "string"},
	},
	"setrevealpin": {
		{Name: "pin", Kind: "string"},
	},
	"setscrapercredential": {
		{Name: "authRef", Kind: "string"},
		{Name: "secret", Kind: "string"},
	},
	"setscrobbleeligibility": {
		{Name: "eligible", Kind: "boolean", Optional: true},
		{Name: "libraryID", Kind: "string"},
	},
	"setskipdetectionenabled": {
		{Name: "enabled", Kind: "boolean"},
		{Name: "libraryID", Kind: "string"},
	},
	"setsubscriptionretention": {
		{Name: "id", Kind: "string"},
		{Name: "kind", Kind: "string"},
		{Name: "n", Kind: "number", Optional: true},
	},
	"settranscodeconfig": {
		{Name: "clearMaxConcurrentTranscodes", Kind: "boolean", Optional: true},
		{Name: "codec", Kind: "string", Optional: true},
		{Name: "device", Kind: "string", Optional: true},
		{Name: "hardwareEnabled", Kind: "boolean", Optional: true},
		{Name: "maxConcurrentTranscodes", Kind: "number", Optional: true},
		{Name: "quality", Kind: "string", Optional: true},
		{Name: "tempDir", Kind: "string", Optional: true},
		{Name: "toneMapEnabled", Kind: "boolean", Optional: true},
	},
	"setupstate": {},
	"setwatched": {
		{Name: "entityID", Kind: "string", Optional: true},
		{Name: "mediaID", Kind: "string", Optional: true},
		{Name: "watched", Kind: "boolean"},
	},
	"setwatchlisted": {
		{Name: "entityID", Kind: "string", Optional: true},
		{Name: "mediaID", Kind: "string", Optional: true},
		{Name: "watchlisted", Kind: "boolean"},
	},
	"setwebhooksettings": {
		{Name: "publicBaseURL", Kind: "string", Optional: true},
	},
	"skipdetectionenabled": {},
	"smartcollections": {
		{Name: "libraryID", Kind: "string"},
	},
	"sourceitems":     sourceItemsRegistryArguments(),
	"stagedrestore":   {},
	"revealbackupkey": {},
	"backupkeystatus": {},
	"stagerestore": {
		{Name: "backupKey", Kind: "string"},
		{Name: "envelope", Kind: "string"},
		{Name: "replaceIdentity", Kind: "boolean", Optional: true},
	},
	"stationdelete": {
		{Name: "id", Kind: "string"},
	},
	"stationgenerate": {
		{Name: "rulesJSON", Kind: "string", Optional: true},
		{Name: "seed", Kind: "string", Optional: true},
		{Name: "seedID", Kind: "string"},
		{Name: "seedKind", Kind: "string"},
		{Name: "tuningJSON", Kind: "string", Optional: true},
	},
	"stations": {
		{Name: "libraryID", Kind: "string"},
	},
	"stationsave": {
		{Name: "libraryID", Kind: "string"},
		{Name: "name", Kind: "string"},
		{Name: "rulesJSON", Kind: "string", Optional: true},
		{Name: "seedJSON", Kind: "string", Optional: true},
		{Name: "tuningJSON", Kind: "string", Optional: true},
	},
	"stationstart": {
		{Name: "confirmReplace", Kind: "boolean", Optional: true},
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "rulesJSON", Kind: "string", Optional: true},
		{Name: "seed", Kind: "string", Optional: true},
		{Name: "seedID", Kind: "string"},
		{Name: "seedKind", Kind: "string"},
		{Name: "tuningJSON", Kind: "string", Optional: true},
	},
	"subscriptions": {
		{Name: "libraryID", Kind: "string"},
	},
	"suppressunmatched": {
		{Name: "candidateID", Kind: "string", Optional: true},
		{Name: "fingerprint", Kind: "string", Optional: true},
		{Name: "recordID", Kind: "string", Optional: true},
		{Name: "scope", Kind: "string"},
		{Name: "sourceHandle", Kind: "string", Optional: true},
	},
	"switchprofile": {
		{Name: "pin", Kind: "string", Optional: true},
		{Name: "profileID", Kind: "string"},
	},
	"syncofflinepackprogress": {
		{Name: "bytesReady", Kind: "int", Optional: true},
		{Name: "bytesTotal", Kind: "int", Optional: true},
		{Name: "id", Kind: "string"},
	},
	"tagassignments": {
		{Name: "targetID", Kind: "string"},
		{Name: "targetType", Kind: "string"},
	},
	"tags": {
		{Name: "includeTrashed", Kind: "boolean", Optional: true},
	},
	"terminatesession": {
		{Name: "sessionID", Kind: "string"},
	},
	"testwebhook": {
		{Name: "id", Kind: "string"},
	},
	"transcodeconfig": {},
	"transferprofileowner": {
		{Name: "profileID", Kind: "string"},
	},
	"transitiongroup": {
		{Name: "groupID", Kind: "string"},
		{Name: "item", Kind: "string"},
		{Name: "positionMS", Kind: "number", Optional: true},
	},
	"trashedlibraries": {},
	"trashedmarkers": {
		{Name: "mediaID", Kind: "string"},
	},
	"trashlist": {},
	"trashmedia": {
		{Name: "mediaID", Kind: "string"},
	},
	"unassigntag": {
		{Name: "tagID", Kind: "string"},
		{Name: "targetID", Kind: "string"},
		{Name: "targetType", Kind: "string"},
	},
	"undobatch": {
		{Name: "batchID", Kind: "string"},
	},
	"uninstallplugindata": {
		{Name: "confirm", Kind: "boolean"},
		{Name: "pluginID", Kind: "string"},
	},
	"unlinkscrobble": {
		{Name: "service", Kind: "string"},
	},
	"unmatchedqueue": {
		{Name: "limit", Kind: "int", Optional: true},
		{Name: "offset", Kind: "int", Optional: true},
	},
	"unmatchedrecords": {},
	"unmatchedsuppressions": {
		{Name: "recordID", Kind: "string", Optional: true},
	},
	"unsealencryptedlibrary": {
		{Name: "libraryID", Kind: "string"},
		{Name: "passphrase", Kind: "string", Optional: true},
		{Name: "recoveryKey", Kind: "string", Optional: true},
	},
	"updatechannel": {
		{Name: "id", Kind: "string"},
		{Name: "name", Kind: "string", Optional: true},
		{Name: "ordering", Kind: "string", Optional: true},
		{Name: "repeat", Kind: "string", Optional: true},
		{Name: "sourceExpr", Kind: "string", Optional: true},
	},
	"updatecollection": {
		{Name: "id", Kind: "string"},
		{Name: "name", Kind: "string", Optional: true},
		{Name: "parentID", Kind: "string", Optional: true},
		{Name: "sortOrder", Kind: "int", Optional: true},
	},
	"updatemarker": {
		{Name: "endSeconds", Kind: "number", Optional: true},
		{Name: "markerID", Kind: "string"},
		{Name: "segmentKind", Kind: "string", Optional: true},
		{Name: "startSeconds", Kind: "number", Optional: true},
		{Name: "tags", Kind: "list", Optional: true},
		{Name: "title", Kind: "string"},
	},
	"reorderprofiles": {
		{Name: "profileIDs", Kind: "list"},
	},
	"setprofileavatar": {
		{Name: "avatar", Kind: "string"},
		{Name: "profileID", Kind: "string"},
	},
	"updateprofile": {
		{Name: "audioLanguages", Kind: "list", Optional: true},
		{Name: "disabled", Kind: "boolean", Optional: true},
		{Name: "entryPIN", Kind: "string", Optional: true},
		{Name: "kind", Kind: "string", Optional: true},
		{Name: "maxConcurrentStreams", Kind: "number", Optional: true},
		{Name: "name", Kind: "string", Optional: true},
		{Name: "preferSDH", Kind: "boolean", Optional: true},
		{Name: "profileID", Kind: "string"},
		{Name: "remoteMaxBitrate", Kind: "number", Optional: true},
		{Name: "subtitleLanguages", Kind: "list", Optional: true},
		{Name: "subtitleMode", Kind: "string", Optional: true},
		{Name: "transcodeAllowed", Kind: "boolean", Optional: true},
	},
	"updateretentionrule": {
		{Name: "enabled", Kind: "boolean", Optional: true},
		{Name: "graceDays", Kind: "number", Optional: true},
		{Name: "id", Kind: "string"},
		{Name: "name", Kind: "string", Optional: true},
		{Name: "revealedAtAuthoring", Kind: "boolean", Optional: true},
		{Name: "sourceExpr", Kind: "string", Optional: true},
		{Name: "targetLibraryID", Kind: "string", Optional: true},
	},
	"updateschedule": {
		{Name: "enabled", Kind: "boolean", Optional: true},
		{Name: "id", Kind: "string"},
		{Name: "trigger", Kind: "object", Optional: true},
	},
	"updatesmartcollection": {
		{Name: "id", Kind: "string"},
		{Name: "name", Kind: "string", Optional: true},
		{Name: "sortExpr", Kind: "string", Optional: true},
		{Name: "sourceExpr", Kind: "string", Optional: true},
	},
	"updatestatus": {},
	"updatetag": {
		{Name: "name", Kind: "string", Optional: true},
		{Name: "parentTagID", Kind: "string", Optional: true},
		{Name: "tagID", Kind: "string"},
	},
	"upsertentityrelation": {
		{Name: "fromEntityID", Kind: "string"},
		{Name: "relation", Kind: "string"},
		{Name: "sortOrder", Kind: "number", Optional: true},
		{Name: "toEntityID", Kind: "string", Optional: true},
		{Name: "toMediaID", Kind: "string", Optional: true},
	},
	"validatescrapercredential": {
		{Name: "authRef", Kind: "string"},
	},
	"verifyadminsession": {
		{Name: "createFirst", Kind: "boolean", Optional: true},
		{Name: "password", Kind: "string"},
	},
	"verifybackup": {
		{Name: "manifest", Kind: "string"},
	},
	"watcherstatus": {},
	"watchpartyautoskip": {
		{Name: "item", Kind: "string"},
		{Name: "partyID", Kind: "string"},
		{Name: "positionMS", Kind: "number", Optional: true},
	},
	"watchpartytransport": {
		{Name: "decline", Kind: "boolean", Optional: true},
		{Name: "item", Kind: "string"},
		{Name: "partyID", Kind: "string"},
		{Name: "paused", Kind: "boolean", Optional: true},
		{Name: "positionMS", Kind: "number", Optional: true},
		{Name: "request", Kind: "string", Optional: true},
	},
	"webhookdeliveries": {},
	"webhooks":          {},
	"webhooksettings":   {},
}

// sourceItemsRegistryArguments is the sourceItems resolver's argument list:
// the wire nested-schema arguments plus home (CORE-2360), which the wire
// nested schema leaves out because its arguments carry no boolean kind.
// ambientQueue runs the same resolver, so it lists the same arguments.
func sourceItemsRegistryArguments() []NestedArgument {
	return slices.Concat(fieldArguments["sourceitems"], []NestedArgument{
		{Name: "home", Kind: "boolean", Optional: true},
	})
}
