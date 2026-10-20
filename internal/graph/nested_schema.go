package graph

import (
	"slices"
	"strings"

	"github.com/beamfall/core/internal/graphshape"
)

// H23-B: nested-field schema source-of-truth.
//
// The H23-A manifest (SchemaManifest) captures only TOP-LEVEL field names with
// their effect/tier — it says nothing about the SHAPE each field resolves to.
// The depth-1 dispatcher routes solely on those top-level names and never
// inspects nested selection sets, so a client requesting a nonexistent NESTED
// field (the H1 startMillis, the H17 sourceItems.libraryID, an H2 mediaClass)
// gets silently-incomplete data with no error.
//
// This file reflects over the dispatcher's own Go result structs to produce a
// committed nested schema (NestedSchema) — the conformance gate byte-compares
// it against testdata/nested-schema.json, so a struct-shape change without a
// manifest regen fails CI, and clients/reviewers can diff their nested
// selections against the real wire shape. It is a CI-time contract artifact; it
// does NOT change runtime dispatch or authorization (see nested_schema_test
// and the H23-B notes for why runtime nested rejection is gated on conforming
// the shipped clients first).

// resultTypes maps each dispatchable top-level field (lowercased, matching
// topLevelFields normalization) to a zero value of the Go type the dispatcher
// assigns to data[field]. Slice fields map to the element type — the schema
// describes the shape of one item. This is the single declarative bridge
// between the dispatcher's field registry and the result structs reflected
// below; the init() check fails the build if a scoped field lacks an entry, so
// a new resolver can't ship without declaring its nested shape.
//
// Keys mirror queryFields/mutationFields exactly (plus the serverInfo
// exception). Values are the result struct (or its element type for list
// fields). The map is the source the reflection walk consumes; it intentionally
// holds no behavior, only shape.
var resultTypes = map[string]any{
	// Unauthenticated.
	"serverinfo": ServerInfo{},

	// Reads (queryFields).
	"navigationmanifest":     NavigationManifestResponse{},
	"manifest":               NavigationManifestResponse{},
	"plugins":                PluginInfo{},
	"entity":                 EntityInfo{},
	"entitiesformedia":       RelatedEntityInfo{},
	"entityrelations":        EntityRelationInfo{},
	"mediaforentity":         SourceItem{},
	"mediafiles":             MediaFileInfo{},
	"mediarelations":         MediaRelationInfo{},
	"mediafields":            EntityFieldInfo{},
	"artworkcandidates":      ArtworkCandidateInfo{},
	"markers":                MarkerInfo{},
	"trashedmarkers":         MarkerInfo{},
	"sourceitems":            SourceItem{},
	"ambientqueue":           SourceItem{},
	"currentsession":         SessionInfo{},
	"profiles":               ProfileSummary{},
	"search":                 SourceItem{},
	"searchresult":           SearchResultResponse{},
	"semanticcapability":     SemanticCapabilityInfo{},
	"searchentities":         SourceItem{},
	"searchfeeds":            FeedSearchResult{},
	"media":                  MediaInfo{},
	"trashlist":              TrashEntry{},
	"tags":                   TagInfo{},
	"tagassignments":         TagAssignmentInfo{},
	"libraries":              LibraryInfo{},
	"qualitypolicy":          QualityPolicyInfo{},
	"accessschedule":         AccessScheduleInfo{},
	"clientprefs":            ClientPrefInfo{},
	"identifysearch":         IdentifyCandidate{},
	"identifylookup":         IdentifyCandidate{},
	"fixmatchcandidates":     FixMatchCandidateInfo{},
	"fixmatchpreview":        FixMatchFieldDiff{},
	"unmatchedqueue":         UnmatchedItemInfo{},
	"unmatchedrecords":       UnmatchedRecordInfo{},
	"unmatchedsuppressions":  UnmatchedSuppressionInfo{},
	"navoverride":            NavOverrideInfo{},        // WEB-5J (ADR-0061 §Layout editor): current override blob
	"navoverridehistory":     NavOverrideHistoryInfo{}, // WEB-5J (ADR-0061 §Layout editor): audit-log entry shape
	"subscriptions":          SubscriptionInfo{},
	"jobs":                   JobInfo{},
	"job":                    JobInfo{},
	"jobevents":              JobEventInfo{},
	"runeventscompaction":    JobInfo{},
	"operationsgovernor":     OperationsGovernorInfo{},
	"schedules":              ScheduleInfo{},
	"apitokens":              APITokenInfo{},
	"adminsession":           AdminSessionInfo{},
	"apikeys":                ScopedAPIKeyInfo{},
	"activitylog":            ActivityEntryInfo{},
	"activity":               ActivityEntryInfo{}, // ATEST-26: singular by-ID lookup
	"myengagement":           MyEngagementExportInfo{},
	"mydatasummary":          MyDataSummaryInfo{},
	"myengagementlog":        MyEngagementLogInfo{},
	"mysessions":             MySessionsInfo{},
	"mutes":                  MyMuteInfo{},
	"engagementcounter":      EngagementCounterResult{},
	"profilesessions":        ProfileSessionInfo{},
	"libraryhealth":          LibraryHealthInfo{},
	"libraryhealthitems":     HealthItemInfo{},
	"libraryduplicategroups": DuplicateGroupInfo{},
	"metadataexport":         MetadataExportInfo{},
	"libraryindexstatus":     LibraryIndexStatusInfo{},
	"pendingaitagqueue":      PendingAITagInfo{},
	"skipdetectionenabled":   SkipDetectionInfo{},
	"notifications":          NotificationInfo{},
	"extensionrequest":       ExtensionRequestInfo{},
	// ADR-0113 §2.2: per-title access request reads.
	"accessrequest":         AccessRequestInfo{},
	"pendingaccessrequests": AccessRequestInfo{},
	// ADR-0113 §2.3: per-title access grant read (Console UI exception list).
	"accessgrantsforprofile": AccessGrantInfo{},
	"connectionendpoints":    ConnectionEndpoints{},
	"remoteaccess":           RemoteAccessInfo{},
	"watcherstatus":          WatcherStatusInfo{},
	"encryptedlibrarystatus": EncryptedLibraryStatusInfo{},
	"mediafieldlocks":        FieldLockInfo{},
	"consoleconfig":          ConsoleConfigInfo{},
	"updatestatus":           UpdateStatusInfo{},
	"transcodeconfig":        TranscodeConfigInfo{},
	"livesessions":           LiveSessionInfo{},
	"devices":                DeviceInfo{},
	"setdevicerole":          DeviceInfo{},
	"setupstate":             SetupState{},
	"importers":              ImporterInfo{},
	"importstatus":           ImportStatusInfo{},
	"scanestimate":           ScanEstimateInfo{},
	"savedfilters":           SavedFilterInfo{},
	"collections":            CollectionInfo{},
	"smartcollections":       SmartCollectionInfo{},
	"channels":               ChannelInfo{},
	"stations":               SavedStationInfo{},
	"stationgenerate":        StationGenerateResult{},
	"foldertree":             FolderEntryInfo{},
	"browseserverfolders":    FolderEntryInfo{},
	"pathpicker":             PathPickerInfo{},
	"bulkeditpreview":        BulkEditChangeInfo{},
	"organizepreview":        FileChangeInfo{},

	// Writes (mutationFields).
	"switchprofile":              SessionInfo{},
	"reveal":                     RevealState{},
	"relock":                     RevealState{},
	"setrevealpin":               RevealState{},
	"reportplayback":             PlaybackReport{},
	"controlsession":             ControlSessionResult{},
	"formgroup":                  GroupInfo{},
	"joingroup":                  GroupInfo{},
	"leavegroup":                 LeaveGroupResult{},
	"transitiongroup":            GroupTransitionResult{},
	"reportdrift":                ReportDriftResult{},
	"createwatchparty":           WatchPartyInfo{},
	"invitetowatchparty":         WatchPartyInfo{},
	"acceptwatchparty":           AcceptWatchPartyResult{},
	"watchpartytransport":        GroupTransitionResult{},
	"requestwatchpartytransport": RequestTransportResult{},
	"watchpartyautoskip":         WatchPartyAutoSkipResult{},
	"leavewatchparty":            LeaveWatchPartyResult{},
	"marknotificationread":       NotificationReadResult{},
	"registerdevicepushtoken":    PushRegistrationResult{},
	"requestextension":           ExtensionRequestInfo{},
	"respondextension":           ExtensionRequestInfo{},
	// ADR-0113 §2.2: per-title access request mutations.
	"requestaccess":        AccessRequestInfo{},
	"resolveaccessrequest": AccessRequestInfo{},
	// ADR-0113 §2.3: per-title access grant mutation (adult revoke).
	"revokeaccessgrant":                  AccessGrantInfo{},
	"setextensionpreauthorization":       SetExtensionPreauthorizationResult{},
	"setfavorite":                        MediaInfo{},
	"queueadd":                           SourceItem{},
	"queueremove":                        SourceItem{},
	"queuemove":                          SourceItem{},
	"queueclear":                         SourceItem{},
	"stationstart":                       SourceItem{},
	"stationsave":                        SavedStationInfo{},
	"stationdelete":                      SavedStationInfo{},
	"linkscrobble":                       ScrobbleLinkInfo{},
	"unlinkscrobble":                     ScrobbleLinkInfo{},
	"setscrobbleeligibility":             ScrobbleEligibilityInfo{},
	"scrobblelinks":                      ScrobbleLinkInfo{},        // MUSIC-7: list of own links
	"scrobbleeligibility":                ScrobbleEligibilityInfo{}, // MUSIC-7: list of per-Library eligibility
	"scrobblestatus":                     ScrobbleStatusInfo{},      // MUSIC-7: admin diagnostic object
	"setclientpref":                      ClientPrefInfo{},
	"setfollow":                          FollowInfo{},
	"associatesubscriptionentry":         SubscriptionEntryAssociationInfo{},
	"setmute":                            MuteResult{},
	"clearmute":                          MuteResult{},
	"clearallmutes":                      ClearAllMutesResult{},
	"recordengagement":                   EngagementCounterInfo{},
	"removelastengagement":               EngagementCounterInfo{},
	"deletemyengagement":                 MyDataDeleteResult{},
	"operatordeleteprofiledata":          OperatorDeleteProfileDataResult{},
	"transferprofileowner":               TransferProfileOwnerResult{},
	"createmarker":                       MarkerInfo{},
	"updatemarker":                       MarkerInfo{},
	"deletemarker":                       MarkerInfo{},
	"restoremarker":                      MarkerInfo{},
	"pinartworkcandidate":                ArtworkCandidateInfo{},
	"trashmedia":                         TrashEntry{},
	"restoremedia":                       TrashEntry{},
	"createtag":                          TagInfo{},
	"updatetag":                          TagInfo{},
	"deletetag":                          TagInfo{},
	"restoretag":                         TagInfo{},
	"assigntag":                          TagAssignmentInfo{},
	"unassigntag":                        TagAssignmentInfo{},
	"bulkassigntags":                     TagBulkAssignmentInfo{},
	"purgetrash":                         BulkOpResult{},
	"setmediafieldlock":                  FieldLockInfo{},
	"bulkeditapply":                      BatchResultInfo{},
	"registerimportsource":               registerImportSourceResult{},
	"importmanagedfile":                  ManagedPathInfo{},
	"createmanagedfolder":                ManagedPathInfo{},
	"renamemanagedpath":                  ManagedPathInfo{},
	"batchrenamemanagedpaths":            BatchResultInfo{},
	"movemanagedpath":                    ManagedPathInfo{},
	"deletemanagedpath":                  ManagedTrashInfo{},
	"restoremanagedpath":                 ManagedPathInfo{},
	"organizeapply":                      BatchResultInfo{},
	"mergemedia":                         BatchResultInfo{},
	"mergeentities":                      BatchResultInfo{},
	"upsertentityrelation":               RelationWriteResult{},
	"linkmediarelation":                  RelationWriteResult{},
	"undobatch":                          BatchResultInfo{},
	"identifyapply":                      MediaInfo{},
	"fixmatch":                           MediaInfo{},
	"addmediaexternalid":                 MediaInfo{},
	"dismissunmatched":                   deleteLibraryResult{},
	"createmediafromunmatched":           MediaInfo{},
	"suppressunmatched":                  deleteLibraryResult{},
	"resetunmatchedsuppression":          deleteLibraryResult{},
	"setnavoverride":                     NavOverrideInfo{}, // WEB-5J (ADR-0061 §Layout editor): patched override blob
	"resetnavoverride":                   NavOverrideInfo{}, // WEB-5J (ADR-0061 §Layout editor): empty override after reset
	"restorenavoverride":                 NavOverrideInfo{}, // WEB-5J (ADR-0061 §Layout editor): restored override blob
	"createprofile":                      ProfileInfo{},
	"updateprofile":                      ProfileInfo{},
	"setprofilelibraries":                ProfileInfo{},
	"setcertificationceiling":            ProfileInfo{},
	"setaccessschedule":                  AccessScheduleInfo{},
	"createlibrary":                      LibraryInfo{},
	"setlibraryroots":                    LibraryInfo{},
	"addpathpickerroot":                  PathPickerRootInfo{},
	"setlibrarypreviewclips":             LibraryInfo{},
	"setlibrarybacking":                  LibraryInfo{},
	"setlibraryrestricted":               LibraryInfo{},
	"setqualitypolicy":                   QualityPolicyInfo{},
	"deletelibrary":                      deleteLibraryResult{},
	"scanlibrary":                        JobInfo{},
	"addsubscription":                    SubscriptionInfo{},
	"removesubscription":                 SubscriptionInfo{},
	"setsubscriptionretention":           SubscriptionInfo{},
	"runjob":                             JobInfo{},
	"jobcancel":                          JobInfo{},
	"jobretry":                           JobInfo{},
	"updateschedule":                     ScheduleInfo{},
	"backup":                             BackupInfo{},
	"backuphistory":                      BackupHistoryEntry{},
	"verifybackup":                       VerifyBackupInfo{},
	"runimport":                          JobInfo{},
	"verifyadminsession":                 AdminSessionInfo{},
	"endadminsession":                    AdminSessionInfo{},
	"createapikey":                       CreatedScopedAPIKeyInfo{},
	"revokeapikey":                       ScopedAPIKeyInfo{},
	"createapitoken":                     CreatedAPITokenInfo{},
	"revokeapitoken":                     APITokenInfo{},
	"setconsoleconfig":                   ConsoleConfigInfo{},
	"setpluginactive":                    ConsoleConfigInfo{},
	"uninstallplugindata":                ConsoleConfigInfo{},
	"setpluginconfig":                    ConsoleConfigInfo{},
	"pluginworker":                       PluginWorkerInfo{},
	"approvepluginpermission":            PluginWorkerInfo{},
	"setpluginupdatesource":              PluginWorkerInfo{},
	"runpluginlifecycleaction":           PluginWorkerInfo{},
	"setscrapercredential":               ScraperCredentialInfo{},
	"validatescrapercredential":          ScraperCredentialInfo{},
	"disablescrapercredential":           ScraperCredentialInfo{},
	"settranscodeconfig":                 TranscodeConfigInfo{},
	"provisionencryptedlibrary":          ProvisionEncryptedLibraryInfo{},
	"unsealencryptedlibrary":             EncryptedLibraryStatusInfo{},
	"sealencryptedlibrary":               EncryptedLibraryStatusInfo{},
	"rewrapencryptedlibrary":             ProvisionEncryptedLibraryInfo{},
	"bulkrescanunmatched":                BulkOpResult{},
	"bulktrashbroken":                    BulkOpResult{},
	"bulkmergeduplicates":                BulkOpResult{},
	"bulkrescrapescrapefailures":         BulkOpResult{},
	"bulkfetchmissingartwork":            BulkOpResult{},
	"bulkrescanunmatchedselected":        BulkOpResult{},
	"bulktrashbrokenselected":            BulkOpResult{},
	"bulkrescrapescrapefailuresselected": BulkOpResult{},
	"bulkfetchmissingartworkselected":    BulkOpResult{},
	"bulkmergeduplicatesselected":        BulkOpResult{},
	"playbackrenditioncache":             PlaybackCacheInfo{},
	"precomputeplaybackrenditions":       PlaybackCacheOpResult{},
	"evictplaybackrenditioncache":        PlaybackCacheOpResult{},
	"terminatesession":                   TerminateSessionResult{},
	"revokedevice":                       RevokeDeviceResult{},
	"revokealldevices":                   RevokeAllDevicesResult{},
	"createcollection":                   CollectionInfo{},
	"updatecollection":                   CollectionInfo{},
	"deletecollection":                   CollectionInfo{},
	"restorecollection":                  CollectionInfo{},
	"addcollectionmedia":                 CollectionInfo{},
	"removecollectionmedia":              CollectionInfo{},
	"reordercollectionmedia":             CollectionInfo{},
	"createsavedfilter":                  SavedFilterInfo{},
	"deletesavedfilter":                  SavedFilterInfo{},
	"createsmartcollection":              SmartCollectionInfo{},
	"updatesmartcollection":              SmartCollectionInfo{},
	"deletesmartcollection":              SmartCollectionInfo{},
	"createchannel":                      ChannelInfo{},
	"updatechannel":                      ChannelInfo{},
	"deletechannel":                      ChannelInfo{},
	// Retention rules
	"retentionrules":            []RetentionRuleInfo{},
	"createretentionrule":       RetentionRuleInfo{},
	"updateretentionrule":       RetentionRuleInfo{},
	"deleteretentionrule":       RetentionRuleInfo{},
	"setretentioncandidatehold": retentionCandidateHoldResult{},
	"acceptpendingaitag":        PendingAITagInfo{},
	"rejectpendingaitag":        PendingAITagInfo{},
	"setlibraryaiindexing":      LibraryIndexStatusInfo{},
	"setskipdetectionenabled":   SkipDetectionInfo{},
	"offlinepacks":              []OfflinePackInfo{},
	"createofflinepack":         OfflinePackInfo{},
	"revokeofflinepack":         OfflinePackInfo{},
	"syncofflinepackprogress":   OfflinePackInfo{},
	"deletemypacktokens":        deleteMyPackTokensResult{},
}

