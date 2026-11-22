package graph

import (
	"slices"
	"strings"

	"github.com/beamfall/core/internal/graphshape"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// H23-B: nested-field schema source-of-truth.
//
// The H23-A manifest (SchemaManifest) captures only TOP-LEVEL field names with
// their effect/tier — it says nothing about the SHAPE each field resolves to.
// The depth-1 dispatcher routes solely on those top-level names and never
// inspects nested selection sets, so a client requesting a nonexistent NESTED
// field (the H1 startMillis, an H2 mediaClass)
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
	"navigationmanifest":            NavigationManifestResponse{},
	"manifest":                      NavigationManifestResponse{},
	"plugins":                       PluginInfo{},
	"entity":                        EntityDetailInfo{},
	"entitiesformedia":              RelatedEntityInfo{},
	"entitysearch":                  EntitySearchInfo{},
	"entityrelations":               EntityRelationInfo{},
	"mediaforentity":                SourceItem{},
	"mediafiles":                    MediaFileInfo{},
	"mediatracks":                   MediaTracksInfo{},
	"relatedmedia":                  SourceItem{},
	"mediarelations":                MediaRelationInfo{},
	"mediafields":                   EntityFieldInfo{},
	"artworkcandidates":             ArtworkCandidateInfo{},
	"markers":                       MarkerInfo{},
	"trashedmarkers":                MarkerInfo{},
	"sourceitems":                   SourceItem{},
	"libraryentities":               LibraryEntitiesResponse{},
	"libraryitems":                  LibraryItemsResponse{},
	"libraryfolder":                 LibraryFolderResponse{},
	"ambientqueue":                  SourceItem{},
	"currentsession":                SessionInfo{},
	"profiles":                      ProfileSummary{},
	"search":                        SourceItem{},
	"searchresult":                  SearchResultResponse{},
	"semanticcapability":            SemanticCapabilityInfo{},
	"searchentities":                SourceItem{},
	"searchfeeds":                   FeedSearchResult{},
	"preplayqueue":                  []PrePlayItem{},
	"preplayclips":                  PrePlayClipsInfo{},
	"media":                         MediaInfo{},
	"trashlist":                     TrashEntry{},
	"duplicatecopies":               DuplicateCopySetInfo{},
	"tags":                          TagInfo{},
	"tagassignments":                TagAssignmentInfo{},
	"libraries":                     LibraryInfo{},
	"qualitypolicy":                 QualityPolicyInfo{},
	"accessschedule":                AccessScheduleInfo{},
	"clientprefs":                   ClientPrefInfo{},
	"identifysearch":                IdentifyCandidate{},
	"identifylookup":                IdentifyCandidate{},
	"fixmatchcandidates":            FixMatchCandidateInfo{},
	"fixmatchchildren":              scrape.ChildRow{},
	"fixmatchcurrent":               FixMatchCurrentInfo{},
	"fixmatchlockedfields":          FixMatchLockedFieldInfo{},
	"fixmatchpreview":               FixMatchFieldDiff{},
	"fixmatchsources":               FixMatchSourceInfo{},
	"unmatchedqueue":                UnmatchedItemInfo{},
	"unmatchedrecords":              UnmatchedRecordInfo{},
	"unmatchedsuppressions":         UnmatchedSuppressionInfo{},
	"navoverride":                   NavOverrideInfo{},        // WEB-5J (ADR-0061 §Layout editor): current override blob
	"navoverridehistory":            NavOverrideHistoryInfo{}, // WEB-5J (ADR-0061 §Layout editor): audit-log entry shape
	"subscriptions":                 SubscriptionInfo{},
	"subscriptionentries":           SubscriptionEntryInfo{},
	"followfeed":                    SubscriptionInfo{},
	"setsubscriptiondownloadpolicy": SubscriptionInfo{},
	"jobs":                          JobInfo{},
	"job":                           JobInfo{},
	"jobevents":                     JobEventInfo{},
	"runeventscompaction":           JobInfo{},
	"operationsgovernor":            OperationsGovernorInfo{},
	"schedules":                     ScheduleInfo{},
	"apitokens":                     APITokenInfo{},
	"adminsession":                  AdminSessionInfo{},
	"apikeys":                       ScopedAPIKeyInfo{},
	"activitylog":                   ActivityEntryInfo{},
	"activity":                      ActivityEntryInfo{}, // ATEST-26: singular by-ID lookup
	"myengagement":                  MyEngagementExportInfo{},
	"mydatasummary":                 MyDataSummaryInfo{},
	"myengagementlog":               MyEngagementLogInfo{},
	"mysessions":                    MySessionsInfo{},
	"mutes":                         MyMuteInfo{},
	"engagementcounter":             EngagementCounterResult{},
	"profilesessions":               ProfileSessionInfo{},
	"libraryhealth":                 LibraryHealthInfo{},
	"trashedlibraries":              TrashedLibraryInfo{},
	"webhooks":                      WebhookEndpointInfo{},
	"webhookdeliveries":             WebhookDeliveryInfo{},
	"webhooksettings":               WebhookSettingsInfo{},
	"serverlogs":                    ServerLogsInfo{},
	"agentcatalog":                  AgentCatalogInfo{},
	"diagnosticsbundle":             DiagnosticsBundleInfo{},
	"stagedrestore":                 StagedRestoreInfo{},
	"backupkeystatus":               BackupKeyStatusInfo{},
	"libraryhealthitems":            HealthItemInfo{},
	"libraryduplicategroups":        DuplicateGroupInfo{},
	"metadataexport":                MetadataExportInfo{},
	"libraryindexstatus":            LibraryIndexStatusInfo{},
	"pendingaitagqueue":             PendingAITagInfo{},
	"skipdetectionenabled":          SkipDetectionInfo{},
	"notifications":                 NotificationInfo{},
	"extensionrequest":              ExtensionRequestInfo{},
	"playqueue":                     PlayQueueInfo{},
	"similarmedia":                  SourceItem{},
	"sonicanalysisenabled":          SonicAnalysisInfo{},
	"medialyrics":                   MediaLyricsInfo{},
	"homelayout":                    HomeLayoutInfo{}, // CORE-1972
	"partysession":                  PartySessionInfo{},
	"partystage":                    PartyStageInfo{},
	"myplayhistory":                 PlayInfo{},
	"myplaystats":                   PlayStatsInfo{},
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
	"mediametadata":          MediaMetadataInfo{},
	"consoleconfig":          ConsoleConfigInfo{},
	"updatestatus":           UpdateStatusInfo{},
	"transcodeconfig":        TranscodeConfigInfo{},
	"livesessions":           LiveSessionInfo{},
	"hostmetrics":            HostMetricsInfo{},
	"playhistory":            PlayInfo{},
	"playstats":              PlayStatsInfo{},
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
	"endplayback":                endPlaybackResult{},
	"registerdevicepushtoken":    PushRegistrationResult{},
	"requestextension":           ExtensionRequestInfo{},
	"respondextension":           ExtensionRequestInfo{},
	// ADR-0113 §2.2: per-title access request mutations.
	"requestaccess":        AccessRequestInfo{},
	"resolveaccessrequest": AccessRequestInfo{},
	// ADR-0113 §2.3: per-title access grant mutation (adult revoke).
	"revokeaccessgrant":                  AccessGrantInfo{},
	"setextensionpreauthorization":       SetExtensionPreauthorizationResult{},
	"itemstate":                          ItemStateInfo{},
	"metadatacredits":                    []MetadataAttributionInfo{},
	"setwatched":                         ItemStateInfo{},
	"setwatchlisted":                     ItemStateInfo{},
	"setrating":                          ItemStateInfo{},
	"setcontinuewatchinghidden":          ItemStateInfo{},
	"setfavorite":                        MediaInfo{},
	"partystart":                         PartySessionInfo{},
	"partyend":                           partyBooleanResult{},
	"partyupdate":                        PartySessionInfo{},
	"partyrotatecode":                    PartySessionInfo{},
	"partyadd":                           PartySessionInfo{},
	"partyreview":                        PartySessionInfo{},
	"partymove":                          PartySessionInfo{},
	"partyremove":                        PartySessionInfo{},
	"partyrestore":                       PartySessionInfo{},
	"partyadvance":                       PartySessionInfo{},
	"partyplayback":                      PartySessionInfo{},
	"partyremoveparticipant":             PartySessionInfo{},
	"partyscreenregister":                PartyScreenInfo{},
	"partyscreenreport":                  partyBooleanResult{},
	"partyscreenticker":                  PartySessionInfo{},
	"partyscreendrop":                    PartySessionInfo{},
	"queueaddmany":                       PlayQueueInfo{},
	"queuemoveentry":                     PlayQueueInfo{},
	"queueremoveentry":                   PlayQueueInfo{},
	"queueplaynext":                      PlayQueueInfo{},
	"queuesetcurrent":                    PlayQueueInfo{},
	"queuesetshuffle":                    PlayQueueInfo{},
	"queuesetrepeat":                     PlayQueueInfo{},
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
	"sethomelayout":                      HomeLayoutInfo{}, // CORE-1972
	"resethomelayout":                    HomeLayoutInfo{}, // CORE-1972
	"setfollow":                          FollowInfo{},
	"refreshsubscription":                SubscriptionRefreshInfo{},
	"refreshallsubscriptions":            SubscriptionRefreshAllInfo{},
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
	"editmediametadata":                  MediaMetadataInfo{},
	"refreshmediametadata":               MediaMetadataInfo{},
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
	"setmediarelation":                   RelationWriteResult{},
	"removemediarelation":                RelationWriteResult{},
	"linkmediarelation":                  RelationWriteResult{},
	"undobatch":                          BatchResultInfo{},
	"identifyapply":                      MediaInfo{},
	"fixmatch":                           MediaInfo{},
	"clearmediaordinal":                  false,
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
	"reorderprofiles":                    ProfileSummary{},
	"setprofileavatar":                   ProfileSummary{},
	"setprofilelibraries":                ProfileInfo{},
	"setcertificationceiling":            ProfileInfo{},
	"setaccessschedule":                  AccessScheduleInfo{},
	"createlibrary":                      LibraryInfo{},
	"setlibraryroots":                    LibraryInfo{},
	"addpathpickerroot":                  PathPickerRootInfo{},
	"setlibrarypreviewclips":             LibraryInfo{},
	"setlibraryshowonhome":               LibraryInfo{},
	"setlibrarybacking":                  LibraryInfo{},
	"setlibraryrestricted":               LibraryInfo{},
	"setqualitypolicy":                   QualityPolicyInfo{},
	"deletelibrary":                      deleteLibraryResult{},
	"restorelibrary":                     restoreLibraryResult{},
	"purgelibrary":                       LibraryPurgeResult{},
	"savewebhook":                        WebhookEndpointInfo{},
	"deletewebhook":                      deleteWebhookResult{},
	"inspectbackup":                      BackupContentsInfo{},
	"stagerestore":                       StagedRestoreInfo{},
	"cancelstagedrestore":                cancelStagedRestoreResult{},
	"revealbackupkey":                    RevealedBackupKeyInfo{},
	"acknowledgebackupkey":               BackupKeyStatusInfo{},
	"testwebhook":                        WebhookTestResultInfo{},
	"setwebhooksettings":                 WebhookSettingsInfo{},
	"scanlibrary":                        JobInfo{},
	"refreshlibrarymetadata":             JobInfo{},
	"scanpreview":                        ScanPreviewInfo{},
	"commitscanpreview":                  JobInfo{},
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
	"setremoteaccessportmapping":         RemoteAccessInfo{},
	"checkremoteaccessreachability":      RemoteAccessInfo{},
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
	"setsonicanalysisenabled":   SonicAnalysisInfo{},
	"setpreplayclips":           PrePlayClipsInfo{},
	"offlinepacks":              []OfflinePackInfo{},
	"createofflinepack":         OfflinePackInfo{},
	"revokeofflinepack":         OfflinePackInfo{},
	"retryofflinepack":          OfflinePackInfo{},
	"syncofflinepackprogress":   OfflinePackInfo{},
	"deletemypacktokens":        deleteMyPackTokensResult{},
}

