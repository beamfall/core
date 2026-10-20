package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/observability"
	"github.com/beamfall/core/internal/store"
)

type fieldDispatchContext struct {
	handler            *Handler
	ctx                context.Context
	scope              auth.Scope
	req                request
	fields             map[string]bool
	data               map[string]any
	errs               []graphError
	privilegedOutcomes map[string]observability.AuditOutcome
	canonical          string
	responseKey        string
}

type fieldDispatchFunc func(*fieldDispatchContext) response

type fieldKind uint8

const (
	fieldRead fieldKind = iota + 1
	fieldMutation
)

type fieldRoleGate uint8

const (
	rolePublic fieldRoleGate = 0
)

const (
	roleSession fieldRoleGate = 1 << iota
	roleManager
	roleAdmin
)

type fieldDispatchOrder uint16
type fieldDispatchPlan []fieldDispatchOrder

type fieldHandler struct {
	wireName string
	dataKey  string
	errField string
	kind     fieldKind
	roleGate fieldRoleGate
	run      fieldDispatchPlan
}

func newFieldHandler(dataKey, errField string, kind fieldKind, roleGate fieldRoleGate, orders ...fieldDispatchOrder) fieldHandler {
	return fieldHandler{
		wireName: dataKey,
		dataKey:  dataKey,
		errField: errField,
		kind:     kind,
		roleGate: roleGate,
		run:      orders,
	}
}

func newAliasedFieldHandler(wireName, dataKey, errField string, kind fieldKind, roleGate fieldRoleGate, orders ...fieldDispatchOrder) fieldHandler {
	handler := newFieldHandler(dataKey, errField, kind, roleGate, orders...)
	handler.wireName = wireName
	return handler
}

func resolveField[T any](dispatch *fieldDispatchContext, field string, result T, err error) response {
	handler := fieldHandlerRegistry[field]
	dataKey, errorPath := handler.dataKey, handler.dataKey
	if field == dispatch.canonical && dispatch.responseKey != "" {
		dataKey, errorPath = dispatch.responseKey, dispatch.responseKey
	}
	outcome := observability.AuditSuccess
	if err != nil {
		outcome = observability.AuditError
	}
	recordPrivilegedOutcome(dispatch, field, outcome)
	if handler.kind == fieldRead {
		readField(dispatch.data, &dispatch.errs, dataKey, handler.errField, errorPath, result, err)
		return response{}
	}
	if err != nil {
		return graphFail(handler.errField, err)
	}
	dispatch.data[dataKey] = result
	return response{}
}

func setField[T any](dispatch *fieldDispatchContext, field string, result T) {
	handler := fieldHandlerRegistry[field]
	dataKey := handler.dataKey
	if field == dispatch.canonical && dispatch.responseKey != "" {
		dataKey = dispatch.responseKey
	}
	dispatch.data[dataKey] = result
	recordPrivilegedOutcome(dispatch, field, observability.AuditSuccess)
}

func recordPrivilegedOutcome(dispatch *fieldDispatchContext, field string, outcome observability.AuditOutcome) {
	if adminFields[field] {
		key := field
		if field == dispatch.canonical && dispatch.responseKey != "" {
			key = dispatch.responseKey
		}
		dispatch.privilegedOutcomes[key] = outcome
	}
}

func requestedField(fields map[string]bool, names ...string) string {
	for _, name := range names {
		if fields[name] {
			return name
		}
	}
	return names[0]
}

func authorizeDispatchOrder(dispatch *fieldDispatchContext, order fieldDispatchOrder) response {
	var gate fieldRoleGate
	for field, requested := range dispatch.fields {
		if !requested {
			continue
		}
		handler, ok := fieldHandlerRegistry[field]
		if !ok {
			continue
		}
		for _, candidate := range handler.run {
			if candidate == order {
				gate |= handler.roleGate
				break
			}
		}
	}
	if gate&roleManager != 0 && dispatch.scope.DeviceRole != store.DeviceRoleManager {
		return response{
			Errors:               []graphError{{Message: "manager role required"}},
			authorizationRefused: true,
		}
	}
	if gate&roleAdmin != 0 && !dispatch.scope.Admin {
		return response{
			Errors:               []graphError{{Message: "admin session required"}},
			authorizationRefused: true,
		}
	}
	return response{}
}

// fieldDispatchRuns is the single order-to-function authority. Field handlers
// reference only these orders, so two fields can share a block but a global
// order can never resolve to different functions through map iteration.
var fieldDispatchRuns = [...]fieldDispatchFunc{
	0:   dispatchFieldBlock000,
	1:   dispatchFieldBlock001,
	2:   dispatchFieldBlock002,
	3:   dispatchFieldBlock003,
	4:   dispatchFieldBlock004,
	5:   dispatchFieldBlock005,
	6:   dispatchFieldBlock006,
	7:   dispatchFieldBlock007,
	8:   dispatchFieldBlock008,
	9:   dispatchFieldBlock009,
	10:  dispatchFieldBlock010,
	11:  dispatchFieldBlock011,
	12:  dispatchFieldBlock012,
	13:  dispatchFieldBlock013,
	14:  dispatchFieldBlock014,
	15:  dispatchFieldBlock015,
	16:  dispatchFieldBlock016,
	17:  dispatchFieldBlock017,
	18:  dispatchFieldBlock018,
	19:  dispatchFieldBlock019,
	20:  dispatchFieldBlock020,
	21:  dispatchFieldBlock021,
	22:  dispatchFieldBlock022,
	23:  dispatchFieldBlock023,
	24:  dispatchFieldBlock024,
	25:  dispatchFieldBlock025,
	26:  dispatchFieldBlock026,
	27:  dispatchFieldBlock027,
	28:  dispatchFieldBlock028,
	29:  dispatchFieldBlock029,
	30:  dispatchFieldBlock030,
	31:  dispatchFieldBlock031,
	32:  dispatchFieldBlock032,
	33:  dispatchFieldBlock033,
	34:  dispatchFieldBlock034,
	35:  dispatchFieldBlock035,
	36:  dispatchFieldBlock036,
	37:  dispatchFieldBlock037,
	38:  dispatchFieldBlock038,
	39:  dispatchFieldBlock039,
	40:  dispatchFieldBlock040,
	41:  dispatchFieldBlock041,
	42:  dispatchFieldBlock042,
	43:  dispatchFieldBlock043,
	44:  dispatchFieldBlock044,
	45:  dispatchFieldBlock045,
	46:  dispatchFieldBlock046,
	47:  dispatchFieldBlock047,
	48:  dispatchFieldBlock048,
	49:  dispatchFieldBlock049,
	50:  dispatchFieldBlock050,
	51:  dispatchFieldBlock051,
	52:  dispatchFieldBlock052,
	53:  dispatchFieldBlock053,
	54:  dispatchFieldBlock054,
	55:  dispatchFieldBlock055,
	56:  dispatchFieldBlock056,
	57:  dispatchFieldBlock057,
	58:  dispatchFieldBlock058,
	59:  dispatchFieldBlock059,
	60:  dispatchFieldBlock060,
	61:  dispatchFieldBlock061,
	62:  dispatchFieldBlock062,
	63:  dispatchFieldBlock063,
	64:  dispatchFieldBlock064,
	65:  dispatchFieldBlock065,
	66:  dispatchFieldBlock066,
	67:  dispatchFieldBlock067,
	68:  dispatchFieldBlock068,
	69:  dispatchFieldBlock069,
	70:  dispatchFieldBlock070,
	71:  dispatchFieldBlock071,
	72:  dispatchFieldBlock072,
	73:  dispatchFieldBlock073,
	74:  dispatchFieldBlock074,
	75:  dispatchFieldBlock075,
	76:  dispatchFieldBlock076,
	77:  dispatchFieldBlock077,
	78:  dispatchFieldBlock078,
	79:  dispatchFieldBlock079,
	80:  dispatchFieldBlock080,
	81:  dispatchFieldBlock081,
	82:  dispatchFieldBlock082,
	83:  dispatchFieldBlock083,
	84:  dispatchFieldBlock084,
	85:  dispatchFieldBlock085,
	86:  dispatchFieldBlock086,
	87:  dispatchFieldBlock087,
	88:  dispatchFieldBlock088,
	89:  dispatchFieldBlock089,
	90:  nil, // retired duplicate notifications dispatch slot
	91:  nil, // retired duplicate markNotificationRead dispatch slot
	92:  dispatchFieldBlock092,
	93:  dispatchFieldBlock093,
	94:  dispatchFieldBlock094,
	95:  dispatchFieldBlock095,
	96:  dispatchFieldBlock096,
	97:  dispatchFieldBlock097,
	98:  dispatchFieldBlock098,
	99:  dispatchFieldBlock099,
	100: dispatchFieldBlock100,
	101: dispatchFieldBlock101,
	102: dispatchFieldBlock102,
	103: dispatchFieldBlock103,
	104: dispatchFieldBlock104,
	105: dispatchFieldBlock105,
	106: dispatchFieldBlock106,
	107: dispatchFieldBlock107,
	108: dispatchFieldBlock108,
	109: dispatchFieldBlock109,
	110: dispatchFieldBlock110,
	111: dispatchFieldBlock111,
	112: dispatchFieldBlock112,
	113: dispatchFieldBlock113,
	114: dispatchFieldBlock114,
	115: dispatchFieldBlock115,
	116: dispatchFieldBlock116,
	117: dispatchFieldBlock117,
	118: dispatchFieldBlock118,
	119: dispatchFieldBlock119,
	120: dispatchFieldBlock120,
	121: dispatchFieldBlock121,
	122: dispatchFieldBlock122,
	123: dispatchFieldBlock123,
	124: dispatchFieldBlock124,
	125: dispatchFieldBlock125,
	126: dispatchFieldBlock126,
	127: dispatchFieldBlock127,
	128: dispatchFieldBlock128,
	129: dispatchFieldBlock129,
	130: dispatchFieldBlock130,
	131: dispatchFieldBlock131,
	132: dispatchFieldBlock132,
	133: dispatchFieldBlock133,
	134: dispatchFieldBlock134,
	135: dispatchFieldBlock135,
	136: dispatchFieldBlock136,
	137: dispatchFieldBlock137,
	138: dispatchFieldBlock138,
	139: dispatchFieldBlock139,
	140: dispatchFieldBlock140,
	141: dispatchFieldBlock141,
	142: dispatchFieldBlock142,
	143: dispatchFieldBlock143,
	144: dispatchFieldBlock144,
	145: dispatchFieldBlock145,
}