var fieldArguments = map[string][]NestedArgument{
	"ambientqueue":     sourceArguments(),
	"entitiesformedia": {{Name: "mediaID", Kind: "string"}, {Name: "relation", Kind: "string"}},
	"entity":           {{Name: "id", Kind: "string"}},
	"entityrelations":  {{Name: "entityID", Kind: "string"}, {Name: "relation", Kind: "string", Optional: true}},
	"fixmatch": {
		{Name: "candidateID", Kind: "string", Optional: true},
		{Name: "importRunID", Kind: "string", Optional: true},
		{Name: "mediaID", Kind: "string"},
		{Name: "pluginID", Kind: "string", Optional: true},
		{Name: "recordID", Kind: "string", Optional: true},
		{Name: "scraperID", Kind: "string", Optional: true},
	},
	"importstatus":   {{Name: "jobID", Kind: "string"}},
	"media":          {{Name: "id", Kind: "string"}},
	"mediafields":    {{Name: "mediaID", Kind: "string"}},
	"mediafiles":     {{Name: "mediaID", Kind: "string"}},
	"mediaforentity": {{Name: "entityID", Kind: "string"}},
	"mediarelations": {{Name: "mediaID", Kind: "string"}, {Name: "relation", Kind: "string"}},
	"search":         {{Name: "query", Kind: "string", Optional: true}},
	"searchentities": {{Name: "query", Kind: "string", Optional: true}},
	"searchfeeds":    {{Name: "query", Kind: "string"}},
	"searchresult":   {searchModeArgument(), {Name: "query", Kind: "string", Optional: true}},
	"sourceitems":    sourceArguments(),
}