var fieldArguments = map[string][]NestedArgument{
	"clearmediaordinal": {{Name: "mediaID", Kind: "string"}},
	"ambientqueue":      sourceArguments(),
	"entitiesformedia":  {{Name: "mediaID", Kind: "string"}, {Name: "relation", Kind: "string", Optional: true}},
	"entitysearch": {
		{Name: "kind", Kind: "string"},
		{Name: "limit", Kind: "int", Optional: true},
		{Name: "query", Kind: "string"},
	},
	"setmediarelation": {
		{Name: "attributes", Kind: "map", Optional: true, Elem: &NestedArgument{Kind: "string"}},
		{Name: "entityID", Kind: "string", Optional: true},
		{Name: "mediaID", Kind: "string"},
		{Name: "newEntity", Kind: "object", Optional: true, Fields: []NestedArgument{
			{Name: "kind", Kind: "string"}, {Name: "name", Kind: "string"},
		}},
		{Name: "relation", Kind: "string"},
		{Name: "sortOrder", Kind: "int", Optional: true},
	},
	"removemediarelation": {
		{Name: "entityID", Kind: "string"},
		{Name: "mediaID", Kind: "string"},
		{Name: "relation", Kind: "string"},
	},
	"entity":          {{Name: "id", Kind: "string"}},
	"entityrelations": {{Name: "entityID", Kind: "string"}, {Name: "relation", Kind: "string", Optional: true}},
	"fixmatchchildren": {
		{Name: "candidateID", Kind: "string"},
		{Name: "level", Kind: "int"},
		{Name: "mediaID", Kind: "string"},
		{Name: "parentKey", Kind: "string", Optional: true},
		{Name: "pluginID", Kind: "string"},
		{Name: "scraperID", Kind: "string"},
	},
	"fixmatch": {
		{Name: "candidateID", Kind: "string", Optional: true},
		{Name: "clearLocks", Kind: "list", Optional: true, Elem: &NestedArgument{Kind: "string"}},
		{Name: "importRunID", Kind: "string", Optional: true},
		{Name: "index", Kind: "int", Optional: true},
		{Name: "mediaID", Kind: "string"},
		{Name: "pluginID", Kind: "string", Optional: true},
		{Name: "recordID", Kind: "string", Optional: true},
		{Name: "scraperID", Kind: "string", Optional: true},
		{Name: "sequence", Kind: "string", Optional: true},
	},
	"fixmatchpreview": {
		{Name: "candidateID", Kind: "string"},
		{Name: "clearLocks", Kind: "list", Optional: true, Elem: &NestedArgument{Kind: "string"}},
		{Name: "index", Kind: "int", Optional: true},
		{Name: "mediaID", Kind: "string"},
		{Name: "pluginID", Kind: "string"},
		{Name: "scraperID", Kind: "string"},
		{Name: "sequence", Kind: "string", Optional: true},
	},
	"diagnosticsbundle": diagnosticsBundleArguments(),
	"importstatus":      {{Name: "jobID", Kind: "string"}},
	"itemstate":         {{Name: "entityID", Kind: "string", Optional: true}, {Name: "mediaID", Kind: "string", Optional: true}},
	"libraryentities":   libraryEntitiesArguments(),
	"libraryitems":      append([]NestedArgument{{Name: "categoryCounts", Kind: "boolean", Optional: true}}, libraryItemsArguments()...),
	"media":             {{Name: "id", Kind: "string"}},
	"mediafields":       {{Name: "mediaID", Kind: "string"}},
	"mediafiles":        {{Name: "mediaID", Kind: "string"}},
	"mediatracks":       {{Name: "mediaID", Kind: "string"}},
	"medialyrics":       {{Name: "mediaID", Kind: "string"}},
	"relatedmedia":      {{Name: "limit", Kind: "int", Optional: true}, {Name: "mediaID", Kind: "string"}},
	"similarmedia":      {{Name: "limit", Kind: "int", Optional: true}, {Name: "mediaID", Kind: "string"}},
	"mediaforentity":    {{Name: "entityID", Kind: "string"}, {Name: "rollUp", Kind: "boolean", Optional: true}},
	"mediarelations":    {{Name: "mediaID", Kind: "string"}, {Name: "relation", Kind: "string"}},
	"search":            {{Name: "query", Kind: "string", Optional: true}},
	"serverlogs":        serverLogsArguments(),
	"searchentities":    {{Name: "query", Kind: "string", Optional: true}},
	"searchfeeds":       {{Name: "query", Kind: "string"}},
	"preplayqueue":      {{Name: "mediaID", Kind: "string"}},
	"searchresult":      {{Name: "limit", Kind: "int", Optional: true}, searchModeArgument(), {Name: "offset", Kind: "int", Optional: true}, {Name: "query", Kind: "string", Optional: true}},
	"partystage": {
		{Name: "screenID", Kind: "string", Optional: true},
		{Name: "sessionID", Kind: "string"},
	},
	"partystart": {
		{Name: "autoAdd", Kind: "boolean", Optional: true},
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "fairness", Kind: "string", Optional: true, Enum: []string{"fifo", "round-robin"}},
		{Name: "libraryIDs", Kind: "list", Elem: &NestedArgument{Kind: "string"}},
	},
	"partyend": {
		{Name: "expectedProfileID", Kind: "string", Optional: true},
	},
	"partyupdate": {
		{Name: "autoAdd", Kind: "boolean", Optional: true},
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "fairness", Kind: "string", Optional: true, Enum: []string{"fifo", "round-robin"}},
		{Name: "joinOpen", Kind: "boolean", Optional: true},
	},
	"partyrotatecode": {
		{Name: "expectedProfileID", Kind: "string", Optional: true},
	},
	"partyadd": {
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "mediaID", Kind: "string"},
		{Name: "participantID", Kind: "string", Optional: true},
		{Name: "participantName", Kind: "string", Optional: true},
	},
	"partyreview": {
		{Name: "approve", Kind: "boolean"},
		{Name: "entryID", Kind: "string"},
		{Name: "expectedProfileID", Kind: "string", Optional: true},
	},
	"partymove": {
		{Name: "entryID", Kind: "string"},
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "position", Kind: "number"},
	},
	"partyremove": {
		{Name: "entryID", Kind: "string"},
		{Name: "expectedProfileID", Kind: "string", Optional: true},
	},
	"partyrestore": {
		{Name: "entryID", Kind: "string"},
		{Name: "expectedProfileID", Kind: "string", Optional: true},
	},
	"partyadvance": {
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "fromEntryID", Kind: "string", Optional: true},
	},
	"partyplayback": {
		{Name: "entryID", Kind: "string"},
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "pitchSemitones", Kind: "number", Optional: true},
		{Name: "positionMS", Kind: "number"},
		{Name: "state", Kind: "string", Enum: []string{"paused", "playing"}},
		{Name: "tempo", Kind: "number", Optional: true},
	},
	"partyremoveparticipant": {
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "participantID", Kind: "string"},
	},
	"partyscreenregister": {
		{Name: "carrier", Kind: "string", Enum: []string{"local", "relay"}},
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "kind", Kind: "string", Enum: []string{"joined", "popout"}},
		{Name: "name", Kind: "string"},
		{Name: "sessionID", Kind: "string"},
	},
	"partyscreenreport": {
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "offsetMS", Kind: "number"},
		{Name: "rttMS", Kind: "number"},
		{Name: "screenID", Kind: "string"},
	},
	"partyscreenticker": {
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "screenID", Kind: "string"},
		{Name: "ticker", Kind: "boolean"},
	},
	"partyscreendrop": {
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "screenID", Kind: "string"},
	},
	"queueaddmany": {
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "mediaIDs", Kind: "list", Elem: &NestedArgument{Kind: "string"}},
		{Name: "placement", Kind: "string", Enum: []string{"end", "next"}},
	},
	"queuemoveentry": {
		{Name: "entryID", Kind: "string"},
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "position", Kind: "number"},
	},
	"queueremoveentry": {
		{Name: "entryID", Kind: "string"},
		{Name: "expectedProfileID", Kind: "string", Optional: true},
	},
	"queueplaynext": {
		{Name: "entryID", Kind: "string"},
		{Name: "expectedProfileID", Kind: "string", Optional: true},
	},
	"queuesetcurrent": {
		{Name: "entryID", Kind: "string", Optional: true},
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "positionMS", Kind: "number"},
	},
	"queuesetshuffle": {
		{Name: "enabled", Kind: "boolean"},
		{Name: "expectedProfileID", Kind: "string", Optional: true},
	},
	"queuesetrepeat": {
		{Name: "expectedProfileID", Kind: "string", Optional: true},
		{Name: "mode", Kind: "string", Enum: []string{"all", "off", "one"}},
	},
	"sourceitems": sourceArguments(),
}