var fieldHandlerRegistry = map[string]fieldHandler{
	"acceptpendingaitag":                 newFieldHandler("acceptPendingAITag", "acceptPendingAITag", fieldMutation, roleSession|roleAdmin, 77),
	"acceptwatchparty":                   newFieldHandler("acceptWatchParty", "acceptWatchParty", fieldMutation, roleSession, 138),
	"accessgrantsforprofile":             newFieldHandler("accessGrantsForProfile", "accessGrantsForProfile", fieldRead, roleSession, 101),
	"accessrequest":                      newFieldHandler("accessRequest", "accessRequest", fieldRead, roleSession, 99),
	"accessschedule":                     newFieldHandler("accessSchedule", "accessSchedule", fieldRead, roleSession|roleAdmin, 41),
	"activity":                           newFieldHandler("activity", "activity", fieldRead, roleSession, 84),
	"activitylog":                        newFieldHandler("activityLog", "activityLog", fieldRead, roleSession, 83),
	"addcollectionmedia":                 newFieldHandler("addCollectionMedia", "addCollectionMedia", fieldMutation, roleSession|roleManager, 57),
	"addmediaexternalid":                 newFieldHandler("addMediaExternalID", "addMediaExternalID", fieldMutation, roleSession|roleManager, 61),
	"addpathpickerroot":                  newFieldHandler("addPathPickerRoot", "addPathPickerRoot", fieldMutation, roleSession|roleManager|roleAdmin, 109),
	"addsubscription":                    newFieldHandler("addSubscription", "addSubscription", fieldMutation, roleSession|roleManager, 54),
	"adminsession":                       newFieldHandler("adminSession", "", fieldRead, roleSession, 72),
	"ambientqueue":                       newFieldHandler("ambientQueue", "ambientQueue", fieldRead, roleSession, 18),
	"apikeys":                            newFieldHandler("apiKeys", "apiKeys", fieldRead, roleSession|roleAdmin, 73),
	"apitokens":                          newFieldHandler("apiTokens", "apiTokens", fieldRead, roleSession|roleAdmin, 79),
	"approvepluginpermission":            newFieldHandler("approvePluginPermission", "approvePluginPermission", fieldMutation, roleSession|roleAdmin, 66),
	"artworkcandidates":                  newFieldHandler("artworkCandidates", "artworkCandidates", fieldRead, roleSession|roleManager, 16),
	"assigntag":                          newFieldHandler("assignTag", "assignTag", fieldMutation, roleSession|roleManager, 36),
	"associatesubscriptionentry":         newFieldHandler("associateSubscriptionEntry", "associateSubscriptionEntry", fieldMutation, roleSession|roleManager, 54),
	"backup":                             newFieldHandler("backup", "backup", fieldMutation, roleSession|roleAdmin, 65),
	"backuphistory":                      newFieldHandler("backupHistory", "backupHistory", fieldRead, roleSession|roleAdmin, 65),
	"batchrenamemanagedpaths":            newFieldHandler("batchRenameManagedPaths", "batchRenameManagedPaths", fieldMutation, roleSession|roleManager, 110),
	"browseserverfolders":                newFieldHandler("browseServerFolders", "browseServerFolders", fieldRead, roleSession|roleManager|roleAdmin, 109),
	"bulkassigntags":                     newFieldHandler("bulkAssignTags", "bulkAssignTags", fieldMutation, roleSession|roleManager, 36),
	"bulkeditapply":                      newFieldHandler("bulkEditApply", "bulkEditApply", fieldMutation, roleSession|roleManager, 110),
	"bulkeditpreview":                    newFieldHandler("bulkEditPreview", "bulkEditPreview", fieldRead, roleSession|roleManager, 110),
	"bulkfetchmissingartwork":            newFieldHandler("bulkFetchMissingArtwork", "bulkFetchMissingArtwork", fieldMutation, roleSession|roleAdmin, 74),
	"bulkfetchmissingartworkselected":    newFieldHandler("bulkFetchMissingArtworkSelected", "bulkFetchMissingArtworkSelected", fieldMutation, roleSession|roleAdmin, 74),
	"bulkmergeduplicates":                newFieldHandler("bulkMergeDuplicates", "bulkMergeDuplicates", fieldMutation, roleSession|roleAdmin, 74),
	"bulkmergeduplicatesselected":        newFieldHandler("bulkMergeDuplicatesSelected", "bulkMergeDuplicatesSelected", fieldMutation, roleSession|roleAdmin, 74),
	"bulkrescanunmatched":                newFieldHandler("bulkRescanUnmatched", "bulkRescanUnmatched", fieldMutation, roleSession|roleAdmin, 74),
	"bulkrescanunmatchedselected":        newFieldHandler("bulkRescanUnmatchedSelected", "bulkRescanUnmatchedSelected", fieldMutation, roleSession|roleAdmin, 74),
	"bulkrescrapescrapefailures":         newFieldHandler("bulkRescrapeScrapeFailures", "bulkRescrapeScrapeFailures", fieldMutation, roleSession|roleAdmin, 74),
	"bulkrescrapescrapefailuresselected": newFieldHandler("bulkRescrapeScrapeFailuresSelected", "bulkRescrapeScrapeFailuresSelected", fieldMutation, roleSession|roleAdmin, 74),
	"bulktrashbroken":                    newFieldHandler("bulkTrashBroken", "bulkTrashBroken", fieldMutation, roleSession|roleAdmin, 74),
	"bulktrashbrokenselected":            newFieldHandler("bulkTrashBrokenSelected", "bulkTrashBrokenSelected", fieldMutation, roleSession|roleAdmin, 74),
	"capabilitystates":                   newFieldHandler("capabilityStates", "capabilityStates", fieldRead, roleSession|roleAdmin, 67),
	"clearallmutes":                      newFieldHandler("clearAllMutes", "clearAllMutes", fieldMutation, roleSession, 125),
	"clearmute":                          newFieldHandler("clearMute", "clearMute", fieldMutation, roleSession, 124),
	"clientprefs":                        newFieldHandler("clientPrefs", "client preferences", fieldRead, roleSession, 43),
	"collections":                        newFieldHandler("collections", "collections", fieldRead, roleSession, 57),
	"connectionendpoints":                newFieldHandler("connectionEndpoints", "", fieldRead, roleSession, 104),
	"consoleconfig":                      newFieldHandler("consoleConfig", "consoleConfig", fieldRead, roleSession|roleAdmin, 66),
	"controlsession":                     newFieldHandler("controlSession", "controlSession", fieldMutation, roleSession, 130),
	"createapikey":                       newFieldHandler("createApiKey", "createApiKey", fieldMutation, roleSession|roleAdmin, 73),
	"createapitoken":                     newFieldHandler("createApiToken", "createApiToken", fieldMutation, roleSession|roleAdmin, 79),
	"createcollection":                   newFieldHandler("createCollection", "createCollection", fieldMutation, roleSession|roleManager, 57),
	"createlibrary":                      newFieldHandler("createLibrary", "createLibrary", fieldMutation, roleSession|roleAdmin, 44),
	"createmanagedfolder":                newFieldHandler("createManagedFolder", "createManagedFolder", fieldMutation, roleSession|roleManager, 110),
	"createmarker":                       newFieldHandler("createMarker", "createMarker", fieldMutation, roleSession|roleManager, 14),
	"createofflinepack":                  newFieldHandler("createOfflinePack", "createOfflinePack", fieldMutation, roleSession, 118),
	"channels":                           newFieldHandler("channels", "channels", fieldRead, roleSession, 57),
	"createchannel":                      newFieldHandler("createChannel", "createChannel", fieldMutation, roleSession|roleManager, 57),
	"createprofile":                      newFieldHandler("createProfile", "createProfile", fieldMutation, roleSession|roleAdmin, 37),
	"createretentionrule":                newFieldHandler("createRetentionRule", "createRetentionRule", fieldMutation, roleSession|roleAdmin, 58),
	"createsavedfilter":                  newFieldHandler("createSavedFilter", "createSavedFilter", fieldMutation, roleSession, 55),
	"createsmartcollection":              newFieldHandler("createSmartCollection", "createSmartCollection", fieldMutation, roleSession|roleManager, 57),
	"createtag":                          newFieldHandler("createTag", "createTag", fieldMutation, roleSession|roleManager, 36),
	"createwatchparty":                   newFieldHandler("createWatchParty", "createWatchParty", fieldMutation, roleSession, 136),
	"currentsession":                     newFieldHandler("currentSession", "", fieldRead, roleSession, 1),
	"deletechannel":                      newFieldHandler("deleteChannel", "deleteChannel", fieldMutation, roleSession|roleManager, 57),
	"deletecollection":                   newFieldHandler("deleteCollection", "deleteCollection", fieldMutation, roleSession|roleManager, 57),
	"deletelibrary":                      newFieldHandler("deleteLibrary", "deleteLibrary", fieldMutation, roleSession|roleAdmin, 51),
	"deletemanagedpath":                  newFieldHandler("deleteManagedPath", "deleteManagedPath", fieldMutation, roleSession|roleManager, 110),
	"deletemarker":                       newFieldHandler("deleteMarker", "deleteMarker", fieldMutation, roleSession|roleManager, 14),
	"deletemyengagement":                 newFieldHandler("deleteMyEngagement", "deleteMyEngagement", fieldMutation, roleSession, 115),
	"deletemypacktokens":                 newFieldHandler("deleteMyPackTokens", "deleteMyPackTokens", fieldMutation, roleSession, 121),
	"deleteretentionrule":                newFieldHandler("deleteRetentionRule", "deleteRetentionRule", fieldMutation, roleSession|roleAdmin, 58),
	"deletesavedfilter":                  newFieldHandler("deleteSavedFilter", "deleteSavedFilter", fieldMutation, roleSession, 55),
	"deletesmartcollection":              newFieldHandler("deleteSmartCollection", "deleteSmartCollection", fieldMutation, roleSession|roleManager, 57),
	"deletetag":                          newFieldHandler("deleteTag", "deleteTag", fieldMutation, roleSession|roleManager, 36),
	"devices":                            newFieldHandler("devices", "devices", fieldRead, roleSession|roleAdmin, 145),
	"disablescrapercredential":           newFieldHandler("disableScraperCredential", "disableScraperCredential", fieldMutation, roleSession|roleAdmin, 66),
	"createmediafromunmatched":           newFieldHandler("createMediaFromUnmatched", "createMediaFromUnmatched", fieldMutation, roleSession|roleManager, 61),
	"dismissunmatched":                   newFieldHandler("dismissUnmatched", "dismissUnmatched", fieldMutation, roleSession|roleManager, 61),
	"encryptedlibrarystatus":             newFieldHandler("encryptedLibraryStatus", "encryptedLibraryStatus", fieldRead, roleSession|roleAdmin, 107),
	"endadminsession":                    newFieldHandler("endAdminSession", "endAdminSession", fieldMutation, roleSession, 71),
	"engagementcounter":                  newFieldHandler("engagementCounter", "engagementCounter", fieldRead, roleSession, 126),
	"entitiesformedia":                   newFieldHandler("entitiesForMedia", "entitiesForMedia", fieldRead, roleSession, 6),
	"entity":                             newFieldHandler("entity", "entity", fieldRead, roleSession, 11),
	"entityrelations":                    newFieldHandler("entityRelations", "entityRelations", fieldRead, roleSession, 7),
	"evictplaybackrenditioncache":        newFieldHandler("evictPlaybackRenditionCache", "evictPlaybackRenditionCache", fieldMutation, roleSession|roleAdmin, 75),
	"extensionrequest":                   newFieldHandler("extensionRequest", "extensionRequest", fieldRead, roleSession, 95),
	"fixmatch":                           newFieldHandler("fixMatch", "fixMatch", fieldMutation, roleSession|roleManager, 60),
	"fixmatchcandidates":                 newFieldHandler("fixMatchCandidates", "fixMatchCandidates", fieldRead, roleSession|roleManager, 60),
	"fixmatchpreview":                    newFieldHandler("fixMatchPreview", "fixMatchPreview", fieldRead, roleSession|roleManager, 60),
	"foldertree":                         newFieldHandler("folderTree", "folderTree", fieldRead, roleSession|roleManager, 110),
	"formgroup":                          newFieldHandler("formGroup", "formGroup", fieldMutation, roleSession, 131),
	"identifyapply":                      newFieldHandler("identifyApply", "identifyApply", fieldMutation, roleSession|roleManager, 60),
	"identifylookup":                     newFieldHandler("identifyLookup", "identifyLookup", fieldRead, roleSession|roleManager, 60),
	"identifysearch":                     newFieldHandler("identifySearch", "identifySearch", fieldRead, roleSession|roleManager, 60),
	"importers":                          newFieldHandler("importers", "", fieldRead, roleSession|roleAdmin, 81),
	"importmanagedfile":                  newFieldHandler("importManagedFile", "importManagedFile", fieldMutation, roleSession|roleAdmin, 111),
	"importstatus":                       newFieldHandler("importStatus", "importStatus", fieldRead, roleSession|roleAdmin, 81),
	"invitetowatchparty":                 newFieldHandler("inviteToWatchParty", "inviteToWatchParty", fieldMutation, roleSession, 137),
	"job":                                newFieldHandler("job", "job", fieldRead, roleSession|roleAdmin, 63),
	"jobcancel":                          newFieldHandler("jobCancel", "jobCancel", fieldMutation, roleSession|roleAdmin, 63),
	"jobevents":                          newFieldHandler("jobEvents", "jobEvents", fieldRead, roleSession|roleAdmin, 63),
	"jobretry":                           newFieldHandler("jobRetry", "jobRetry", fieldMutation, roleSession|roleAdmin, 63),
	"jobs":                               newFieldHandler("jobs", "jobs", fieldRead, roleSession|roleAdmin, 63),
	"joingroup":                          newFieldHandler("joinGroup", "joinGroup", fieldMutation, roleSession, 132),
	"leavegroup":                         newFieldHandler("leaveGroup", "leaveGroup", fieldMutation, roleSession, 133),
	"leavewatchparty":                    newFieldHandler("leaveWatchParty", "leaveWatchParty", fieldMutation, roleSession, 142),
	"libraries":                          newFieldHandler("libraries", "libraries", fieldRead, roleSession|roleAdmin, 45),
	"libraryduplicategroups":             newFieldHandler("libraryDuplicateGroups", "libraryDuplicateGroups", fieldRead, roleSession|roleAdmin, 74),
	"libraryhealth":                      newFieldHandler("libraryHealth", "libraryHealth", fieldRead, roleSession|roleAdmin, 74),
	"libraryhealthitems":                 newFieldHandler("libraryHealthItems", "libraryHealthItems", fieldRead, roleSession|roleAdmin, 74),
	"libraryindexstatus":                 newFieldHandler("libraryIndexStatus", "libraryIndexStatus", fieldRead, roleSession|roleAdmin, 77),
	"linkmediarelation":                  newFieldHandler("linkMediaRelation", "linkMediaRelation", fieldMutation, roleSession|roleManager, 110),
	"linkscrobble":                       newFieldHandler("linkScrobble", "linkScrobble", fieldMutation, roleSession, 27),
	"livesessions":                       newFieldHandler("liveSessions", "liveSessions", fieldRead, roleSession|roleAdmin, 145),
	"manifest":                           newAliasedFieldHandler("manifest", "navigationManifest", "manifest", fieldRead, roleSession, 4),
	"markers":                            newFieldHandler("markers", "markers", fieldRead, roleSession, 12),
	"marknotificationread":               newFieldHandler("markNotificationRead", "markNotificationRead", fieldMutation, roleSession, 89),
	"media":                              newFieldHandler("media", "media", fieldRead, roleSession, 24),
	"mediafieldlocks":                    newFieldHandler("mediaFieldLocks", "mediaFieldLocks", fieldRead, roleSession|roleManager, 108),
	"mediafields":                        newFieldHandler("mediaFields", "mediaFields", fieldRead, roleSession, 15),
	"mediafiles":                         newFieldHandler("mediaFiles", "mediaFiles", fieldRead, roleSession, 9),
	"mediaforentity":                     newFieldHandler("mediaForEntity", "mediaForEntity", fieldRead, roleSession, 8),
	"mediarelations":                     newFieldHandler("mediaRelations", "mediaRelations", fieldRead, roleSession, 10),
	"mergeentities":                      newFieldHandler("mergeEntities", "mergeEntities", fieldMutation, roleSession|roleManager, 110),
	"mergemedia":                         newFieldHandler("mergeMedia", "mergeMedia", fieldMutation, roleSession|roleManager, 110),
	"metadataexport":                     newFieldHandler("metadataExport", "metadataExport", fieldRead, roleSession|roleAdmin, 76),
	"movemanagedpath":                    newFieldHandler("moveManagedPath", "moveManagedPath", fieldMutation, roleSession|roleManager, 110),
	"mutes":                              newFieldHandler("mutes", "mutes", fieldRead, roleSession, 122),
	"mydatasummary":                      newFieldHandler("myDataSummary", "myDataSummary", fieldRead, roleSession, 113),
	"myengagement":                       newFieldHandler("myEngagement", "myEngagement", fieldRead, roleSession, 112),
	"myengagementlog":                    newFieldHandler("myEngagementLog", "myEngagementLog", fieldRead, roleSession, 114),
	"mysessions":                         newFieldHandler("mySessions", "mySessions", fieldRead, roleSession, 116),
	"navigationmanifest":                 newFieldHandler("navigationManifest", "manifest", fieldRead, roleSession, 4),
	"navoverride":                        newFieldHandler("navOverride", "navOverride", fieldRead, roleSession|roleManager, 62),
	"navoverridehistory":                 newFieldHandler("navOverrideHistory", "navOverrideHistory", fieldRead, roleSession|roleManager, 62),
	"notifications":                      newFieldHandler("notifications", "notifications", fieldRead, roleSession, 88),
	"offlinepacks":                       newFieldHandler("offlinePacks", "offlinePacks", fieldRead, roleSession, 117),
	"operationsgovernor":                 newFieldHandler("operationsGovernor", "operationsGovernor", fieldRead, roleSession|roleAdmin, 63),
	"operatordeleteprofiledata":          newFieldHandler("operatorDeleteProfileData", "operatorDeleteProfileData", fieldMutation, roleSession|roleAdmin, 143),
	"organizeapply":                      newFieldHandler("organizeApply", "organizeApply", fieldMutation, roleSession|roleManager, 110),
	"organizepreview":                    newFieldHandler("organizePreview", "organizePreview", fieldRead, roleSession|roleManager, 110),
	"pathpicker":                         newFieldHandler("pathPicker", "pathPicker", fieldRead, roleSession|roleManager|roleAdmin, 109),
	"pendingaccessrequests":              newFieldHandler("pendingAccessRequests", "pendingAccessRequests", fieldRead, roleSession, 100),
	"pendingaitagqueue":                  newFieldHandler("pendingAITagQueue", "pendingAITagQueue", fieldRead, roleSession|roleAdmin, 77),
	"pinartworkcandidate":                newFieldHandler("pinArtworkCandidate", "pinArtworkCandidate", fieldMutation, roleSession|roleManager, 16),
	"playbackrenditioncache":             newFieldHandler("playbackRenditionCache", "playbackRenditionCache", fieldRead, roleSession|roleAdmin, 75),
	"plugins":                            newFieldHandler("plugins", "", fieldRead, roleSession, 5),
	"pluginworker":                       newFieldHandler("pluginWorker", "pluginWorker", fieldRead, roleSession|roleAdmin, 66),
	"precomputeplaybackrenditions":       newFieldHandler("precomputePlaybackRenditions", "precomputePlaybackRenditions", fieldMutation, roleSession|roleAdmin, 75),
	"profiles":                           newFieldHandler("profiles", "profiles", fieldRead, roleSession, 2),
	"profilesessions":                    newFieldHandler("profileSessions", "profileSessions", fieldRead, roleSession, 129),
	"provisionencryptedlibrary":          newFieldHandler("provisionEncryptedLibrary", "provisionEncryptedLibrary", fieldMutation, roleSession|roleAdmin, 107),
	"purgetrash":                         newFieldHandler("purgeTrash", "purgeTrash", fieldMutation, roleSession|roleManager|roleAdmin, 35),
	"qbittorrentingest":                  newFieldHandler("qbittorrentIngest", "qbittorrentIngest", fieldRead, roleSession|roleAdmin, 106),
	"qualitypolicy":                      newFieldHandler("qualityPolicy", "qualityPolicy", fieldRead, roleSession|roleAdmin, 50),
	"queueadd":                           newFieldHandler("queueAdd", "queue", fieldMutation, roleSession, 30),
	"queueclear":                         newFieldHandler("queueClear", "queue", fieldMutation, roleSession, 30),
	"queuemove":                          newFieldHandler("queueMove", "queue", fieldMutation, roleSession, 30),
	"queueremove":                        newFieldHandler("queueRemove", "queue", fieldMutation, roleSession, 30),
	"recordengagement":                   newFieldHandler("recordEngagement", "recordEngagement", fieldMutation, roleSession, 127),
	"registerdevicepushtoken":            newFieldHandler("registerDevicePushToken", "registerDevicePushToken", fieldMutation, roleSession, 92),
	"registerimportsource":               newFieldHandler("registerImportSource", "registerImportSource", fieldMutation, roleSession|roleAdmin, 111),
	"rejectpendingaitag":                 newFieldHandler("rejectPendingAITag", "rejectPendingAITag", fieldMutation, roleSession|roleAdmin, 77),
	"relock":                             newFieldHandler("relock", "reveal", fieldMutation, roleSession, 25),
	"remoteaccess":                       newFieldHandler("remoteAccess", "remoteAccess", fieldRead, roleSession|roleAdmin, 103),
	"removecollectionmedia":              newFieldHandler("removeCollectionMedia", "removeCollectionMedia", fieldMutation, roleSession|roleManager, 57),
	"removelastengagement":               newFieldHandler("removeLastEngagement", "removeLastEngagement", fieldMutation, roleSession, 128),
	"removesubscription":                 newFieldHandler("removeSubscription", "removeSubscription", fieldMutation, roleSession|roleManager, 54),
	"renamemanagedpath":                  newFieldHandler("renameManagedPath", "renameManagedPath", fieldMutation, roleSession|roleManager, 110),
	"reordercollectionmedia":             newFieldHandler("reorderCollectionMedia", "reorderCollectionMedia", fieldMutation, roleSession|roleManager, 57),
	"reportdrift":                        newFieldHandler("reportDrift", "reportDrift", fieldMutation, roleSession, 135),
	"reportplayback":                     newFieldHandler("reportPlayback", "reportPlayback", fieldMutation, roleSession, 26),
	"requestaccess":                      newFieldHandler("requestAccess", "requestAccess", fieldMutation, roleSession, 97),
	"requestextension":                   newFieldHandler("requestExtension", "requestExtension", fieldMutation, roleSession, 93),
	"requestwatchpartytransport":         newFieldHandler("requestWatchPartyTransport", "requestWatchPartyTransport", fieldMutation, roleSession, 140),
	"resetnavoverride":                   newFieldHandler("resetNavOverride", "resetNavOverride", fieldMutation, roleSession|roleManager, 62),
	"resolveaccessrequest":               newFieldHandler("resolveAccessRequest", "resolveAccessRequest", fieldMutation, roleSession, 98),
	"respondextension":                   newFieldHandler("respondExtension", "respondExtension", fieldMutation, roleSession, 94),
	"restorecollection":                  newFieldHandler("restoreCollection", "restoreCollection", fieldMutation, roleSession|roleManager, 57),
	"restoremanagedpath":                 newFieldHandler("restoreManagedPath", "restoreManagedPath", fieldMutation, roleSession|roleManager, 110),
	"restoremarker":                      newFieldHandler("restoreMarker", "restoreMarker", fieldMutation, roleSession|roleManager, 14),
	"restoremedia":                       newFieldHandler("restoreMedia", "restoreMedia", fieldMutation, roleSession|roleManager, 35),
	"restorenavoverride":                 newFieldHandler("restoreNavOverride", "restoreNavOverride", fieldMutation, roleSession|roleManager, 62),
	"restoretag":                         newFieldHandler("restoreTag", "restoreTag", fieldMutation, roleSession|roleManager, 36),
	"retentionrules":                     newFieldHandler("retentionRules", "retentionRules", fieldRead, roleSession|roleAdmin, 58),
	"reveal":                             newFieldHandler("reveal", "reveal", fieldMutation, roleSession, 25),
	"revokeaccessgrant":                  newFieldHandler("revokeAccessGrant", "revokeAccessGrant", fieldMutation, roleSession, 102),
	"revokealldevices":                   newFieldHandler("revokeAllDevices", "revokeAllDevices", fieldMutation, roleSession|roleAdmin, 145),
	"revokeapikey":                       newFieldHandler("revokeApiKey", "revokeApiKey", fieldMutation, roleSession|roleAdmin, 73),
	"revokeapitoken":                     newFieldHandler("revokeApiToken", "revokeApiToken", fieldMutation, roleSession|roleAdmin, 79),
	"revokedevice":                       newFieldHandler("revokeDevice", "revokeDevice", fieldMutation, roleSession|roleAdmin, 145),
	"revokeofflinepack":                  newFieldHandler("revokeOfflinePack", "revokeOfflinePack", fieldMutation, roleSession, 119),
	"rewrapencryptedlibrary":             newFieldHandler("rewrapEncryptedLibrary", "rewrapEncryptedLibrary", fieldMutation, roleSession|roleAdmin, 107),
	"runeventscompaction":                newFieldHandler("runEventsCompaction", "runEventsCompaction", fieldMutation, roleSession|roleAdmin, 63),
	"runimport":                          newFieldHandler("runImport", "runImport", fieldMutation, roleSession|roleAdmin, 81),
	"runjob":                             newFieldHandler("runJob", "runJob", fieldMutation, roleSession|roleAdmin, 63),
	"runpluginlifecycleaction":           newFieldHandler("runPluginLifecycleAction", "runPluginLifecycleAction", fieldMutation, roleSession|roleAdmin, 66),
	"savedfilters":                       newFieldHandler("savedFilters", "savedFilters", fieldRead, roleSession, 55),
	"scanestimate":                       newFieldHandler("scanEstimate", "scanEstimate", fieldRead, roleSession|roleAdmin, 82),
	"scanlibrary":                        newFieldHandler("scanLibrary", "scanLibrary", fieldMutation, roleSession|roleAdmin, 52),
	"schedules":                          newFieldHandler("schedules", "schedules", fieldRead, roleSession|roleAdmin, 64),
	"scrobbleeligibility":                newFieldHandler("scrobbleEligibility", "scrobbleEligibility", fieldRead, roleSession, 86),
	"scrobblelinks":                      newFieldHandler("scrobbleLinks", "scrobbleLinks", fieldRead, roleSession, 85),
	"scrobblestatus":                     newFieldHandler("scrobbleStatus", "", fieldRead, roleSession|roleAdmin, 87),
	"sealencryptedlibrary":               newFieldHandler("sealEncryptedLibrary", "sealEncryptedLibrary", fieldMutation, roleSession|roleAdmin, 107),
	"search":                             newFieldHandler("search", "search", fieldRead, roleSession, 19),
	"searchentities":                     newFieldHandler("searchEntities", "searchEntities", fieldRead, roleSession, 22),
	"searchfeeds":                        newFieldHandler("searchFeeds", "searchFeeds", fieldRead, roleSession, 23),
	"searchresult":                       newFieldHandler("searchResult", "searchResult", fieldRead, roleSession, 20),
	"semanticcapability":                 newFieldHandler("semanticCapability", "semanticCapability", fieldRead, roleSession, 21),
	"serverinfo":                         newFieldHandler("serverInfo", "", fieldRead, rolePublic, 0),
	"setaccessschedule":                  newFieldHandler("setAccessSchedule", "setAccessSchedule", fieldMutation, roleSession|roleAdmin, 41),
	"setcapabilityenabled":               newFieldHandler("setCapabilityEnabled", "setCapabilityEnabled", fieldMutation, roleSession|roleAdmin, 67),
	"setcertificationceiling":            newFieldHandler("setCertificationCeiling", "setCertificationCeiling", fieldMutation, roleSession|roleAdmin, 40),
	"setclientpref":                      newFieldHandler("setClientPref", "setClientPref", fieldMutation, roleSession, 42),
	"setconsoleconfig":                   newFieldHandler("setConsoleConfig", "setConsoleConfig", fieldMutation, roleSession|roleAdmin, 66),
	"setdevicerole":                      newFieldHandler("setDeviceRole", "setDeviceRole", fieldMutation, roleSession|roleAdmin, 145),
	"setextensionpreauthorization":       newFieldHandler("setExtensionPreauthorization", "setExtensionPreauthorization", fieldMutation, roleSession|roleAdmin, 96),
	"setfavorite":                        newFieldHandler("setFavorite", "setFavorite", fieldMutation, roleSession, 34),
	"setfollow":                          newFieldHandler("setFollow", "setFollow", fieldMutation, roleSession, 53),
	"setlibraryaiindexing":               newFieldHandler("setLibraryAIIndexing", "setLibraryAIIndexing", fieldMutation, roleSession|roleAdmin, 77),
	"setlibrarybacking":                  newFieldHandler("setLibraryBacking", "setLibraryBacking", fieldMutation, roleSession|roleAdmin, 48),
	"setlibrarypreviewclips":             newFieldHandler("setLibraryPreviewClips", "setLibraryPreviewClips", fieldMutation, roleSession|roleAdmin, 47),
	"setlibraryrestricted":               newFieldHandler("setLibraryRestricted", "setLibraryRestricted", fieldMutation, roleSession|roleAdmin, 49),
	"setlibraryroots":                    newFieldHandler("setLibraryRoots", "setLibraryRoots", fieldMutation, roleSession|roleAdmin, 46),
	"setmediafieldlock":                  newFieldHandler("setMediaFieldLock", "setMediaFieldLock", fieldMutation, roleSession|roleManager, 108),
	"setmute":                            newFieldHandler("setMute", "setMute", fieldMutation, roleSession, 123),
	"setnavoverride":                     newFieldHandler("setNavOverride", "setNavOverride", fieldMutation, roleSession|roleManager, 62),
	"setpluginactive":                    newFieldHandler("setPluginActive", "setPluginActive", fieldMutation, roleSession|roleAdmin, 66),
	"setpluginconfig":                    newFieldHandler("setPluginConfig", "setPluginConfig", fieldMutation, roleSession|roleAdmin, 66),
	"setpluginupdatesource":              newFieldHandler("setPluginUpdateSource", "setPluginUpdateSource", fieldMutation, roleSession|roleAdmin, 66),
	"setprofilelibraries":                newFieldHandler("setProfileLibraries", "setProfileLibraries", fieldMutation, roleSession|roleAdmin, 39),
	"setqbittorrentingest":               newFieldHandler("setQBittorrentIngest", "setQBittorrentIngest", fieldMutation, roleSession|roleAdmin, 106),
	"setqualitypolicy":                   newFieldHandler("setQualityPolicy", "setQualityPolicy", fieldMutation, roleSession|roleAdmin, 50),
	"setretentioncandidatehold":          newFieldHandler("setRetentionCandidateHold", "setRetentionCandidateHold", fieldMutation, roleSession|roleManager, 59),
	"setrevealpin":                       newFieldHandler("setRevealPin", "reveal", fieldMutation, roleSession|roleManager, 25),
	"setscrapercredential":               newFieldHandler("setScraperCredential", "setScraperCredential", fieldMutation, roleSession|roleAdmin, 66),
	"setscrobbleeligibility":             newFieldHandler("setScrobbleEligibility", "setScrobbleEligibility", fieldMutation, roleSession|roleAdmin, 29),
	"setskipdetectionenabled":            newFieldHandler("setSkipDetectionEnabled", "setSkipDetectionEnabled", fieldMutation, roleSession|roleAdmin, 78),
	"setsubscriptionretention":           newFieldHandler("setSubscriptionRetention", "setSubscriptionRetention", fieldMutation, roleSession|roleManager, 54),
	"settranscodeconfig":                 newFieldHandler("setTranscodeConfig", "setTranscodeConfig", fieldMutation, roleSession|roleAdmin, 69),
	"setupstate":                         newFieldHandler("setupState", "setupState", fieldRead, roleSession, 80),
	"skipdetectionenabled":               newFieldHandler("skipDetectionEnabled", "skipDetectionEnabled", fieldRead, roleSession|roleAdmin, 78),
	"smartcollections":                   newFieldHandler("smartCollections", "smartCollections", fieldRead, roleSession, 57),
	"sourceitems":                        newFieldHandler("sourceItems", "sourceItems", fieldRead, roleSession, 17),
	"stationdelete":                      newFieldHandler("stationDelete", "stationDelete", fieldMutation, roleSession, 33),
	"stationgenerate":                    newFieldHandler("stationGenerate", "stationGenerate", fieldRead, roleSession, 56),
	"stations":                           newFieldHandler("stations", "stations", fieldRead, roleSession, 56),
	"stationsave":                        newFieldHandler("stationSave", "stationSave", fieldMutation, roleSession, 32),
	"stationstart":                       newFieldHandler("stationStart", "stationStart", fieldMutation, roleSession, 31),
	"subscriptions":                      newFieldHandler("subscriptions", "subscriptions", fieldRead, roleSession|roleManager, 54),
	"switchprofile":                      newFieldHandler("switchProfile", "switchProfile", fieldMutation, roleSession, 3),
	"syncofflinepackprogress":            newFieldHandler("syncOfflinePackProgress", "syncOfflinePackProgress", fieldMutation, roleSession, 120),
	"tagassignments":                     newFieldHandler("tagAssignments", "tagAssignments", fieldRead, roleSession|roleManager, 36),
	"tags":                               newFieldHandler("tags", "tags", fieldRead, roleSession|roleManager, 36),
	"terminatesession":                   newFieldHandler("terminateSession", "terminateSession", fieldMutation, roleSession|roleAdmin, 145),
	"transcodeconfig":                    newFieldHandler("transcodeConfig", "transcodeConfig", fieldRead, roleSession|roleAdmin, 69),
	"transferprofileowner":               newFieldHandler("transferProfileOwner", "transferProfileOwner", fieldMutation, roleSession|roleAdmin, 144),
	"transitiongroup":                    newFieldHandler("transitionGroup", "transitionGroup", fieldMutation, roleSession, 134),
	"trashedmarkers":                     newFieldHandler("trashedMarkers", "markers", fieldRead, roleSession|roleManager, 13),
	"trashlist":                          newFieldHandler("trashList", "trash", fieldRead, roleSession|roleManager, 35),
	"trashmedia":                         newFieldHandler("trashMedia", "trashMedia", fieldMutation, roleSession|roleManager, 35),
	"unassigntag":                        newFieldHandler("unassignTag", "unassignTag", fieldMutation, roleSession|roleManager, 36),
	"undobatch":                          newFieldHandler("undoBatch", "undoBatch", fieldMutation, roleSession|roleManager, 110),
	"uninstallplugindata":                newFieldHandler("uninstallPluginData", "uninstallPluginData", fieldMutation, roleSession|roleAdmin, 66),
	"unlinkscrobble":                     newFieldHandler("unlinkScrobble", "unlinkScrobble", fieldMutation, roleSession, 28),
	"unmatchedqueue":                     newFieldHandler("unmatchedQueue", "unmatchedQueue", fieldRead, roleSession|roleManager, 61),
	"unmatchedrecords":                   newFieldHandler("unmatchedRecords", "unmatchedRecords", fieldRead, roleSession|roleManager, 61),
	"unmatchedsuppressions":              newFieldHandler("unmatchedSuppressions", "unmatchedSuppressions", fieldRead, roleSession|roleManager, 61),
	"suppressunmatched":                  newFieldHandler("suppressUnmatched", "suppressUnmatched", fieldMutation, roleSession|roleManager, 61),
	"resetunmatchedsuppression":          newFieldHandler("resetUnmatchedSuppression", "resetUnmatchedSuppression", fieldMutation, roleSession|roleManager, 61),
	"unsealencryptedlibrary":             newFieldHandler("unsealEncryptedLibrary", "unsealEncryptedLibrary", fieldMutation, roleSession|roleAdmin, 107),
	"updatecollection":                   newFieldHandler("updateCollection", "updateCollection", fieldMutation, roleSession|roleManager, 57),
	"updatemarker":                       newFieldHandler("updateMarker", "updateMarker", fieldMutation, roleSession|roleManager, 14),
	"updatechannel":                      newFieldHandler("updateChannel", "updateChannel", fieldMutation, roleSession|roleManager, 57),
	"updateprofile":                      newFieldHandler("updateProfile", "updateProfile", fieldMutation, roleSession|roleAdmin, 38),
	"updateretentionrule":                newFieldHandler("updateRetentionRule", "updateRetentionRule", fieldMutation, roleSession|roleAdmin, 58),
	"updateschedule":                     newFieldHandler("updateSchedule", "updateSchedule", fieldMutation, roleSession|roleAdmin, 64),
	"updatesmartcollection":              newFieldHandler("updateSmartCollection", "updateSmartCollection", fieldMutation, roleSession|roleManager, 57),
	"updatestatus":                       newFieldHandler("updateStatus", "updateStatus", fieldRead, roleSession|roleAdmin, 68),
	"updatetag":                          newFieldHandler("updateTag", "updateTag", fieldMutation, roleSession|roleManager, 36),
	"upsertentityrelation":               newFieldHandler("upsertEntityRelation", "upsertEntityRelation", fieldMutation, roleSession|roleManager, 110),
	"validatescrapercredential":          newFieldHandler("validateScraperCredential", "validateScraperCredential", fieldMutation, roleSession|roleAdmin, 66),
	"verifyadminsession":                 newFieldHandler("verifyAdminSession", "verifyAdminSession", fieldMutation, roleSession, 70),
	"verifybackup":                       newFieldHandler("verifyBackup", "verifyBackup", fieldMutation, roleSession|roleAdmin, 65),
	"watcherstatus":                      newFieldHandler("watcherStatus", "watcherStatus", fieldRead, roleSession|roleAdmin, 105),
	"watchpartyautoskip":                 newFieldHandler("watchPartyAutoSkip", "watchPartyAutoSkip", fieldMutation, roleSession, 141),
	"watchpartytransport":                newFieldHandler("watchPartyTransport", "watchPartyTransport", fieldMutation, roleSession, 139),
}