func searchModeArgument() NestedArgument {
	return NestedArgument{Name: "mode", Kind: "string", Optional: true, Enum: []string{
		string(SearchModeAuto),
		string(SearchModeKeyword),
		string(SearchModeRecommend),
		string(SearchModeSemantic),
	}}
}

func entityFilterArgument() NestedArgument {
	return NestedArgument{Name: "entityFilter", Kind: "object", Optional: true, Fields: []NestedArgument{
		{Name: "kind", Kind: "string"},
		{Name: "name", Kind: "string"},
	}}
}

func sourceArguments() []NestedArgument {
	return []NestedArgument{
		entityFilterArgument(),
		{Name: "entityID", Kind: "string", Optional: true},
		{Name: "entityKind", Kind: "string", Optional: true},
		{Name: "fieldFilters", Kind: "map", Optional: true, Elem: &NestedArgument{
			Kind: "union",
			AnyOf: []NestedArgument{
				{Kind: "scalar"},
				{Kind: "object", Fields: []NestedArgument{
					{Name: "gte", Kind: "scalar", Optional: true},
					{Name: "lte", Kind: "scalar", Optional: true},
				}},
			},
		}},
		entityFiltersArgument(),
		{Name: "kind", Kind: "string", Optional: true},
		{Name: "libraryID", Kind: "string", Optional: true},
		{Name: "limit", Kind: "int", Optional: true},
		searchModeArgument(),
		{Name: "offset", Kind: "int", Optional: true},
		{Name: "query", Kind: "string", Optional: true},
		{Name: "seed", Kind: "string", Optional: true},
		{Name: "value", Kind: "string", Optional: true},
	}
}