func serverLogsArguments() []NestedArgument {
	return []NestedArgument{
		{Name: "contains", Kind: "string", Optional: true},
		{Name: "level", Kind: "string", Optional: true, Enum: []string{"DEBUG", "ERROR", "INFO", "WARN"}},
		{Name: "limit", Kind: "number", Optional: true},
		{Name: "requestId", Kind: "string", Optional: true},
	}
}

func diagnosticsBundleArguments() []NestedArgument {
	args := serverLogsArguments()
	profiles := NestedArgument{Name: "profiles", Kind: "list", Optional: true, Elem: &NestedArgument{Kind: "string", Enum: diagnosticsProfiles}}
	return slices.Insert(args, len(args)-1, profiles) // keep names sorted: profiles sorts before requestId
}

func searchModeArgument() NestedArgument {
	return NestedArgument{Name: "mode", Kind: "string", Optional: true, Enum: []string{
		string(SearchModeAuto),
		string(SearchModeKeyword),
		string(SearchModeRecommend),
		string(SearchModeSemantic),
	}}
}

func libraryItemsArguments() []NestedArgument {
	return []NestedArgument{
		{Name: "direction", Kind: "string", Optional: true, Enum: []string{"asc", "desc"}},
		{Name: "groupFilters", Kind: "map", Optional: true, Elem: &NestedArgument{Kind: "string"}},
		{Name: "libraryID", Kind: "string"},
		{Name: "limit", Kind: "int", Optional: true},
		{Name: "minRating", Kind: "int", Optional: true},
		{Name: "offset", Kind: "int", Optional: true},
		{Name: "quality", Kind: "string", Optional: true, Enum: []string{
			store.QualityTierHD1080,
			store.QualityTierHD720,
			store.QualityTierSD,
			store.QualityTierUHD,
		}},
		{Name: "query", Kind: "string", Optional: true},
		{Name: "seed", Kind: "string", Optional: true},
		{Name: "sort", Kind: "string", Optional: true, Enum: []string{
			store.LibraryBrowseSortAdded,
			store.LibraryBrowseSortAudienceScore,
			store.LibraryBrowseSortDuration,
			store.LibraryBrowseSortLastWatched,
			store.LibraryBrowseSortPlays,
			store.LibraryBrowseSortRandom,
			store.LibraryBrowseSortRating,
			store.LibraryBrowseSortReleaseDate,
			store.LibraryBrowseSortTitle,
			store.LibraryBrowseSortYear,
		}},
		{Name: "watch", Kind: "string", Optional: true, Enum: []string{
			store.LibraryBrowseWatchInProgress,
			store.LibraryBrowseWatchUnwatched,
			store.LibraryBrowseWatchWatched,
		}},
		{Name: "yearFrom", Kind: "int", Optional: true},
		{Name: "yearTo", Kind: "int", Optional: true},
	}
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
		// When non-empty, fold edition groups to one visible representative and
		// exclude the context Media's group before paging an entity-media rail.
		// Plain entity-media reads preserve Entity contents when omitted or empty.
		{Name: "excludeEditionGroupOf", Kind: "string", Optional: true},
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

// restoreLibraryResult models the boolean-leaf Library restore mutation
// (CORE-1201): execute() assigns data["restoreLibrary"] = true, so like
// deleteLibraryResult it carries no nested fields.
type restoreLibraryResult struct{}

// deleteWebhookResult models the boolean-leaf webhook removal (CORE-1697).
type deleteWebhookResult struct{}

// endPlaybackResult models the boolean-leaf playback end (CORE-2305).
type endPlaybackResult struct{}

// cancelStagedRestoreResult models the boolean-leaf staged-restore removal
// (CORE-1720).
type cancelStagedRestoreResult struct{}

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