func dispatchFieldBlock000(dispatch *fieldDispatchContext) response {
	fields := dispatch.fields
	if fields["serverinfo"] {
		setField(dispatch, "serverinfo", ServerInfo{
			Name:    "Beamfall",
			Version: serverVersionString,
		})
	}
	return response{}
}

func dispatchFieldBlock001(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	if fields["currentsession"] {
		expiresAt := ""
		if !scope.ExpiresAt.IsZero() {
			expiresAt = scope.ExpiresAt.UTC().Format(time.RFC3339)
		}
		session := SessionInfo{
			ID:         string(scope.SessionID),
			DeviceRole: string(scope.DeviceRole),
			ProfileID:  string(scope.ProfileID),
			Profile:    string(scope.Profile),
			Revealed:   scope.Revealed,
			ExpiresAt:  expiresAt,
		}
		if h.store != nil {
			if profile, err := h.store.Profile(ctx, scope.ProfileID); err == nil {
				session.AudioLanguages = profile.AudioLanguages
				session.SubtitleLanguages = profile.SubtitleLanguages
				session.SubtitleMode = string(profile.SubtitleMode)
				session.PreferSDH = profile.PreferSDH
			}
		}
		setField(dispatch, "currentsession", session)
	}
	return response{}
}

func dispatchFieldBlock002(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	fields := dispatch.fields
	if fields["profiles"] {
		profiles, err := h.profiles(ctx)
		if resp := resolveField(dispatch, "profiles", profiles, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock003(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["switchprofile"] {
		session, err := h.switchProfile(ctx, scope, req)
		if resp := resolveField(dispatch, "switchprofile", session, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock004(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	if fields["navigationmanifest"] || fields["manifest"] {
		document, err := h.navigationManifest(ctx, scope)
		if resp := resolveField(dispatch, requestedField(fields, "navigationmanifest", "manifest"), document, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock005(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	fields := dispatch.fields
	if fields["plugins"] {
		setField(dispatch, "plugins", h.pluginInfos())
	}
	return response{}
}

func dispatchFieldBlock006(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["entitiesformedia"] {
		related, err := h.entitiesForMedia(ctx, scope, req)
		if resp := resolveField(dispatch, "entitiesformedia", related, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock007(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["entityrelations"] {
		related, err := h.entityRelations(ctx, scope, req)
		if resp := resolveField(dispatch, "entityrelations", related, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock008(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["mediaforentity"] {
		items, err := h.mediaForEntity(ctx, scope, req)
		if resp := resolveField(dispatch, "mediaforentity", items, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock009(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["mediafiles"] {
		files, err := h.mediaFiles(ctx, scope, req)
		if resp := resolveField(dispatch, "mediafiles", files, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock010(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["mediarelations"] {
		related, err := h.mediaRelations(ctx, scope, req)
		if resp := resolveField(dispatch, "mediarelations", related, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock011(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["entity"] {
		entity, err := h.entityQuery(ctx, scope, req)
		if resp := resolveField(dispatch, "entity", entity, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock012(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["markers"] {
		markers, err := h.markersQuery(ctx, scope, req)
		if resp := resolveField(dispatch, "markers", markers, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock013(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["trashedmarkers"] {
		markers, err := h.trashedMarkersQuery(ctx, scope, req)
		if resp := resolveField(dispatch, "trashedmarkers", markers, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock014(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["createmarker"] || fields["updatemarker"] || fields["deletemarker"] || fields["restoremarker"] {
		if fields["createmarker"] {
			marker, err := h.createMarker(ctx, scope, req)
			if resp := resolveField(dispatch, "createmarker", marker, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["updatemarker"] {
			marker, err := h.updateMarker(ctx, scope, req)
			if resp := resolveField(dispatch, "updatemarker", marker, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["deletemarker"] {
			marker, err := h.deleteMarker(ctx, scope, req)
			if resp := resolveField(dispatch, "deletemarker", marker, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["restoremarker"] {
			marker, err := h.restoreMarker(ctx, scope, req)
			if resp := resolveField(dispatch, "restoremarker", marker, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock015(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["mediafields"] {
		values, err := h.mediaFieldsQuery(ctx, scope, req)
		if resp := resolveField(dispatch, "mediafields", values, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock016(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["artworkcandidates"] || fields["pinartworkcandidate"] {
		if fields["artworkcandidates"] {
			candidates, err := h.artworkCandidates(ctx, scope, req)
			if resp := resolveField(dispatch, "artworkcandidates", candidates, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["pinartworkcandidate"] {
			candidate, err := h.pinArtworkCandidate(ctx, scope, req)
			if resp := resolveField(dispatch, "pinartworkcandidate", candidate, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock017(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["sourceitems"] {
		items, err := h.sourceItems(ctx, scope, req)
		if resp := resolveField(dispatch, "sourceitems", items, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock018(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["ambientqueue"] {
		items, err := h.ambientQueue(ctx, scope, req)
		if resp := resolveField(dispatch, "ambientqueue", items, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock019(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["search"] {
		items, err := h.search(ctx, scope, req)
		if resp := resolveField(dispatch, "search", items, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock020(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Explainable, mode-aware search (ADR-0041 #62/#106) + the per-Library
	// semantic capability (the viewer's 4-state toggle). Not operator-gated:
	// every session searches its own reveal-filtered library and needs to know
	// whether semantic mode is available to it.
	if fields["searchresult"] {
		result, err := h.searchResult(ctx, scope, req)
		if resp := resolveField(dispatch, "searchresult", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock021(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	if fields["semanticcapability"] {
		caps, err := h.semanticCapability(ctx, scope)
		if resp := resolveField(dispatch, "semanticcapability", caps, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock022(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["searchentities"] {
		items, err := h.searchEntities(ctx, scope, req)
		if resp := resolveField(dispatch, "searchentities", items, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock023(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["searchfeeds"] {
		items, err := h.searchFeeds(ctx, scope, req)
		if resp := resolveField(dispatch, "searchfeeds", items, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock024(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["media"] {
		media, err := h.media(ctx, scope, req)
		if resp := resolveField(dispatch, "media", media, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock025(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["reveal"] || fields["relock"] || fields["setrevealpin"] {
		result, err := h.revealOps(ctx, scope, fields, req)
		if err != nil {
			return resolveField(dispatch, requestedField(fields, "setrevealpin", "reveal", "relock"), any(nil), err)
		}
		for field, value := range result {
			if resp := resolveField(dispatch, field, value, nil); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock026(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["reportplayback"] {
		result, err := h.reportPlayback(ctx, scope, req)
		if resp := resolveField(dispatch, "reportplayback", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock027(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Scrobble link: per-Profile self-service — a Profile links
	// or unlinks its OWN third-party scrobble account (the session's Profile,
	// never an other-profile parameter). The credential goes to the secure store;
	// no token is ever echoed back (invariant #5).
	if fields["linkscrobble"] {
		result, err := h.linkScrobble(ctx, scope, req)
		if resp := resolveField(dispatch, "linkscrobble", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock028(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["unlinkscrobble"] {
		result, err := h.unlinkScrobble(ctx, scope, req)
		if resp := resolveField(dispatch, "unlinkscrobble", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock029(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Scrobble eligibility: per-Library opt-in is an OPERATOR
	// decision (admin-gated). Adult/Restricted Libraries are never eligible by
	// default, so enabling one is a deliberate operator act.
	if fields["setscrobbleeligibility"] {
		result, err := h.setScrobbleEligibility(ctx, scope, req)
		if resp := resolveField(dispatch, "setscrobbleeligibility", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock030(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["queueadd"] || fields["queueremove"] || fields["queuemove"] || fields["queueclear"] {
		result, err := h.queueOps(ctx, scope, fields, req)
		if err != nil {
			return resolveField(dispatch, requestedField(fields, "queueadd", "queueremove", "queuemove", "queueclear"), any(nil), err)
		}
		for field, value := range result {
			if resp := resolveField(dispatch, field, value, nil); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock031(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Stations (MUSIC-5, ADR-0098 area 5): start materializes a window into the
	// caller's own Play Queue; save/delete manage the caller's own saved Stations.
	if fields["stationstart"] {
		items, err := h.stationStart(ctx, scope, req)
		if resp := resolveField(dispatch, "stationstart", items, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock032(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["stationsave"] {
		st, err := h.stationSave(ctx, scope, req)
		if resp := resolveField(dispatch, "stationsave", st, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock033(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["stationdelete"] {
		st, err := h.stationDelete(ctx, scope, req)
		if resp := resolveField(dispatch, "stationdelete", st, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock034(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["setfavorite"] {
		media, err := h.setFavorite(ctx, scope, req)
		if resp := resolveField(dispatch, "setfavorite", media, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock035(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["trashmedia"] || fields["restoremedia"] || fields["trashlist"] || fields["purgetrash"] {
		if fields["trashmedia"] {
			entries, err := h.trashMedia(ctx, scope, req)
			if resp := resolveField(dispatch, "trashmedia", entries, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["restoremedia"] {
			entries, err := h.restoreMedia(ctx, scope, req)
			if resp := resolveField(dispatch, "restoremedia", entries, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["trashlist"] {
			entries, err := h.trashList(ctx, scope)
			if resp := resolveField(dispatch, "trashlist", entries, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["purgetrash"] {
			result, err := h.purgeTrash(ctx, scope, req)
			if resp := resolveField(dispatch, "purgetrash", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock036(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["tags"] || fields["tagassignments"] || fields["createtag"] || fields["updatetag"] ||
		fields["deletetag"] || fields["restoretag"] || fields["assigntag"] ||
		fields["unassigntag"] || fields["bulkassigntags"] {
		if fields["tags"] {
			tags, err := h.tags(ctx, req)
			if resp := resolveField(dispatch, "tags", tags, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["tagassignments"] {
			assignment, err := h.tagAssignments(ctx, scope, req)
			if resp := resolveField(dispatch, "tagassignments", assignment, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["createtag"] {
			tag, err := h.createTag(ctx, scope, req)
			if resp := resolveField(dispatch, "createtag", tag, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["updatetag"] {
			tag, err := h.updateTag(ctx, scope, req)
			if resp := resolveField(dispatch, "updatetag", tag, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["deletetag"] {
			tag, err := h.deleteTag(ctx, scope, req)
			if resp := resolveField(dispatch, "deletetag", tag, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["restoretag"] {
			tag, err := h.restoreTag(ctx, scope, req)
			if resp := resolveField(dispatch, "restoretag", tag, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["assigntag"] {
			assignment, err := h.assignTag(ctx, scope, req)
			if resp := resolveField(dispatch, "assigntag", assignment, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["unassigntag"] {
			assignment, err := h.unassignTag(ctx, scope, req)
			if resp := resolveField(dispatch, "unassigntag", assignment, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["bulkassigntags"] {
			result, err := h.bulkAssignTags(ctx, scope, req)
			if resp := resolveField(dispatch, "bulkassigntags", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock037(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["createprofile"] {
		profile, err := h.createProfile(ctx, scope, req)
		if resp := resolveField(dispatch, "createprofile", profile, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock038(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	req := dispatch.req
	fields := dispatch.fields
	if fields["updateprofile"] {
		profile, err := h.updateProfile(ctx, req)
		if resp := resolveField(dispatch, "updateprofile", profile, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock039(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["setprofilelibraries"] {
		profile, err := h.setProfileLibraries(ctx, scope, req)
		if resp := resolveField(dispatch, "setprofilelibraries", profile, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock040(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	req := dispatch.req
	fields := dispatch.fields
	if fields["setcertificationceiling"] {
		profile, err := h.setCertificationCeiling(ctx, req)
		if resp := resolveField(dispatch, "setcertificationceiling", profile, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock041(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Access schedules (ADR-0004 §4 / ADR-0007 §4 Profiles & access policies):
	// the Admin-Session-gated Console resolver to read and set a managed
	// Profile's allowed time windows. The enforcement half lives at the auth
	// scope-resolution choke point.
	if fields["accessschedule"] || fields["setaccessschedule"] {
		if fields["accessschedule"] {
			schedule, err := h.accessSchedule(ctx, req)
			if resp := resolveField(dispatch, "accessschedule", schedule, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["setaccessschedule"] {
			schedule, err := h.setAccessSchedule(ctx, scope, req)
			if resp := resolveField(dispatch, "setaccessschedule", schedule, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock042(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["setclientpref"] {
		pref, err := h.setClientPref(ctx, scope, req)
		if resp := resolveField(dispatch, "setclientpref", pref, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock043(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	if fields["clientprefs"] {
		prefs, err := h.clientPrefs(ctx, scope)
		if resp := resolveField(dispatch, "clientprefs", prefs, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock044(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["createlibrary"] {
		library, err := h.createLibrary(ctx, scope, req)
		if resp := resolveField(dispatch, "createlibrary", library, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock045(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	if fields["libraries"] {
		libraries, err := h.libraries(ctx, scope)
		if resp := resolveField(dispatch, "libraries", libraries, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock046(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["setlibraryroots"] {
		library, err := h.setLibraryRoots(ctx, scope, req)
		if resp := resolveField(dispatch, "setlibraryroots", library, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock047(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["setlibrarypreviewclips"] {
		library, err := h.setLibraryPreviewClips(ctx, scope, req)
		if resp := resolveField(dispatch, "setlibrarypreviewclips", library, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock048(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["setlibrarybacking"] {
		library, err := h.setLibraryBacking(ctx, scope, req)
		if resp := resolveField(dispatch, "setlibrarybacking", library, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock049(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["setlibraryrestricted"] {
		library, err := h.setLibraryRestricted(ctx, scope, req)
		if resp := resolveField(dispatch, "setlibraryrestricted", library, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock050(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["qualitypolicy"] || fields["setqualitypolicy"] {
		if fields["qualitypolicy"] {
			policy, err := h.qualityPolicy(ctx, scope, req)
			if resp := resolveField(dispatch, "qualitypolicy", policy, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["setqualitypolicy"] {
			policy, err := h.setQualityPolicy(ctx, scope, req)
			if resp := resolveField(dispatch, "setqualitypolicy", policy, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock051(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["deletelibrary"] {
		err := h.deleteLibrary(ctx, scope, req)
		if resp := resolveField(dispatch, "deletelibrary", true, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock052(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["scanlibrary"] {
		job, err := h.scanLibrary(ctx, scope, req)
		if resp := resolveField(dispatch, "scanlibrary", job, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock053(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["setfollow"] {
		subscription, err := h.setFollow(ctx, scope, req)
		if resp := resolveField(dispatch, "setfollow", subscription, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock054(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["addsubscription"] || fields["removesubscription"] || fields["subscriptions"] || fields["setsubscriptionretention"] || fields["associatesubscriptionentry"] {
		if fields["addsubscription"] {
			subscription, err := h.addSubscription(ctx, scope, req)
			if resp := resolveField(dispatch, "addsubscription", subscription, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["removesubscription"] {
			subscription, err := h.removeSubscription(ctx, scope, req)
			if resp := resolveField(dispatch, "removesubscription", subscription, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["subscriptions"] {
			subscriptions, err := h.subscriptions(ctx, scope, req)
			if resp := resolveField(dispatch, "subscriptions", subscriptions, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["setsubscriptionretention"] {
			subscription, err := h.setSubscriptionRetention(ctx, scope, req)
			if resp := resolveField(dispatch, "setsubscriptionretention", subscription, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["associatesubscriptionentry"] {
			association, err := h.associateSubscriptionEntry(ctx, scope, req)
			if resp := resolveField(dispatch, "associatesubscriptionentry", association, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock055(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Saved Filters are per-Profile personal data — any authenticated session
	// may create or delete its own filters.
	if fields["savedfilters"] || fields["createsavedfilter"] || fields["deletesavedfilter"] {
		if fields["savedfilters"] {
			filters, err := h.savedFilters(ctx, scope, req)
			if resp := resolveField(dispatch, "savedfilters", filters, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["createsavedfilter"] {
			filter, err := h.createSavedFilter(ctx, scope, req)
			if resp := resolveField(dispatch, "createsavedfilter", filter, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["deletesavedfilter"] {
			filter, err := h.deleteSavedFilter(ctx, scope, req)
			if resp := resolveField(dispatch, "deletesavedfilter", filter, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock056(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Stations (MUSIC-5, ADR-0098 area 5) are per-Profile personal data: any
	// authenticated session reads its own saved Stations and previews a
	// generation (read-only) for itself.
	if fields["stations"] || fields["stationgenerate"] {
		if fields["stations"] {
			stations, err := h.stations(ctx, scope, req)
			if resp := resolveField(dispatch, "stations", stations, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["stationgenerate"] {
			result, err := h.stationGenerate(ctx, scope, req)
			if resp := resolveField(dispatch, "stationgenerate", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock057(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Collections and Smart Collections are Library-level curation: reads are
	// open to any session; edits require the manager role.
	if fields["collections"] || fields["smartcollections"] || fields["channels"] || fields["createcollection"] || fields["updatecollection"] || fields["deletecollection"] || fields["restorecollection"] || fields["addcollectionmedia"] || fields["removecollectionmedia"] || fields["reordercollectionmedia"] || fields["createsmartcollection"] || fields["updatesmartcollection"] || fields["deletesmartcollection"] || fields["createchannel"] || fields["updatechannel"] || fields["deletechannel"] {
		if fields["collections"] {
			collections, err := h.collections(ctx, scope, req)
			if resp := resolveField(dispatch, "collections", collections, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["smartcollections"] {
			collections, err := h.smartCollections(ctx, scope, req)
			if resp := resolveField(dispatch, "smartcollections", collections, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["createcollection"] {
			collection, err := h.createCollection(ctx, scope, req)
			if resp := resolveField(dispatch, "createcollection", collection, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["updatecollection"] {
			collection, err := h.updateCollection(ctx, scope, req)
			if resp := resolveField(dispatch, "updatecollection", collection, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["deletecollection"] {
			collection, err := h.deleteCollection(ctx, scope, req)
			if resp := resolveField(dispatch, "deletecollection", collection, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["restorecollection"] {
			collection, err := h.restoreCollection(ctx, scope, req)
			if resp := resolveField(dispatch, "restorecollection", collection, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["addcollectionmedia"] {
			collection, err := h.addCollectionMedia(ctx, scope, req)
			if resp := resolveField(dispatch, "addcollectionmedia", collection, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["removecollectionmedia"] {
			collection, err := h.removeCollectionMedia(ctx, scope, req)
			if resp := resolveField(dispatch, "removecollectionmedia", collection, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["reordercollectionmedia"] {
			collection, err := h.reorderCollectionMedia(ctx, scope, req)
			if resp := resolveField(dispatch, "reordercollectionmedia", collection, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["createsmartcollection"] {
			collection, err := h.createSmartCollection(ctx, scope, req)
			if resp := resolveField(dispatch, "createsmartcollection", collection, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["updatesmartcollection"] {
			collection, err := h.updateSmartCollection(ctx, scope, req)
			if resp := resolveField(dispatch, "updatesmartcollection", collection, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["deletesmartcollection"] {
			collection, err := h.deleteSmartCollection(ctx, scope, req)
			if resp := resolveField(dispatch, "deletesmartcollection", collection, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["channels"] {
			channels, err := h.channels(ctx, scope, req)
			if resp := resolveField(dispatch, "channels", channels, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["createchannel"] {
			channel, err := h.createChannel(ctx, scope, req)
			if resp := resolveField(dispatch, "createchannel", channel, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["updatechannel"] {
			channel, err := h.updateChannel(ctx, scope, req)
			if resp := resolveField(dispatch, "updatechannel", channel, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["deletechannel"] {
			channel, err := h.deleteChannel(ctx, scope, req)
			if resp := resolveField(dispatch, "deletechannel", channel, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock058(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Retention rules: Console-authored leaving-soon rules.
	// retentionRules / createRetentionRule / updateRetentionRule /
	// deleteRetentionRule are Admin-Session-gated Console operations.
	// setRetentionCandidateHold is the manager-tier operator hold; viewer
	// rescue is the per-Profile Favorite-class keep bit via setFavorite.
	if fields["retentionrules"] || fields["createretentionrule"] ||
		fields["updateretentionrule"] || fields["deleteretentionrule"] {
		if fields["retentionrules"] {
			rules, err := h.retentionRules(ctx, scope, req)
			if resp := resolveField(dispatch, "retentionrules", rules, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["createretentionrule"] {
			rule, err := h.createRetentionRule(ctx, scope, req)
			if resp := resolveField(dispatch, "createretentionrule", rule, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["updateretentionrule"] {
			rule, err := h.updateRetentionRule(ctx, scope, req)
			if resp := resolveField(dispatch, "updateretentionrule", rule, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["deleteretentionrule"] {
			rule, err := h.deleteRetentionRule(ctx, scope, req)
			if resp := resolveField(dispatch, "deleteretentionrule", rule, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock059(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["setretentioncandidatehold"] {
		result, err := h.setRetentionCandidateHold(ctx, scope, req)
		if resp := resolveField(dispatch, "setretentioncandidatehold", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock060(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["identifysearch"] || fields["identifyapply"] || fields["fixmatch"] ||
		fields["identifylookup"] || fields["fixmatchcandidates"] || fields["fixmatchpreview"] {
		if h.identify == nil {
			return response{Errors: []graphError{{Message: "identification unavailable"}}}
		}
		if fields["identifysearch"] {
			candidates, err := h.identifySearch(ctx, scope, req)
			if resp := resolveField(dispatch, "identifysearch", candidates, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["identifyapply"] {
			media, err := h.identifyApply(ctx, scope, req)
			if resp := resolveField(dispatch, "identifyapply", media, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		// METAPARITY-4 (ADR-0068 §A) Fix-Match contract: scored candidates with
		// source attribution, the paste-an-id direct fetch, and the
		// preview-before-apply diff. All three are reads that write nothing.
		if fields["fixmatchcandidates"] {
			candidates, err := h.fixMatchCandidates(ctx, scope, req)
			if resp := resolveField(dispatch, "fixmatchcandidates", candidates, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["identifylookup"] {
			candidate, err := h.identifyLookup(ctx, scope, req)
			if resp := resolveField(dispatch, "identifylookup", candidate, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["fixmatchpreview"] {
			diff, err := h.fixMatchPreview(ctx, scope, req)
			if resp := resolveField(dispatch, "fixmatchpreview", diff, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["fixmatch"] {
			media, err := h.fixMatch(ctx, scope, req)
			if resp := resolveField(dispatch, "fixmatch", media, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock061(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// PIPE-1 / WEB-5G (ADR-0003 §4, resolvers in graph_identify.go): read the
	// Unmatched queue, dismiss items, pin a corrected match, and attach external IDs.
	// Store-only paths — no identify service required; manager-gated.
	if fields["unmatchedqueue"] || fields["dismissunmatched"] || fields["addmediaexternalid"] ||
		fields["unmatchedrecords"] || fields["createmediafromunmatched"] ||
		fields["unmatchedsuppressions"] || fields["suppressunmatched"] || fields["resetunmatchedsuppression"] {
		if fields["unmatchedqueue"] {
			items, err := h.unmatchedQueue(ctx, scope, req)
			if resp := resolveField(dispatch, "unmatchedqueue", items, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["dismissunmatched"] {
			removed, err := h.dismissUnmatched(ctx, scope, req)
			if resp := resolveField(dispatch, "dismissunmatched", removed, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["addmediaexternalid"] {
			media, err := h.addMediaExternalID(ctx, scope, req)
			if resp := resolveField(dispatch, "addmediaexternalid", media, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		// METAPARITY-6/7 (ADR-0068 §A, resolvers in graph_unmatched_record.go):
		// the Media-INDEPENDENT record inbox, hand-authored no-match Media, and
		// the three independently-resettable suppression scopes.
		if fields["unmatchedrecords"] {
			records, err := h.unmatchedRecords(ctx, scope, req)
			if resp := resolveField(dispatch, "unmatchedrecords", records, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["unmatchedsuppressions"] {
			suppressions, err := h.unmatchedSuppressions(ctx, scope, req)
			if resp := resolveField(dispatch, "unmatchedsuppressions", suppressions, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["createmediafromunmatched"] {
			media, err := h.createMediaFromUnmatched(ctx, scope, req)
			if resp := resolveField(dispatch, "createmediafromunmatched", media, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["suppressunmatched"] {
			ok, err := h.suppressUnmatched(ctx, scope, req)
			if resp := resolveField(dispatch, "suppressunmatched", ok, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["resetunmatchedsuppression"] {
			reset, err := h.resetUnmatchedSuppression(ctx, scope, req)
			if resp := resolveField(dispatch, "resetunmatchedsuppression", reset, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock062(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// WEB-5J (ADR-0061 §Layout editor): read and edit Navigation Manifest + Layout
	// overrides at the server level. Manager-role-gated (operator-facing, like the
	// unmatched-queue curator flow — not an Admin Session operation).
	if fields["navoverride"] || fields["navoverridehistory"] || fields["setnavoverride"] || fields["resetnavoverride"] || fields["restorenavoverride"] {
		if fields["navoverride"] {
			result, err := h.navOverride(ctx, scope, req)
			if resp := resolveField(dispatch, "navoverride", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["navoverridehistory"] {
			result, err := h.navOverrideHistory(ctx, scope, req)
			if resp := resolveField(dispatch, "navoverridehistory", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["setnavoverride"] {
			result, err := h.setNavOverride(ctx, scope, req)
			if resp := resolveField(dispatch, "setnavoverride", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["resetnavoverride"] {
			result, err := h.resetNavOverride(ctx, scope, req)
			if resp := resolveField(dispatch, "resetnavoverride", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["restorenavoverride"] {
			result, err := h.restoreNavOverride(ctx, scope, req)
			if resp := resolveField(dispatch, "restorenavoverride", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock063(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["jobs"] || fields["job"] || fields["jobevents"] || fields["operationsgovernor"] || fields["jobcancel"] || fields["jobretry"] || fields["runjob"] || fields["runeventscompaction"] {
		if fields["jobs"] {
			jobs, err := h.jobs(ctx, scope)
			if resp := resolveField(dispatch, "jobs", jobs, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["job"] {
			job, err := h.job(ctx, scope, req)
			if resp := resolveField(dispatch, "job", job, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["jobevents"] {
			events, err := h.jobEvents(ctx, scope, req)
			if resp := resolveField(dispatch, "jobevents", events, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["operationsgovernor"] {
			status, err := h.operationsGovernor(ctx)
			if resp := resolveField(dispatch, "operationsgovernor", status, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["jobcancel"] {
			job, err := h.jobCancel(ctx, scope, req)
			if resp := resolveField(dispatch, "jobcancel", job, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["jobretry"] {
			job, err := h.jobRetry(ctx, scope, req)
			if resp := resolveField(dispatch, "jobretry", job, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["runjob"] {
			job, err := h.runJob(ctx, scope)
			if resp := resolveField(dispatch, "runjob", job, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["runeventscompaction"] {
			job, err := h.runEventsCompaction(ctx, scope)
			if resp := resolveField(dispatch, "runeventscompaction", job, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock064(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["schedules"] || fields["updateschedule"] {
		if fields["schedules"] {
			schedules, err := h.schedules(ctx)
			if resp := resolveField(dispatch, "schedules", schedules, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["updateschedule"] {
			schedule, err := h.updateSchedule(ctx, scope, req)
			if resp := resolveField(dispatch, "updateschedule", schedule, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock065(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	req := dispatch.req
	fields := dispatch.fields
	if fields["backup"] || fields["backuphistory"] || fields["verifybackup"] {
		if fields["backup"] {
			result, err := h.backup(ctx, req)
			if resp := resolveField(dispatch, "backup", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["backuphistory"] {
			history, err := h.backupHistory(ctx, req)
			if resp := resolveField(dispatch, "backuphistory", history, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["verifybackup"] {
			result, err := h.verifyBackup(ctx, req)
			if resp := resolveField(dispatch, "verifybackup", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock066(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Console server config (console-config, ADR-0007 §4 / ADR-0013 §5): read
	// runtime config + plugin activation, mutate config values, activate or
	// deactivate plugins. Admin-Session-gated (the Console mutation class).
	if fields["consoleconfig"] || fields["setconsoleconfig"] || fields["setpluginactive"] || fields["uninstallplugindata"] || fields["setpluginconfig"] || fields["pluginworker"] || fields["approvepluginpermission"] || fields["setpluginupdatesource"] || fields["runpluginlifecycleaction"] || fields["setscrapercredential"] || fields["validatescrapercredential"] || fields["disablescrapercredential"] {
		if fields["consoleconfig"] {
			result, err := h.consoleConfig(ctx, scope, req)
			if resp := resolveField(dispatch, "consoleconfig", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["setconsoleconfig"] {
			result, err := h.setConsoleConfig(ctx, scope, req)
			if resp := resolveField(dispatch, "setconsoleconfig", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["setpluginactive"] {
			result, err := h.setPluginActive(ctx, scope, req)
			if resp := resolveField(dispatch, "setpluginactive", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["uninstallplugindata"] {
			result, err := h.uninstallPluginData(ctx, scope, req)
			if resp := resolveField(dispatch, "uninstallplugindata", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["setpluginconfig"] {
			result, err := h.setPluginConfig(ctx, scope, req)
			if resp := resolveField(dispatch, "setpluginconfig", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["setscrapercredential"] {
			result, err := h.setScraperCredential(ctx, scope, req)
			if resp := resolveField(dispatch, "setscrapercredential", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["pluginworker"] {
			result, err := h.pluginWorkerField(ctx, req)
			if resp := resolveField(dispatch, "pluginworker", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["approvepluginpermission"] {
			result, err := h.approvePluginPermission(ctx, scope, req)
			if resp := resolveField(dispatch, "approvepluginpermission", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["setpluginupdatesource"] {
			result, err := h.setPluginUpdateSource(ctx, scope, req)
			if resp := resolveField(dispatch, "setpluginupdatesource", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["runpluginlifecycleaction"] {
			result, err := h.runPluginLifecycleAction(ctx, scope, req)
			if resp := resolveField(dispatch, "runpluginlifecycleaction", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["validatescrapercredential"] {
			result, err := h.validateScraperCredential(ctx, req)
			if resp := resolveField(dispatch, "validatescrapercredential", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["disablescrapercredential"] {
			result, err := h.disableScraperCredential(ctx, scope, req)
			if resp := resolveField(dispatch, "disablescrapercredential", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock067(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	fields := dispatch.fields
	// Capability runtime-gating control surface: the Admin-Session-
	// gated read of capability runtime state + the audited, attributed Workflow-
	// capability toggle. The actual resolvers live in graph_capability.go and are
	// compiled OUT on an App Store binary (graph_capability_appstore.go), where
	// the fields are also never registered — so they are rejected as unknown top-
	// level fields before reaching execute() (not advertised, not reachable —
	// invariant 8). The literal fields[...] indexes below keep the dispatch
	// visible to the action-registry conformance gate.
	if fields["capabilitystates"] || fields["setcapabilityenabled"] {
		if resp, handled := h.dispatchCapabilityControl(dispatch); handled {
			if len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock068(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	// Opt-in update awareness (update-status, ADR-0013 §7): an Admin-Session-
	// gated read of the version-check result. Default-off and no telemetry
	// (invariant #5) — the resolver makes no outbound request until the operator
	// opts in via consoleConfig.updateCheckEnabled.
	if fields["updatestatus"] {
		status, err := h.updateStatus(ctx, scope)
		if resp := resolveField(dispatch, "updatestatus", status, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock069(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Hardware transcoding config (transcode-config, ADR-0007 §§4/5 / ADR-0013
	// §4): read and update the HW-transcode policy. Admin-Session-gated.
	if fields["transcodeconfig"] || fields["settranscodeconfig"] {
		if fields["transcodeconfig"] {
			result, err := h.transcodeConfig(ctx, scope, req)
			if resp := resolveField(dispatch, "transcodeconfig", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["settranscodeconfig"] {
			result, err := h.setTranscodeConfig(ctx, scope, req)
			if resp := resolveField(dispatch, "settranscodeconfig", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock070(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Admin Session re-auth: verify the server admin password to
	// open the short-lived window; ending it locks immediately. Reading the
	// window's state is the Console's "am I still admin?" check. These are NOT
	// admin-gated themselves — verifyAdminSession is how a session BECOMES
	// admin; it is gated only by a resolved session scope.
	if fields["verifyadminsession"] {
		result, err := h.verifyAdminSession(ctx, scope, req)
		if resp := resolveField(dispatch, "verifyadminsession", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock071(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	if fields["endadminsession"] {
		result, err := h.endAdminSession(ctx, scope)
		if resp := resolveField(dispatch, "endadminsession", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock072(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	scope := dispatch.scope
	fields := dispatch.fields
	if fields["adminsession"] {
		setField(dispatch, "adminsession", h.adminSession(scope))
	}
	return response{}
}

func dispatchFieldBlock073(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Scoped API keys: a Console operation, so the Admin
	// Session tier gates create/revoke, orthogonal to Device Role. Listing is
	// also Console-gated — key metadata (names, scopes, Profile bindings) is
	// operator-only.
	if fields["apikeys"] || fields["createapikey"] || fields["revokeapikey"] {
		if fields["apikeys"] {
			keys, err := h.apiKeys(ctx)
			if resp := resolveField(dispatch, "apikeys", keys, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["createapikey"] {
			created, err := h.createAPIKey(ctx, scope, req)
			if resp := resolveField(dispatch, "createapikey", created, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["revokeapikey"] {
			revoked, err := h.revokeAPIKey(ctx, scope, req)
			if resp := resolveField(dispatch, "revokeapikey", revoked, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock074(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// ── Library Health Dashboard (ADR-0042, resolvers in graph_health.go) ──
	// Operator-only Console tool: every field is Admin-Session gated, like
	// scoped API keys above. Self-contained group; resolvers live in
	// graph_health.go.
	if fields["libraryhealth"] || fields["libraryhealthitems"] || fields["libraryduplicategroups"] ||
		fields["bulkrescanunmatched"] || fields["bulktrashbroken"] || fields["bulkmergeduplicates"] ||
		fields["bulkrescrapescrapefailures"] || fields["bulkfetchmissingartwork"] ||
		fields["bulkrescanunmatchedselected"] || fields["bulktrashbrokenselected"] ||
		fields["bulkrescrapescrapefailuresselected"] || fields["bulkfetchmissingartworkselected"] ||
		fields["bulkmergeduplicatesselected"] {
		if fields["libraryhealth"] {
			health, err := h.libraryHealth(ctx, scope)
			if resp := resolveField(dispatch, "libraryhealth", health, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["libraryhealthitems"] {
			items, err := h.libraryHealthItems(ctx, scope, req)
			if resp := resolveField(dispatch, "libraryhealthitems", items, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["libraryduplicategroups"] {
			groups, err := h.libraryDuplicateGroups(ctx, scope, req)
			if resp := resolveField(dispatch, "libraryduplicategroups", groups, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["bulkrescanunmatched"] {
			result, err := h.bulkRescanUnmatched(ctx, scope, req)
			if resp := resolveField(dispatch, "bulkrescanunmatched", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["bulktrashbroken"] {
			result, err := h.bulkTrashBroken(ctx, scope, req)
			if resp := resolveField(dispatch, "bulktrashbroken", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["bulkmergeduplicates"] {
			result, err := h.bulkMergeDuplicates(ctx, scope, req)
			if resp := resolveField(dispatch, "bulkmergeduplicates", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["bulkrescrapescrapefailures"] {
			result, err := h.bulkRescrapeScrapeFailures(ctx, scope, req)
			if resp := resolveField(dispatch, "bulkrescrapescrapefailures", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["bulkfetchmissingartwork"] {
			result, err := h.bulkFetchMissingArtwork(ctx, scope, req)
			if resp := resolveField(dispatch, "bulkfetchmissingartwork", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["bulkrescanunmatchedselected"] {
			result, err := h.bulkRescanUnmatchedSelected(ctx, scope, req)
			if resp := resolveField(dispatch, "bulkrescanunmatchedselected", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["bulktrashbrokenselected"] {
			result, err := h.bulkTrashBrokenSelected(ctx, scope, req)
			if resp := resolveField(dispatch, "bulktrashbrokenselected", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["bulkrescrapescrapefailuresselected"] {
			result, err := h.bulkRescrapeScrapeFailuresSelected(ctx, scope, req)
			if resp := resolveField(dispatch, "bulkrescrapescrapefailuresselected", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["bulkfetchmissingartworkselected"] {
			result, err := h.bulkFetchMissingArtworkSelected(ctx, scope, req)
			if resp := resolveField(dispatch, "bulkfetchmissingartworkselected", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["bulkmergeduplicatesselected"] {
			result, err := h.bulkMergeDuplicatesSelected(ctx, scope, req)
			if resp := resolveField(dispatch, "bulkmergeduplicatesselected", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock075(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// ── Precomputed playback rendition cache (ADR-0057, resolvers in
	// graph_playbackcache.go) ── Operator-only Console controls, Admin-Session
	// gated like the Library Health group above: the cache-health snapshot
	// (read), explicit precompute generation (§2 — never automatic), and
	// on-demand budget eviction (§4). Self-contained group.
	if fields["playbackrenditioncache"] || fields["precomputeplaybackrenditions"] || fields["evictplaybackrenditioncache"] {
		if fields["playbackrenditioncache"] {
			info, err := h.playbackRenditionCache(ctx)
			if resp := resolveField(dispatch, "playbackrenditioncache", info, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["precomputeplaybackrenditions"] {
			result, err := h.precomputePlaybackRenditions(ctx, scope, req)
			if resp := resolveField(dispatch, "precomputeplaybackrenditions", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["evictplaybackrenditioncache"] {
			result, err := h.evictPlaybackRenditionCache(ctx, scope)
			if resp := resolveField(dispatch, "evictplaybackrenditioncache", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock076(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	// ── Full metadata JSON export (ADR-0005 §5, resolver in graph_export.go) ──
	// Operator-only, Admin-Session gated like the Library Health group above: a
	// READ-only portability export of catalog curation, reveal-filtered through
	// scope.LibraryFilter(). Self-contained group.
	if fields["metadataexport"] {
		export, err := h.metadataExport(ctx, scope)
		if resp := resolveField(dispatch, "metadataexport", export, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock077(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// ── Console Enrichment panel (ADR-0041 #107, resolvers in
	// graph_enrichment.go) ── Operator-only, Admin-Session gated like the
	// Library Health group above: per-Library index status, the auto-tag review
	// queue, accept/reject (the §2.5 promotion gate), and the per-Library
	// AI-indexing opt-out. Self-contained group.
	if fields["libraryindexstatus"] || fields["pendingaitagqueue"] ||
		fields["acceptpendingaitag"] || fields["rejectpendingaitag"] ||
		fields["setlibraryaiindexing"] {
		if fields["libraryindexstatus"] {
			statuses, err := h.libraryIndexStatus(ctx, scope)
			if resp := resolveField(dispatch, "libraryindexstatus", statuses, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["pendingaitagqueue"] {
			queue, err := h.pendingAITagQueue(ctx, scope, req)
			if resp := resolveField(dispatch, "pendingaitagqueue", queue, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["acceptpendingaitag"] {
			queue, err := h.acceptPendingAITag(ctx, scope, req)
			if resp := resolveField(dispatch, "acceptpendingaitag", queue, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["rejectpendingaitag"] {
			queue, err := h.rejectPendingAITag(ctx, scope, req)
			if resp := resolveField(dispatch, "rejectpendingaitag", queue, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["setlibraryaiindexing"] {
			statuses, err := h.setLibraryAIIndexing(ctx, scope, req)
			if resp := resolveField(dispatch, "setlibraryaiindexing", statuses, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock078(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// ── Skip-segment detection per-Library toggle (ENR-7, ADR-0048 Decision
	// 3/6, resolvers in graph_skipdetect.go) ── Operator-only, Admin-Session
	// gated like the Enrichment panel above: exposes the existing
	// store.SkipDetectionEnabled/SetSkipDetectionEnabled toggle so the built
	// detector (internal/skipdetect) can run on a real Library. Self-contained
	// group.
	if fields["skipdetectionenabled"] || fields["setskipdetectionenabled"] {
		if fields["skipdetectionenabled"] {
			infos, err := h.skipDetectionEnabledRead(ctx, scope)
			if resp := resolveField(dispatch, "skipdetectionenabled", infos, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["setskipdetectionenabled"] {
			info, err := h.setSkipDetectionEnabled(ctx, scope, req)
			if resp := resolveField(dispatch, "setskipdetectionenabled", info, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock079(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["apitokens"] || fields["createapitoken"] || fields["revokeapitoken"] {
		if fields["apitokens"] {
			tokens, err := h.apiTokens(ctx)
			if resp := resolveField(dispatch, "apitokens", tokens, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["createapitoken"] {
			created, err := h.createAPIToken(ctx, scope, req)
			if resp := resolveField(dispatch, "createapitoken", created, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["revokeapitoken"] {
			revoked, err := h.revokeAPIToken(ctx, scope, req)
			if resp := resolveField(dispatch, "revokeapitoken", revoked, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock080(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	fields := dispatch.fields
	// First-run wizard (ADR-0007 §2, feature first-run-wizard): the computed
	// setup-completion state that drives onboarding. It IS the bootstrap status
	// a fresh browser device reads before any admin/manager tier can exist, so
	// it gates only on a resolved session scope (in scopedFields) — never on
	// Device Role or Admin Session. The wizard orchestrates the EXISTING
	// mutations (verifyAdminSession → createLibrary → createProfile); this field
	// reports which steps remain.
	if fields["setupstate"] {
		state, err := h.setupState(ctx)
		if resp := resolveField(dispatch, "setupstate", state, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock081(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Migration Wizard (ADR-0037, feature migration-wizard): the wizard shell
	// over the Importer Extension Point. Listing sources, running an import, and
	// reading its status are Console operations — admin/manager-gated, matching
	// the ADR-0007 §4 Console mutation class.
	if fields["importers"] || fields["runimport"] || fields["importstatus"] {
		if fields["importers"] {
			setField(dispatch, "importers", h.importers())
		}
		if fields["runimport"] {
			job, err := h.runImport(ctx, scope, req)
			if resp := resolveField(dispatch, "runimport", job, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["importstatus"] {
			job, err := h.importStatus(ctx, scope, req)
			if resp := resolveField(dispatch, "importstatus", job, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock082(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// First-scan estimate: the wizard's pre-scan projection
	// of inline (playability-gating) scan cost vs the deferrable background asset
	// backlog ("play now; sprites overnight"). It is a Console operation — it
	// discloses a filesystem layout and is part of the first-run/Library setup
	// flow — so it is admin-gated, matching the ADR-0007 §4 Console class.
	if fields["scanestimate"] {
		estimate, err := h.scanEstimate(ctx, scope, req)
		if resp := resolveField(dispatch, "scanestimate", estimate, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock083(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["activitylog"] {
		entries, err := h.activityLog(ctx, scope, req)
		if resp := resolveField(dispatch, "activitylog", entries, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock084(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["activity"] {
		entry, err := h.activityEntry(ctx, scope, req)
		if resp := resolveField(dispatch, "activity", entry, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock085(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	// Scrobble status reads (ADR-0098 area 7). scrobbleLinks is the session
	// Profile's OWN links (profile-scoped, no token); scrobbleEligibility is the
	// per-visible-Library eligibility (visibility-filtered). Both are non-admin
	// reads of non-secret state.
	if fields["scrobblelinks"] {
		links, err := h.scrobbleLinksRead(ctx, scope)
		if resp := resolveField(dispatch, "scrobblelinks", links, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock086(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	if fields["scrobbleeligibility"] {
		eligibility, err := h.scrobbleEligibilityRead(ctx, scope)
		if resp := resolveField(dispatch, "scrobbleeligibility", eligibility, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock087(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	fields := dispatch.fields
	// scrobbleStatus is the operator diagnostic feed (cross-Profile dead-letter +
	// skip records): an Admin-Session surface, the same tier as the write side
	// setScrobbleEligibility (ADR-0098 area 7).
	if fields["scrobblestatus"] {
		setField(dispatch, "scrobblestatus", h.scrobbleStatusRead())
	}
	return response{}
}

func dispatchFieldBlock088(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// In-app notification inbox (ADR-0039 §2.5, #32): the polled, client-
	// independent push primitive. Gated to the caller's own Profile and
	// reveal-filtered in the resolver — a reveal-content surface (see
	// graph_notifications.go; reveal conformance in notifications_test.go).
	if fields["notifications"] {
		items, err := h.notifications(ctx, scope, req)
		if resp := resolveField(dispatch, "notifications", items, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock089(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["marknotificationread"] {
		result, err := h.markNotificationRead(ctx, scope, req)
		if resp := resolveField(dispatch, "marknotificationread", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock092(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Device push-token registration: the viewer
	// registers its APNs/FCM/Web-Push token against its own enrolled-device
	// record. The device + Profile come from the session scope in the resolver,
	// never the request. Reports enabled=false (not an error) when push is gated
	// off so the client falls back to the polled inbox.
	if fields["registerdevicepushtoken"] {
		result, err := h.registerDevicePushToken(ctx, scope, req)
		if resp := resolveField(dispatch, "registerdevicepushtoken", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock093(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Parental extension request (ADR-0039 §2.1–2.4, PUSH-2): the "more time"
	// flow. requestExtension is managed-only (verified in the resolver
	// from scope.Profile); respondExtension is adult-only (the household-model
	// authorization — any adult, no designated guardian, no managed self-approve);
	// extensionRequest reads one by id, gated to the requester or an adult.
	// setExtensionPreauthorization is the §2.4 toggle, admin-gated like
	// setAccessSchedule (a Profile policy an adult sets in the Console/Manager).
	if fields["requestextension"] {
		result, err := h.requestExtension(ctx, scope, req)
		if resp := resolveField(dispatch, "requestextension", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock094(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["respondextension"] {
		result, err := h.respondExtension(ctx, scope, req)
		if resp := resolveField(dispatch, "respondextension", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock095(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["extensionrequest"] {
		result, err := h.extensionRequest(ctx, scope, req)
		if resp := resolveField(dispatch, "extensionrequest", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock096(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["setextensionpreauthorization"] {
		result, err := h.setExtensionPreauthorization(ctx, scope, req)
		if resp := resolveField(dispatch, "setextensionpreauthorization", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock097(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Per-title access request: managed Profile raises an "Ask a
	// parent" request (requestAccess), an adult resolves it (resolveAccessRequest),
	// either party reads one request by id (accessRequest), and either party lists
	// its visible pending requests (pendingAccessRequests).
	if fields["requestaccess"] {
		result, err := h.requestAccess(ctx, scope, req)
		if resp := resolveField(dispatch, "requestaccess", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock098(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["resolveaccessrequest"] {
		result, err := h.resolveAccessRequest(ctx, scope, req)
		if resp := resolveField(dispatch, "resolveaccessrequest", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock099(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["accessrequest"] {
		result, err := h.accessRequest(ctx, scope, req)
		if resp := resolveField(dispatch, "accessrequest", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock100(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["pendingaccessrequests"] {
		result, err := h.pendingAccessRequests(ctx, scope, req)
		if resp := resolveField(dispatch, "pendingaccessrequests", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock101(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Per-title access grant: adult lists or revokes grants.
	if fields["accessgrantsforprofile"] {
		result, err := h.accessGrantsForProfile(ctx, scope, req)
		if resp := resolveField(dispatch, "accessgrantsforprofile", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock102(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["revokeaccessgrant"] {
		result, err := h.revokeAccessGrant(ctx, scope, req)
		if resp := resolveField(dispatch, "revokeaccessgrant", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock103(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	// Remote access (ADR-0012 §1 direct HTTPS + ACME; ADR-0018/0025 self-hosted
	// relay carrier): Admin-Session-gated operator-Console status/config (§4
	// server settings). Reports the public domain, cert state, relay endpoint,
	// and carrier connection state — never a secret, never relayed bytes
	// (byte-blindness).
	if fields["remoteaccess"] {
		info, err := h.resolveRemoteAccess(ctx, scope)
		if resp := resolveField(dispatch, "remoteaccess", info, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock104(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	fields := dispatch.fields
	// Connection endpoint discovery is intentionally separate from the
	// manager-only remoteAccess status/configuration surface. Every enrolled,
	// authenticated device may refresh this versioned, secret-free ordered set.
	if fields["connectionendpoints"] {
		setField(dispatch, "connectionendpoints", h.connectionEndpoints(ctx))
	}
	return response{}
}

func dispatchFieldBlock105(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	// Watcher status (ADR-0032 — the fsnotify per-Library Watcher, resolver in
	// graph_watcher.go): Admin-Session-gated operator-Console read of per-Library
	// Watcher state (§4 Operations / §5 real-time monitoring), Library-filtered
	// through scope so a hidden Library never appears. Read-only — the Watcher is
	// ON by default and is an accelerator, never a toggle the Console flips
	// (Schedules are the correctness backstop).
	if fields["watcherstatus"] {
		statuses, err := h.watcherStatus(ctx, scope)
		if resp := resolveField(dispatch, "watcherstatus", statuses, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock106(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// qBittorrent ingest (ADR-0020, resolver in graph_qbittorrent.go):
	// Admin-Session-gated operator-Console read of the ingest config + status
	// feed (§4 Plugins/Operations) and the §6 setQBittorrentIngest mutation that
	// reconfigures routing + connection at runtime. Read-only fields are
	// credential-free (the password lives in a 0600 secret file, never on this
	// surface); the mutation accepts a password inbound and hands it straight to
	// the secure store, never echoing it back (invariant #5).
	if fields["qbittorrentingest"] || fields["setqbittorrentingest"] {
		if fields["setqbittorrentingest"] {
			result, err := h.setQBittorrentIngest(ctx, scope, req)
			if resp := resolveField(dispatch, "setqbittorrentingest", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["qbittorrentingest"] {
			info, err := h.qbittorrentIngestInfo(ctx, scope)
			if resp := resolveField(dispatch, "qbittorrentingest", info, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock107(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Encrypted-Library key model (ADR-0045 §2.4, ENC-2, resolvers in
	// graph_libencrypt.go): Admin-Session-gated Console operations that provision
	// a per-Library DEK + KEK/passphrase/Recovery-Key wraps and drive the
	// Sealed/Unsealed lifecycle. provisionEncryptedLibrary returns the user-held
	// Recovery Key exactly once; no field here ever returns a DEK, KEK,
	// passphrase, or wrapped blob (invariant #5). The status read is credential-
	// free.
	if fields["provisionencryptedlibrary"] || fields["unsealencryptedlibrary"] ||
		fields["sealencryptedlibrary"] || fields["rewrapencryptedlibrary"] ||
		fields["encryptedlibrarystatus"] {
		if fields["provisionencryptedlibrary"] {
			result, err := h.provisionEncryptedLibrary(ctx, scope, req)
			if resp := resolveField(dispatch, "provisionencryptedlibrary", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["unsealencryptedlibrary"] {
			result, err := h.unsealEncryptedLibrary(ctx, scope, req)
			if resp := resolveField(dispatch, "unsealencryptedlibrary", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["sealencryptedlibrary"] {
			result, err := h.sealEncryptedLibrary(ctx, scope, req)
			if resp := resolveField(dispatch, "sealencryptedlibrary", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["rewrapencryptedlibrary"] {
			result, err := h.rewrapEncryptedLibrary(ctx, scope, req)
			if resp := resolveField(dispatch, "rewrapencryptedlibrary", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["encryptedlibrarystatus"] {
			info, err := h.encryptedLibraryStatusField(ctx, scope, req)
			if resp := resolveField(dispatch, "encryptedlibrarystatus", info, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock108(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Per-field source-precedence locks (ADR-0027 / ADR-0009 §8, resolvers in
	// graph_fieldlocks.go): a Manage-area read/set of one Media's metadata-field
	// locks. Manager-role gated like the other curation mutations; reads and
	// writes are reveal-and-Library filtered so a hidden Media is "not found".
	if fields["mediafieldlocks"] || fields["setmediafieldlock"] {
		if fields["mediafieldlocks"] {
			locks, err := h.mediaFieldLocks(ctx, scope, req)
			if resp := resolveField(dispatch, "mediafieldlocks", locks, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["setmediafieldlock"] {
			locks, err := h.setMediaFieldLock(ctx, scope, req)
			if resp := resolveField(dispatch, "setmediafieldlock", locks, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock109(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["browseserverfolders"] || fields["pathpicker"] || fields["addpathpickerroot"] {
		if fields["browseserverfolders"] {
			tree, err := h.browseServerFolders(ctx, scope, req)
			if resp := resolveField(dispatch, "browseserverfolders", tree, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["pathpicker"] {
			picker, err := h.pathPicker(ctx, scope, req)
			if resp := resolveField(dispatch, "pathpicker", picker, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["addpathpickerroot"] {
			root, err := h.addPathPickerRoot(ctx, scope, req)
			if resp := resolveField(dispatch, "addpathpickerroot", root, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock110(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// ── Bulk edit + file operations (ADR-0009 §5–6, resolvers in
	// graph_bulkedit.go / graph_fileops.go) ── Manage-area file and curation
	// operations over Library roots and Media, sharing the dry-run /
	// batch-undo envelope where writes apply. Manager-role gated like the other
	// Console curation controls. Self-contained group.
	if fields["foldertree"] || fields["createmanagedfolder"] || fields["renamemanagedpath"] || fields["batchrenamemanagedpaths"] ||
		fields["movemanagedpath"] || fields["deletemanagedpath"] || fields["restoremanagedpath"] ||
		fields["bulkeditpreview"] || fields["bulkeditapply"] ||
		fields["organizepreview"] || fields["organizeapply"] ||
		fields["mergemedia"] || fields["mergeentities"] || fields["undobatch"] ||
		fields["upsertentityrelation"] || fields["linkmediarelation"] {
		if fields["foldertree"] {
			tree, err := h.folderTree(ctx, scope, req)
			if resp := resolveField(dispatch, "foldertree", tree, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["createmanagedfolder"] {
			path, err := h.createManagedFolder(ctx, scope, req)
			if resp := resolveField(dispatch, "createmanagedfolder", path, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["renamemanagedpath"] {
			path, err := h.renameManagedPath(ctx, scope, req)
			if resp := resolveField(dispatch, "renamemanagedpath", path, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["batchrenamemanagedpaths"] {
			result, err := h.batchRenameManagedPaths(ctx, scope, req)
			if resp := resolveField(dispatch, "batchrenamemanagedpaths", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["movemanagedpath"] {
			path, err := h.moveManagedPath(ctx, scope, req)
			if resp := resolveField(dispatch, "movemanagedpath", path, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["deletemanagedpath"] {
			trash, err := h.deleteManagedPath(ctx, scope, req)
			if resp := resolveField(dispatch, "deletemanagedpath", trash, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["restoremanagedpath"] {
			path, err := h.restoreManagedPath(ctx, scope, req)
			if resp := resolveField(dispatch, "restoremanagedpath", path, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["bulkeditpreview"] {
			changes, err := h.bulkEditPreview(ctx, scope, req)
			if resp := resolveField(dispatch, "bulkeditpreview", changes, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["bulkeditapply"] {
			result, err := h.bulkEditApply(ctx, scope, req)
			if resp := resolveField(dispatch, "bulkeditapply", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["organizepreview"] {
			changes, err := h.organizePreview(ctx, scope, req)
			if resp := resolveField(dispatch, "organizepreview", changes, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["organizeapply"] {
			result, err := h.organizeApply(ctx, scope, req)
			if resp := resolveField(dispatch, "organizeapply", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["mergemedia"] {
			result, err := h.mergeApply(ctx, scope, req)
			if resp := resolveField(dispatch, "mergemedia", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["mergeentities"] {
			result, err := h.mergeEntities(ctx, scope, req)
			if resp := resolveField(dispatch, "mergeentities", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["undobatch"] {
			result, err := h.undoBatch(ctx, scope, req)
			if resp := resolveField(dispatch, "undobatch", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["upsertentityrelation"] {
			result, err := h.upsertEntityRelation(ctx, scope, req)
			if resp := resolveField(dispatch, "upsertentityrelation", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["linkmediarelation"] {
			result, err := h.linkMediaRelation(ctx, scope, req)
			if resp := resolveField(dispatch, "linkmediarelation", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock111(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Authorized native→server import transport (apple ADR-0003 §1/§3,
	// POWERTOOLS-6B). The native Companion resolves its security-scoped URL,
	// couriers the locator over the authorized loopback bearer to
	// registerImportSource (which mints the server token the JS realm holds), then
	// imports it under a Library root via importManagedFile. Both fields are gated
	// on the Admin Session class — the tier that bearer authenticates as — so the
	// JS realm can neither self-mint a token nor inject a raw path; the store
	// refuses any token it did not vend.
	if fields["registerimportsource"] || fields["importmanagedfile"] {
		if fields["registerimportsource"] {
			token, err := h.registerImportSource(ctx, scope, req)
			if resp := resolveField(dispatch, "registerimportsource", token, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["importmanagedfile"] {
			path, err := h.importManagedFile(ctx, scope, req)
			if resp := resolveField(dispatch, "importmanagedfile", path, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}

func dispatchFieldBlock112(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// My Data / Privacy Panel: the caller's own engagement export,
	// hard delete, and session/token view. Never manager-gated — every session
	// reads and deletes its OWN Profile's data, scoped by scope.ProfileID only.
	if fields["myengagement"] {
		export, err := h.myEngagement(ctx, scope, req)
		if resp := resolveField(dispatch, "myengagement", export, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock113(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	if fields["mydatasummary"] {
		summary, err := h.myDataSummary(ctx, scope)
		if resp := resolveField(dispatch, "mydatasummary", summary, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock114(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["myengagementlog"] {
		log, err := h.myEngagementLog(ctx, scope, req)
		if resp := resolveField(dispatch, "myengagementlog", log, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock115(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["deletemyengagement"] {
		result, err := h.deleteMyEngagement(ctx, scope, req)
		if resp := resolveField(dispatch, "deletemyengagement", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock116(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	if fields["mysessions"] {
		sessions, err := h.mySessions(ctx, scope)
		if resp := resolveField(dispatch, "mysessions", sessions, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock117(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	if fields["offlinepacks"] {
		packs, err := h.offlinePacks_(ctx, scope)
		if resp := resolveField(dispatch, "offlinepacks", packs, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock118(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Offline Packs (ADR-0098 area 6): build/list/revoke are self-scoped to the
	// caller's Profile (no manager gate, like My Data); deleteMyPackTokens clears
	// the caller's own pack-token transparency ledger rows.
	if fields["createofflinepack"] {
		pack, err := h.createOfflinePack(ctx, scope, req)
		if resp := resolveField(dispatch, "createofflinepack", pack, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock119(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["revokeofflinepack"] {
		pack, err := h.revokeOfflinePack(ctx, scope, req)
		if resp := resolveField(dispatch, "revokeofflinepack", pack, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock120(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["syncofflinepackprogress"] {
		pack, err := h.syncOfflinePackProgress(ctx, scope, req)
		if resp := resolveField(dispatch, "syncofflinepackprogress", pack, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock121(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	if fields["deletemypacktokens"] {
		if err := h.store.DeleteMyPackTokens(ctx, scope.ProfileID); err != nil {
			return resolveField(dispatch, "deletemypacktokens", true, err)
		}
		if err := h.logActivity(ctx, scope, store.ActivityOfflinePackTokensDelete, "offline_pack_tokens", string(scope.ProfileID), ""); err != nil {
			return resolveField(dispatch, "deletemypacktokens", true, err)
		}
		if resp := resolveField(dispatch, "deletemypacktokens", true, nil); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock122(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	// Per-Profile Mute / "Not interested": the caller's own
	// self-authored mute list (read), set/clear of one target, and the My-Data
	// "clear all" reset. Never manager-gated — a Mute is self-authored and
	// self-cleared, scoped by scope.ProfileID only, exactly like My Data.
	if fields["mutes"] {
		list, err := h.mutes(ctx, scope)
		if resp := resolveField(dispatch, "mutes", list, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock123(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["setmute"] {
		result, err := h.setMute(ctx, scope, req)
		if resp := resolveField(dispatch, "setmute", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock124(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["clearmute"] {
		result, err := h.clearMute(ctx, scope, req)
		if resp := resolveField(dispatch, "clearmute", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock125(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	if fields["clearallmutes"] {
		result, err := h.clearAllMutes(ctx, scope)
		if resp := resolveField(dispatch, "clearallmutes", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock126(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Per-Profile engagement counter (ADR-0027 D1, resolvers in
	// graph_engagement_event.go): the caller's own counter for one plugin-declared
	// kind + target (read), an increment, and a remove-last correction. Never
	// manager-gated — the counter is Profile-private, scoped by scope.ProfileID
	// only, exactly like My Data. The kind is validated against the activated
	// plugins' declared kinds, so Core never names a domain kind (invariant #1).
	if fields["engagementcounter"] {
		result, err := h.engagementCounter(ctx, scope, req)
		if resp := resolveField(dispatch, "engagementcounter", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock127(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["recordengagement"] {
		result, err := h.recordEngagement(ctx, scope, req)
		if resp := resolveField(dispatch, "recordengagement", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock128(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["removelastengagement"] {
		result, err := h.removeLastEngagement(ctx, scope, req)
		if resp := resolveField(dispatch, "removelastengagement", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock129(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	// Cross-device session control: session-scoped, same-Profile +
	// Reveal-gated in the resolvers. profileSessions enumerates the caller's own
	// Profile's active playbacks; controlSession publishes a transport command to
	// another same-Profile device's session over the existing /api/events SSE.
	if fields["profilesessions"] {
		sessions, err := h.profileSessions(ctx, scope)
		if resp := resolveField(dispatch, "profilesessions", sessions, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock130(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["controlsession"] {
		result, err := h.controlSession(ctx, scope, req)
		if resp := resolveField(dispatch, "controlsession", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock131(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	fields := dispatch.fields
	// Ambient Group Conductor: session-scoped, same-Profile-
	// gated form/join/leave + transition/drift-report. Membership is ephemeral
	// (in-memory conductor.Registry; never persisted); sync signals ride the
	// existing /api/events SSE outbox. LAN-only is enforced upstream at the
	// calibration endpoint (a device can only join with LAN-calibrated numbers).
	if fields["formgroup"] {
		result, err := h.formGroup(ctx, scope)
		if resp := resolveField(dispatch, "formgroup", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock132(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["joingroup"] {
		result, err := h.joinGroup(ctx, scope, req)
		if resp := resolveField(dispatch, "joingroup", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock133(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["leavegroup"] {
		result, err := h.leaveGroup(ctx, scope, req)
		if resp := resolveField(dispatch, "leavegroup", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock134(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["transitiongroup"] {
		result, err := h.transitionGroup(ctx, scope, req)
		if resp := resolveField(dispatch, "transitiongroup", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock135(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["reportdrift"] {
		result, err := h.reportDrift(ctx, scope, req)
		if resp := resolveField(dispatch, "reportdrift", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock136(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Watch Party: a Group Conductor session with a
	// full-AV posture + host-role transport authority, formed cross-Profile by
	// invite → notification → accept with a Reveal intersection enforced at accept
	// against each accepting session's own filter. Ephemeral (in-memory party +
	// invite state, no migration); host transport rides the same SSE outbox as
	// the Ambient Group transition. LAN-only is inherited from the same upstream
	// calibration gate the Conductor uses.
	if fields["createwatchparty"] {
		result, err := h.createWatchParty(ctx, scope, req)
		if resp := resolveField(dispatch, "createwatchparty", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock137(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["invitetowatchparty"] {
		result, err := h.inviteToWatchParty(ctx, scope, req)
		if resp := resolveField(dispatch, "invitetowatchparty", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock138(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["acceptwatchparty"] {
		result, err := h.acceptWatchParty(ctx, scope, req)
		if resp := resolveField(dispatch, "acceptwatchparty", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock139(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["watchpartytransport"] {
		result, err := h.watchPartyTransport(ctx, scope, req)
		if resp := resolveField(dispatch, "watchpartytransport", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock140(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["requestwatchpartytransport"] {
		result, err := h.requestWatchPartyTransport(ctx, scope, req)
		if resp := resolveField(dispatch, "requestwatchpartytransport", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock141(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["watchpartyautoskip"] {
		result, err := h.watchPartyAutoSkip(ctx, scope, req)
		if resp := resolveField(dispatch, "watchpartyautoskip", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock142(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["leavewatchparty"] {
		result, err := h.leaveWatchParty(ctx, scope, req)
		if resp := resolveField(dispatch, "leavewatchparty", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock143(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Console operator-delete + managed-Profile guardian path (ADR-0036
	// §2.5/§2.6): an operator (or guardian, through the same Console) hard-deletes
	// a managed Profile's engagement footprint. Admin-Session-gated — this is a
	// Console mutation, NOT the self-service deleteMyEngagement above; an
	// un-admin-gated operator-delete is a security hole. The managed-target rule
	// is enforced in the resolver.
	if fields["operatordeleteprofiledata"] {
		result, err := h.operatorDeleteProfileData(ctx, scope, req)
		if resp := resolveField(dispatch, "operatordeleteprofiledata", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock144(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Owner transfer: move the single owner flag to another adult
	// Profile. Admin-Session-gated — once an owner is designated only the owner
	// can hold an Admin Session, so this is the owner handing ownership off. The
	// store enforces the single-owner invariant + adult-only rule.
	if fields["transferprofileowner"] {
		result, err := h.transferProfileOwner(ctx, scope, req)
		if resp := resolveField(dispatch, "transferprofileowner", result, err); len(resp.Errors) > 0 {
			return resp
		}
	}
	return response{}
}

func dispatchFieldBlock145(dispatch *fieldDispatchContext) response {
	h := dispatch.handler
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	// Live sessions dashboard (ADR-0007 §4 Operations): Admin-Session-gated read
	// of currently-active playback sessions + a kill mutation. Resolves over the
	// existing Stream Token ledger (no new storage); see graph_livesessions.go.
	if fields["livesessions"] || fields["terminatesession"] || fields["revokedevice"] ||
		fields["devices"] || fields["revokealldevices"] || fields["setdevicerole"] {
		if fields["livesessions"] {
			sessions, err := h.liveSessions(ctx, scope)
			if resp := resolveField(dispatch, "livesessions", sessions, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["devices"] {
			list, err := h.devices(ctx, scope)
			if resp := resolveField(dispatch, "devices", list, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["terminatesession"] {
			result, err := h.terminateSession(ctx, scope, req)
			if resp := resolveField(dispatch, "terminatesession", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["revokedevice"] {
			result, err := h.revokeDevice(ctx, scope, req)
			if resp := resolveField(dispatch, "revokedevice", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["revokealldevices"] {
			result, err := h.revokeAllDevices(ctx, scope)
			if resp := resolveField(dispatch, "revokealldevices", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
		if fields["setdevicerole"] {
			result, err := h.setDeviceRole(ctx, scope, req)
			if resp := resolveField(dispatch, "setdevicerole", result, err); len(resp.Errors) > 0 {
				return resp
			}
		}
	}
	return response{}
}