// retentionCandidateHoldResult is the result shape for setRetentionCandidateHold:
// ruleID, mediaID, and hold confirm the applied state.
type retentionCandidateHoldResult struct {
	RuleID  string `json:"ruleID"`
	MediaID string `json:"mediaID"`
	Hold    bool   `json:"hold"`
}

// deleteMyPackTokensResult models the boolean-leaf pack-token clear mutation:
// execute() assigns data["deleteMyPackTokens"] = true, so like
// deleteLibraryResult it carries no nested fields.
type deleteMyPackTokensResult struct{}

// deleteLibraryResult models the one scalar-result mutation: execute() assigns
// data["deleteLibrary"] = true. It carries no nested fields — a boolean leaf —
// so reflecting over it yields an empty selection set, which is exactly the
// nested shape clients may request (none).
type deleteLibraryResult struct{}

// registerImportSourceResult models the scalar-result import-token mutation
// (apple ADR-0003 §3, POWERTOOLS-6B): execute() assigns data["registerImportSource"]
// = the server-minted token string. A string leaf carries no nested fields, so its
// selection set is empty — the only shape a client may request.
type registerImportSourceResult struct{}

// init checks that every dispatchable top-level field has a declared result
// type, so the nested schema can never silently omit a field the dispatcher
// actually serves. A new resolver added to queryFields/mutationFields without a
// resultTypes entry fails the build here, not later in a stale committed
// artifact.
func init() {
	if _, ok := resultTypes["serverinfo"]; !ok {
		panic("graph: resultTypes missing serverinfo")
	}
	for name := range scopedFields {
		if _, ok := resultTypes[name]; !ok {
			panic("graph: resultTypes missing result type for scoped field: " + name)
		}
	}
	for name := range resultTypes {
		if name == "serverinfo" {
			continue
		}
		if !scopedFields[name] {
			panic("graph: resultTypes lists unknown (non-scoped) field: " + name)
		}
	}
	for name, args := range fieldArguments {
		if _, ok := resultTypes[name]; !ok {
			panic("graph: fieldArguments lists unknown field: " + name)
		}
		checkArgumentNodes(name, args)
	}
}

func checkArgumentNodes(field string, args []NestedArgument) {
	if !slices.IsSortedFunc(args, func(a, b NestedArgument) int {
		return strings.Compare(a.Name, b.Name)
	}) {
		panic("graph: fieldArguments are not sorted for field: " + field)
	}
	for _, arg := range args {
		if !slices.IsSorted(arg.Enum) {
			panic("graph: fieldArguments enum is not sorted for field: " + field)
		}
		if arg.Elem != nil {
			checkArgumentNodes(field, []NestedArgument{*arg.Elem})
		}
		checkArgumentNodes(field, arg.AnyOf)
		checkArgumentNodes(field, arg.Fields)
	}
}

// NestedArgument is one server-owned GraphQL argument declaration for a
// top-level field. Object fields and list/map elements recurse through the same
// node shape; Enum records closed parser vocabularies.
type NestedArgument struct {
	Name     string           `json:"name,omitempty"`
	Kind     string           `json:"kind"`
	Optional bool             `json:"optional,omitempty"`
	Enum     []string         `json:"enum,omitempty"`
	Elem     *NestedArgument  `json:"elem,omitempty"`
	AnyOf    []NestedArgument `json:"anyOf,omitempty"`
	Fields   []NestedArgument `json:"fields,omitempty"`
}

// NestedField is one node in the nested schema tree (H23-B): a JSON field name,
// its exact JSON kind, and its recursively described children or element.
type NestedField = graphshape.Field

// NestedSchemaField is one top-level entry: a dispatchable field with its
// reflected result shape. resultTypes stores the ELEMENT type for list-valued
// fields (e.g. sourceItems → SourceItem), so Kind here describes one item — it
// is "object" (Children is the selection set, sorted by Name) or "scalar" (a
// bare value with no selectable nested fields). Scalar-only object results
// (deleteLibrary → boolean) report "object" with an empty Children set: they
// accept no nested selection. Whether a top-level field returns one item or an
// array is the H23-A manifest/dispatcher's concern; this artifact captures the
// nested SHAPE clients select on.
type NestedSchemaField struct {
	Field     string           `json:"field"`
	Kind      string           `json:"kind"`
	Arguments []NestedArgument `json:"arguments,omitempty"`
	Elem      *NestedField     `json:"elem,omitempty"`
	Children  []NestedField    `json:"children,omitempty"`
}

// NestedSchema returns the reflected nested-field schema for every dispatchable
// top-level field, sorted by field name — the committed source-of-truth artifact
// (H23-B). It reflects over resultTypes (the field→result-struct bridge), so the
// schema can never silently diverge from what execute() actually assigns to
// data[field]: the conformance gate regenerates this and byte-compares against
// the committed JSON.
func NestedSchema() []NestedSchemaField {
	out := make([]NestedSchemaField, 0, len(resultTypes))
	seen := map[string]bool{} // top-level field names already emitted
	for name := range resultTypes {
		if seen[name] {
			continue
		}
		seen[name] = true
		shape := graphshape.Shape(resultTypes[name])
		out = append(out, NestedSchemaField{
			Field:     name,
			Kind:      shape.Kind,
			Arguments: nestedArguments(name),
			Elem:      shape.Elem,
			Children:  shape.Children,
		})
	}
	slices.SortFunc(out, func(a, b NestedSchemaField) int {
		return strings.Compare(a.Field, b.Field)
	})
	return out
}

func nestedArguments(field string) []NestedArgument {
	args := fieldArguments[field]
	if len(args) == 0 {
		return nil
	}
	return slices.Clone(args)
}
