package graph

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// originalScopedFields is the frozen baseline: the set of scoped top-level
// fields that the single scopedFields map held before the query/mutation split,
// plus the ADR-0041 Enrichment + semantic-search fields (#62/#106/#107) and the
// ADR-0036 §2.2 My-Data Summary + Event-log reads (mydatasummary,
// myengagementlog), and the ADR-0035 §3 Ambient Queue read (ambientqueue), and
// the ADR-0035 §7 cross-device session control fields (profilesessions read,
// controlsession transport mutation).
// queryFields ∪ mutationFields MUST equal this set, and the two halves MUST be
// disjoint. Hardcoding the baseline here turns a future accidental drop or add
// (e.g. a new mutation added to the dispatch but forgotten in mutationFields)
// into a failing test rather than a silent gap in the auth gate or the
// action-conformance gate.
var originalScopedFields = []string{
	"acceptpendingaitag",
	"accessschedule",
	"activitylog",
	"addpathpickerroot", // WEB-7: server-side path picker root mutation
	"addsubscription",
	"adminsession",
	"ambientqueue",
	"associatesubscriptionentry",
	"followfeed",
	"refreshsubscription",
	"setsubscriptiondownloadpolicy",
	"subscriptionentries",
	"subscriptionfollow", // CORE-2959: viewer-safe follow state and route
	"apikeys",
	"apitokens",
	"artworkcandidates",
	"backup",
	"backuphistory",
	"browseserverfolders",
	"bulkeditapply",
	"bulkeditpreview",
	"bulkfetchmissingartwork",
	"bulkfetchmissingartworkselected",
	"bulkmergeduplicates",
	"bulkmergeduplicatesselected",
	"playbackrenditioncache",
	"precomputeplaybackrenditions",
	"evictplaybackrenditioncache",
	"bulkrescanunmatched",
	"bulkrescanunmatchedselected",
	"bulkrescrapescrapefailures",
	"bulkrescrapescrapefailuresselected",
	"bulktrashbroken",
	"bulktrashbrokenselected",
	"bulkassigntags",
	"clientprefs",
	"collections",
	"connectionendpoints", // RELREV-8-CORE: authenticated secret-free endpoint discovery
	"consoleconfig",
	"capabilitystates",           // POWERTOOLS-9 (ADR-0124 §5): capability runtime-state read
	"setcapabilityenabled",       // POWERTOOLS-9 (ADR-0124 §5): audited Workflow-capability toggle
	"controlrenderersession",     // CORE-3309 (ADR-0151 D1-D3, ADR-0240 D3): revision-checked renderer session control
	"controlsession",             // AV-3 (ADR-0035 §7): cross-device transport mutation
	"formgroup",                  // AV-4 (ADR-0035 §8): Ambient Group Conductor — form group
	"joingroup",                  // AV-4 (ADR-0035 §8): join group with calibrated offset+latency
	"leavegroup",                 // AV-4 (ADR-0035 §8): leave group (dissolves on last member)
	"transitiongroup",            // AV-4 (ADR-0035 §8): broadcast {item, position, present_at}
	"reportdrift",                // AV-4 (ADR-0035 §8): drift report → ±0.1% nudge / >500ms re-sync
	"createwatchparty",           // AV-10/11 (ADR-0038): form a Watch Party (host role, full-AV)
	"invitetowatchparty",         // AV-11 (ADR-0038 §2.5): invite a Profile by display name
	"acceptwatchparty",           // AV-11 (ADR-0038 §2.3/§2.5): accept + Reveal-intersection check
	"watchpartytransport",        // AV-10 (ADR-0038 §2.4): host authoritative play/pause/seek/skip
	"requestwatchpartytransport", // AV-10 (ADR-0038 §2.4): non-host seek request (relaxed auto-approve)
	"watchpartyautoskip",         // PANEL-14 (ADR-0038 §2.8): member auto-skip degraded to host authority
	"leavewatchparty",            // AV-16 (ADR-0111 §4): cross-Profile leave / revocation propagation
	"createapikey",
	"createapitoken",
	"createtag",
	"createmanagedfolder",  // WEB-5C (ADR-0061): manager-scoped managed folder create
	"registerimportsource", // POWERTOOLS-6B (apple ADR-0003 §3): admin-gated import-source token mint
	"importmanagedfile",    // POWERTOOLS-6B (apple ADR-0003 §1): admin-gated native→server managed import
	"createcollection",
	"createsavedfilter",
	"createsmartcollection",
	"createchannel",
	"updatechannel",
	"deletecollection",
	"deletesavedfilter",
	"deletesmartcollection",
	"deletechannel",
	"createlibrary",
	"createmarker",
	"createprofile",
	"currentsession",
	"playerbinding",
	"playercontrollers",
	"playerdisplays",
	"playerprofiles",
	"setdeviceplayerbinding",
	"cleardeviceplayerbinding",
	"setplayerwalkup",
	"setplayercontrollers",
	"deletelibrary",
	"trashedlibraries",     // CORE-1201: deleted Libraries that can still be restored
	"restorelibrary",       // CORE-1201: bring a deleted Library back
	"purgelibrary",         // CORE-1201: permanently remove a deleted Library
	"webhooks",             // CORE-1697: console-managed webhook endpoints
	"webhookdeliveries",    // CORE-1697: webhook delivery log
	"webhooksettings",      // CORE-1697: shared webhook options
	"savewebhook",          // CORE-1697: add or edit a webhook endpoint
	"deletewebhook",        // CORE-1697: remove a webhook endpoint
	"testwebhook",          // CORE-1697: send one test notification
	"setwebhooksettings",   // CORE-1697: save shared webhook options
	"serverlogs",           // CORE-1716: recent redacted server log lines
	"agentcatalog",         // CORE-2096: self-describing field catalog for agents
	"diagnosticsbundle",    // CORE-2097: runtime diagnostics and text profiles
	"stagedrestore",        // CORE-1720: the restore staged for the next start
	"inspectbackup",        // CORE-1720: decrypt a backup set and report its contents
	"stagerestore",         // CORE-1720: stage a restore for the next start
	"cancelstagedrestore",  // CORE-1720: drop a staged restore
	"backupkeystatus",      // CORE-2032: whether the backup key still has to be saved
	"revealbackupkey",      // CORE-2032: reveal the backup key to save
	"acknowledgebackupkey", // CORE-2032: confirm the backup key is saved
	"deletemanagedpath",    // WEB-5C (ADR-0061): manager-scoped recoverable managed file/folder delete
	"deletemarker",
	"deletetag",
	"deletemyengagement",
	"endadminsession",
	"entitiesformedia",
	"entity",
	"entityrelations",              // WEB-5B: generic Entity relation/order read
	"entitysearch",                 // CORE-3131: Assign relation-target search
	"extensionrequest",             // PUSH-2 (ADR-0039 §2.3): read one extension request by id
	"requestextension",             // PUSH-2 (ADR-0039 §2.1): managed Profile raises a request
	"respondextension",             // PUSH-2 (ADR-0039 §2.3): adult approves/denies, first-responder-wins
	"setextensionpreauthorization", // PUSH-2 (ADR-0039 §2.4): per-managed-Profile pre-auth toggle (Console)
	"identifyapply",
	"identifysearch",
	"identifylookup",
	"unmatchedrecords",
	"unmatchedsuppressions",
	"createmediafromunmatched",
	"suppressunmatched",
	"resetunmatchedsuppression",
	"fixmatchcandidates",
	"fixmatchchildren",
	"fixmatchcurrent",
	"fixmatchlockedfields",
	"fixmatchpreview",
	"fixmatchsources",
	"clearmediaordinal",
	"fixmatch",           // PIPE-1 (ADR-0003 §4): re-identify + pin
	"foldertree",         // WEB-5C (ADR-0061): manager-scoped bounded folder/file tree read
	"unmatchedqueue",     // PIPE-1 (ADR-0003 §4): Unmatched-queue read
	"dismissunmatched",   // PIPE-1/WEB-5G (ADR-0003 §4): dismiss an Unmatched-queue entry
	"addmediaexternalid", // PIPE-1/WEB-5G (ADR-0003 §4): attach a manual external ID
	"navoverride",        // WEB-5J (ADR-0061 §Layout editor): current nav/layout override blob
	"navoverridehistory", // WEB-5J (ADR-0061 §Layout editor): audit log of prior overrides
	"setnavoverride",     // WEB-5J (ADR-0061 §Layout editor): apply partial override patch
	"resetnavoverride",   // WEB-5J (ADR-0061 §Layout editor): clear all overrides
	"restorenavoverride", // WEB-5J (ADR-0061 §Layout editor): restore a history entry
	"importers",
	"importstatus",
	"job",
	"jobcancel",
	"jobevents",
	"jobretry",
	"jobs",
	"libraries",
	"libraryduplicategroups",
	"libraryfolder", // CORE-2359: Library folder view read
	"libraryhealth",
	"libraryhealthitems",
	"libraryindexstatus",
	"libraryentities", // CORE-2362: a Library's Entities by browse kind
	"libraryitems",    // CORE-1190: full library browse read
	"livesessions",
	"hostmetrics",
	"playhistory",
	"playstats",
	"devices",
	"manifest",
	"markers",
	"marknotificationread",
	"media",
	"mediafieldlocks",
	"mediafields",
	"mediametadata", // CORE-1715: per-item metadata editor read
	"mediafiles",    // WEB-5B: Versions/Parts file-plane read
	"mediaforentity",
	"mediarelations",          // WEB-5B: generic Media relation read
	"mediatracks",             // CORE-2442: Versions and tracks before playback
	"relatedmedia",            // CORE-2444: ranked Media sharing credits, discovery-suppressed
	"similarmedia",            // CORE-3074: sonically similar Media, discovery-suppressed
	"sonicanalysisenabled",    // CORE-3073: Admin read of the per-Library sonic toggle
	"setsonicanalysisenabled", // CORE-3073: Admin write of the per-Library sonic toggle
	"medialyrics",             // CORE-1638: lyrics of an audio item
	"mergeentities",
	"mergemedia",
	"metadataexport",
	"movemanagedpath", // WEB-5C (ADR-0061): manager-scoped managed file/folder move
	"batchrenamemanagedpaths",
	"myengagement",
	"mydatasummary",
	"myengagementlog",
	"myplayhistory", // CORE-1703: the caller's own plays
	"myplaystats",   // CORE-1703: the caller's own play stats
	"mysessions",
	"offlinepacks",            // MUSIC-6 (ADR-0098 area 6): own Offline Packs + fetch tokens
	"createofflinepack",       // MUSIC-6 (ADR-0098 area 6): build a pack from an opaque target
	"revokeofflinepack",       // MUSIC-6 (ADR-0098 area 6): revoke an owned pack
	"retryofflinepack",        // CORE-1743: retry an owned failed background pack build
	"syncofflinepackprogress", // APPLE-6 (ADR-0098 area 6): sync own Offline Pack byte progress
	"deletemypacktokens",      // MUSIC-6 (ADR-0036): clear own pack-token ledger rows
	"mutes",                   // ENR-4 (ADR-0054 §2.8): own self-authored mute list read
	"setmute",                 // ENR-4 (ADR-0054 §2.8): set "Not interested" (own Profile)
	"clearmute",               // ENR-4 (ADR-0054 §2.8): unmute one target (own Profile)
	"clearallmutes",           // ENR-4 (ADR-0054 §2.8): My-Data clear-all reset (own Profile)
	"disablescrapercredential",
	"engagementcounter",    // ADULT-1 (ADR-0027 D1): own per-Profile engagement counter read
	"recordengagement",     // ADULT-1 (ADR-0027 D1): increment a per-Profile engagement counter
	"removelastengagement", // ADULT-1 (ADR-0027 D1): remove-last correction (own Profile)
	"renamemanagedpath",    // WEB-5C (ADR-0061): manager-scoped managed file/folder rename
	"navigationmanifest",
	"notifications",
	"operationsgovernor",
	"operatordeleteprofiledata",
	"organizeapply",
	"organizepreview",
	"pendingaitagqueue",
	"pathpicker", // WEB-7: server-side path picker read
	"pinartworkcandidate",
	"plugins",
	"profiles",
	"profilesessions", // AV-3 (ADR-0035 §7): Profile-scoped active-session enumeration
	"purgetrash",
	"qualitypolicy",
	"qbittorrentingest",
	"setqbittorrentingest", // FEAT-10 (ADR-0020 §6): runtime ingest reconfigure
	"jellyfincompat",       // CORE-2683 (ADR-0171 §2.2-§2.4): compatibility Console settings
	"setjellyfincompat",
	"setprofileintegrationsignin",
	"jellyfincompatkeys", // CORE-2725 (ADR-0171 §2.4): durable integration keys
	"createjellyfincompatkey",
	"revokejellyfincompatkey",
	"encryptedlibrarystatus",    // ENC-2 (ADR-0045 §2.4): credential-free Sealed/Unsealed + mode read
	"provisionencryptedlibrary", // ENC-2 (ADR-0045 §2.4): mint DEK + three wraps, Recovery Key once
	"unsealencryptedlibrary",    // ENC-2 (ADR-0045 §2.4): passphrase/Recovery-Key unseal
	"sealencryptedlibrary",      // ENC-2 (ADR-0045 §2.4): drop in-memory DEK
	"rewrapencryptedlibrary",    // ENC-7 (ADR-0117): rotate DEK + Recovery Key after compromise
	"partysession",
	"partystage",
	"partystart",
	"partyend",
	"partyupdate",
	"partyrotatecode",
	"partyadd",
	"partyreview",
	"partymove",
	"partyremove",
	"partyrestore",
	"partyskip",
	"partyadvance",
	"partyplayback",
	"partyremoveparticipant",
	"partyscreenregister",
	"partyscreenreport",
	"partyscreenticker",
	"partyscreendisplay",
	"partyscreendrop",
	"partyscreenadddisplay",
	"partyscreenlead",
	"playqueue",
	"queueadd",
	"queueaddmany",
	"queuemoveentry",
	"queueremoveentry",
	"queueplaynext",
	"queuesetcurrent",
	"queuesetshuffle",
	"queuesetrepeat",
	"queueclear",
	"queuemove",
	"queueremove",
	"rejectpendingaitag",
	"relock",
	"remoteaccess",
	"activity",                  // ATEST-26 (ADR-0012 §11): singular by-ID activity entry for E2E audit assertions
	"retentionrules",            // RET-2 (ADR-0051): Console-authored leaving-soon rules (Admin-gated)
	"createretentionrule",       // RET-2 (ADR-0051): mint rule + leaving-soon Smart Collection
	"updateretentionrule",       // RET-2 (ADR-0051): partial patch of an existing rule
	"deleteretentionrule",       // RET-2 (ADR-0051): remove rule + its Smart Collection
	"setretentioncandidatehold", // RET-2 (ADR-0051): viewer-writable keep flag (manager-tier)
	"removecollectionmedia",
	"removesubscription",
	"refreshsubscription",     // CORE-2955 (ADR-0238 §2.2): manual feed refresh, rate-limited
	"refreshallsubscriptions", // CORE-2955 (ADR-0238 §2.2): Manager Refresh all
	"reordercollectionmedia",
	"savedfilters",
	"scanestimate", // OPS-4 (ADR-0013 §2): first-scan wizard estimate read
	"smartcollections",
	"channels",                // CHAN-1 (ADR-0158): Library Channels read
	"registerdevicepushtoken", // PUSH-1 (ADR-0039 §2.5): device push-token registration
	"reportplayback",
	"replayofflineengagement",
	"endplayback",        // CORE-2305: a closing player ends its live session
	"restoremanagedpath", // WEB-5C (ADR-0061): manager-scoped managed file/folder restore
	"restorecollection",
	"restoremedia",
	"restoremarker",
	"restoretag",
	"reveal",
	"revokeapikey",
	"revokeapitoken",
	"revokedevice",
	"revokealldevices",
	"runimport",
	"runjob",
	"runeventscompaction",
	"scanlibrary",
	"scanpreview",       // CORE-1054: rescan preview with a single-use token
	"commitscanpreview", // CORE-1054: token-gated rescan commit
	"schedules",
	"search",
	"searchentities",
	"searchfeeds",
	"searchresult",
	"semanticcapability",
	"setaccessschedule",
	"setcertificationceiling",
	"setdevicerole",
	"linkmediarelation",      // WEB-5B: generic Media relation write
	"linkscrobble",           // SCROBBLE-1 (ADR-0049 §2.2): link own Profile to a scrobble service
	"unlinkscrobble",         // SCROBBLE-1 (ADR-0049 §2.9): unlink own Profile from a scrobble service
	"setscrobbleeligibility", // SCROBBLE-1 (ADR-0049 §2.3): per-Library scrobble opt-in (Console)
	"scrobblelinks",          // MUSIC-7 (ADR-0098 area 7): own Profile's scrobble links (read)
	"scrobbleeligibility",    // MUSIC-7 (ADR-0098 area 7): per-visible-Library eligibility (read)
	"scrobblestatus",         // MUSIC-7 (ADR-0098 area 7): admin diagnostic status (read)
	"setclientpref",
	"setconsoleconfig",
	"setscrapercredential",
	"setfavorite",
	"itemstate",                 // CORE-1194: per-Profile item state read
	"metadatacredits",           // CORE-1126: active plugin scrapers' declared attribution
	"setwatched",                // CORE-1194: mark a Media or Entity tree watched/unwatched
	"setwatchlisted",            // CORE-1194: per-Profile watchlist add/remove
	"setrating",                 // CORE-1194: personal rating set/clear
	"setcontinuewatchinghidden", // CORE-2039: remove from / restore to Continue Watching, keeping progress
	"setfollow",
	"setfollownewentryroute", // CORE-2958: per-follow new-entry route (own Profile)
	"setlibraryaiindexing",
	"skipdetectionenabled",    // ENR-7 (ADR-0048 Decision 3/6): per-Library skip-detection toggle (read)
	"setskipdetectionenabled", // ENR-7 (ADR-0048 Decision 3/6): per-Library skip-detection toggle (write)
	"preplayqueue",            // CORE-1701: items to play before a feature
	"preplayclips",            // CORE-1701: the Owner's pre-play clip list (read)
	"setpreplayclips",         // CORE-1701: the Owner's pre-play clip list (write)
	"setlibrarybacking",
	"setlibrarypreviewclips",
	"setlibraryshowonhome",
	"setlibraryrestricted",
	"setlibraryroots",
	"setmediafieldlock",
	"setmediarelation",       // CORE-3131: operator relation write, locked against rehydration
	"removemediarelation",    // CORE-3131: operator relation removal
	"editmediametadata",      // CORE-1715: per-item manual metadata edit (locks edited fields)
	"refreshmediametadata",   // CORE-1715: per-item provider refresh honouring locks
	"refreshlibrarymetadata", // CORE-1136: Library-wide metadata refresh job
	"setpluginactive",
	"setprofilelibraries",
	"uninstallplugindata",
	"setpluginconfig",
	// ADR-0058 third-party Integration Worker Plugin Surface: the
	// server-declared worker read + the grant-approval / update-source /
	// lifecycle-action mutations.
	"pluginworker",
	"approvepluginpermission",
	"setpluginupdatesource",
	"runpluginlifecycleaction",
	"setqualitypolicy",
	"setrevealpin",
	"setsubscriptionretention",
	"settranscodeconfig",
	"setremoteaccessportmapping",
	"checkremoteaccessreachability",
	"setupstate",
	"sourceitems",
	"stationdelete",   // MUSIC-5 (ADR-0098 area 5)
	"stationgenerate", // MUSIC-5 (ADR-0098 area 5)
	"stations",        // MUSIC-5 (ADR-0098 area 5)
	"stationsave",     // MUSIC-5 (ADR-0098 area 5)
	"stationstart",    // MUSIC-5 (ADR-0098 area 5)
	"subscriptions",
	"switchprofile",
	"tagassignments",
	"tags",
	"terminatesession",
	"trashedmarkers",
	"transcodeconfig",
	"transferprofileowner",
	"trashlist",
	"duplicatecopies",
	"trashmedia",
	"undobatch",
	"updatecollection",
	"updatemarker",
	"updateprofile",
	"reorderprofiles",
	"setprofileavatar",
	"updatetag",
	"updateschedule",
	"updatesmartcollection",
	"updatestatus", // OPS-6 (ADR-0013 §7): opt-in version-check status read
	"upsertentityrelation",
	"validatescrapercredential",
	"verifyadminsession",
	"verifybackup",
	"watcherstatus",
	"assigntag",
	"unassigntag",
	"addcollectionmedia",
	// ADR-0113 §2.2: per-title access request (managed "Ask a parent" flow).
	"accessrequest",         // read one request by id (requester or any adult)
	"pendingaccessrequests", // adult inbox or managed self-view
	"requestaccess",         // managed Profile raises a request (mutation)
	"resolveaccessrequest",  // adult resolves (grant/deny), first-responder-wins (mutation)
	// ADR-0113 §2.3: per-title access grant (adult exception list + revoke).
	"accessgrantsforprofile", // adult or managed Profile lists active grants
	"revokeaccessgrant",      // adult revokes a grant (mutation)
	"homelayout",             // CORE-1972: read the calling Profile's Home layout
	"sethomelayout",          // CORE-1972: save the calling Profile's Home order and hidden shelves
	"resethomelayout",        // CORE-1972: return the calling Profile's Home to the default
}

// TestActionRegistryMatchesDispatch is the self-check for the registry split:
// queryFields and mutationFields must be disjoint, their union must equal the
// frozen scopedFields baseline, and the runtime-assembled scopedFields must
// match too. It also parses graph_dispatch.go for the fields["..."] dispatch keys and
// asserts every mutationFields key is actually dispatched — so a mutation can
// never sit in the registry without a resolver wired in execute().
func TestActionRegistryMatchesDispatch(t *testing.T) {
	baseline := make(map[string]bool, len(originalScopedFields))
	for _, name := range originalScopedFields {
		baseline[name] = true
	}
	for name := range suppressedDispatchFields {
		delete(baseline, name)
	}

	// queryFields and mutationFields are disjoint.
	for name := range queryFields {
		if mutationFields[name] {
			t.Errorf("field %q is in both queryFields and mutationFields", name)
		}
	}

	// Union(queryFields, mutationFields) == baseline.
	union := make(map[string]bool, len(queryFields)+len(mutationFields))
	for name := range queryFields {
		union[name] = true
	}
	for name := range mutationFields {
		union[name] = true
	}
	for name := range union {
		if !baseline[name] {
			t.Errorf("field %q is registered but not in the frozen scopedFields baseline (added without updating originalScopedFields?)", name)
		}
	}
	for name := range baseline {
		if !union[name] {
			t.Errorf("field %q is in the frozen baseline but missing from queryFields/mutationFields (dropped from the auth gate?)", name)
		}
	}

	// The runtime-assembled scopedFields (init) must equal the baseline too —
	// this is the set requiresScope actually consults.
	for name := range scopedFields {
		if !baseline[name] {
			t.Errorf("scopedFields has %q not in the frozen baseline", name)
		}
	}
	for name := range baseline {
		if !scopedFields[name] {
			t.Errorf("scopedFields is missing %q from the frozen baseline", name)
		}
	}

	// Every mutation must be wired into the execute() dispatch as a
	// fields["..."] index. This catches a mutation registered in mutationFields
	// but never resolved.
	dispatch := dispatchKeys(t)
	for name := range mutationFields {
		if !dispatch[name] {
			t.Errorf("mutation %q is in mutationFields but has no fields[%q] dispatch in graph_dispatch.go", name, name)
		}
	}
}

func TestFieldHandlerRegistryMatchesDispatch(t *testing.T) {
	dispatch := dispatchKeys(t)
	noErrorField := map[string]bool{
		"adminsession":        true,
		"connectionendpoints": true,
		"currentsession":      true,
		"importers":           true,
		"plugins":             true,
		"scrobblestatus":      true,
		"serverinfo":          true,
	}
	usedOrders := map[fieldDispatchOrder]bool{}
	for name := range dispatch {
		registered, ok := fieldHandlerRegistry[name]
		if !ok && suppressedDispatchFields[name] {
			continue
		}
		if !ok {
			t.Errorf("dispatched field %q is missing from fieldHandlerRegistry", name)
			continue
		}
		if registered.dataKey == "" {
			t.Errorf("fieldHandlerRegistry[%q] has no declarative dataKey", name)
		}
		if (registered.errField == "") != noErrorField[name] {
			t.Errorf("fieldHandlerRegistry[%q] errField=%q, no-error declaration=%t", name, registered.errField, noErrorField[name])
		}
		if len(registered.run) == 0 {
			t.Errorf("fieldHandlerRegistry[%q] has no dispatch run plan", name)
		}
		seenOrders := map[fieldDispatchOrder]bool{}
		var lastOrder fieldDispatchOrder
		for i, order := range registered.run {
			if int(order) >= len(fieldDispatchRuns) || fieldDispatchRuns[order] == nil {
				t.Errorf("fieldHandlerRegistry[%q] references missing dispatch order %d", name, order)
				continue
			}
			if seenOrders[order] {
				t.Errorf("fieldHandlerRegistry[%q] repeats dispatch order %d", name, order)
			}
			if i > 0 && order < lastOrder {
				t.Errorf("fieldHandlerRegistry[%q] dispatch orders are not deterministic", name)
			}
			seenOrders[order] = true
			usedOrders[order] = true
			lastOrder = order
		}
	}
	for name := range fieldHandlerRegistry {
		if !dispatch[name] {
			t.Errorf("fieldHandlerRegistry contains stale field %q with no fields[%q] dispatch", name, name)
		}
	}
	// An App Store build prunes the suppressed fields, which can leave their
	// dispatch runs unreferenced; the default build checks every run is used.
	for order, run := range fieldDispatchRuns {
		if run != nil && !usedOrders[fieldDispatchOrder(order)] && len(suppressedDispatchFields) == 0 {
			t.Errorf("fieldDispatchRuns[%d] is not referenced by any field", order)
		}
		if run == nil && usedOrders[fieldDispatchOrder(order)] {
			t.Errorf("fieldDispatchRuns[%d] is a retired nil slot but a field still dispatches through it", order)
		}
	}
}

func TestFieldHandlerClassificationMetadata(t *testing.T) {
	for name, handler := range fieldHandlerRegistry {
		if suppressedDispatchFields[name] {
			continue
		}
		wantKind := fieldRead
		switch {
		case name == "serverinfo", queryFields[name]:
			wantKind = fieldRead
		case mutationFields[name]:
			wantKind = fieldMutation
		default:
			t.Errorf("fieldHandlerRegistry[%q] has no read/mutation classification source", name)
			continue
		}
		if handler.kind != wantKind {
			t.Errorf("fieldHandlerRegistry[%q].kind=%d, want %d", name, handler.kind, wantKind)
		}

		if name == "serverinfo" && handler.roleGate != rolePublic {
			t.Errorf("fieldHandlerRegistry[%q].roleGate=%d, want public", name, handler.roleGate)
		}
		if name != "serverinfo" && handler.roleGate&roleSession == 0 {
			t.Errorf("fieldHandlerRegistry[%q].roleGate=%d, want session", name, handler.roleGate)
		}
	}
}

func TestFieldHandlerWireMetadataExceptions(t *testing.T) {
	type contract struct {
		dataKey  string
		errField string
		kind     fieldKind
		roleGate fieldRoleGate
		run      fieldDispatchPlan
	}
	want := map[string]contract{
		"manifest":             {"navigationManifest", "manifest", fieldRead, roleSession, fieldDispatchPlan{4}},
		"navigationmanifest":   {"navigationManifest", "manifest", fieldRead, roleSession, fieldDispatchPlan{4}},
		"clientprefs":          {"clientPrefs", "client preferences", fieldRead, roleSession, fieldDispatchPlan{43}},
		"trashedmarkers":       {"trashedMarkers", "markers", fieldRead, roleSession | roleManager, fieldDispatchPlan{13}},
		"trashlist":            {"trashList", "trash", fieldRead, roleSession | roleManager, fieldDispatchPlan{35}},
		"duplicatecopies":      {"duplicateCopies", "trash", fieldRead, roleSession | roleManager, fieldDispatchPlan{35}},
		"reveal":               {"reveal", "reveal", fieldMutation, roleSession, fieldDispatchPlan{25}},
		"relock":               {"relock", "reveal", fieldMutation, roleSession, fieldDispatchPlan{25}},
		"setrevealpin":         {"setRevealPin", "reveal", fieldMutation, roleSession | roleManager, fieldDispatchPlan{25}},
		"queueadd":             {"queueAdd", "queue", fieldMutation, roleSession, fieldDispatchPlan{30}},
		"queueclear":           {"queueClear", "queue", fieldMutation, roleSession, fieldDispatchPlan{30}},
		"queuemove":            {"queueMove", "queue", fieldMutation, roleSession, fieldDispatchPlan{30}},
		"queueremove":          {"queueRemove", "queue", fieldMutation, roleSession, fieldDispatchPlan{30}},
		"createmarker":         {"createMarker", "createMarker", fieldMutation, roleSession | roleManager, fieldDispatchPlan{14}},
		"artworkcandidates":    {"artworkCandidates", "artworkCandidates", fieldRead, roleSession | roleManager, fieldDispatchPlan{16}},
		"pinartworkcandidate":  {"pinArtworkCandidate", "pinArtworkCandidate", fieldMutation, roleSession | roleManager | roleAdultProfile, fieldDispatchPlan{16}},
		"notifications":        {"notifications", "notifications", fieldRead, roleSession, fieldDispatchPlan{88}},
		"marknotificationread": {"markNotificationRead", "markNotificationRead", fieldMutation, roleSession, fieldDispatchPlan{89}},
	}
	for name, expected := range want {
		got := fieldHandlerRegistry[name]
		if got.dataKey != expected.dataKey || got.errField != expected.errField || got.kind != expected.kind || got.roleGate != expected.roleGate || !equalDispatchPlan(got.run, expected.run) {
			t.Errorf("fieldHandlerRegistry[%q]=%+v, want %+v", name, got, expected)
		}
	}
}

func TestFieldHandlerRegistryBindsExecution(t *testing.T) {
	t.Run("data key", func(t *testing.T) {
		original := fieldHandlerRegistry["serverinfo"]
		registered := original
		registered.dataKey = "registryBoundData"
		fieldHandlerRegistry["serverinfo"] = registered
		defer func() { fieldHandlerRegistry["serverinfo"] = original }()

		resp := (&Handler{}).execute(t.Context(), auth.Scope{}, request{}, registryTestPlan("serverinfo"))
		if len(resp.Errors) > 0 || len(resp.Data) != 1 {
			t.Fatalf("execute(serverinfo) = %#v", resp)
		}
		if _, ok := resp.Data[registered.dataKey]; !ok {
			t.Fatalf("data keys = %v, want registry key %q", resp.Data, registered.dataKey)
		}
	})

	t.Run("error field and kind", func(t *testing.T) {
		handler := newTestHandler(t)
		original := fieldHandlerRegistry["media"]
		registered := original
		registered.errField = "registryBoundError"
		fieldHandlerRegistry["media"] = registered
		defer func() { fieldHandlerRegistry["media"] = original }()

		resp := handler.execute(t.Context(), auth.Scope{}, request{}, registryTestPlan("media"))
		if len(resp.Errors) != 1 || len(resp.Errors[0].Path) != 1 || resp.Errors[0].Path[0] != registered.dataKey {
			t.Fatalf("read error = %#v, want path [%q]", resp, registered.dataKey)
		}
		if value, ok := resp.Data[registered.dataKey]; !ok || value != nil {
			t.Fatalf("read data = %#v, want %q:null", resp.Data, registered.dataKey)
		}

		registered.kind = fieldMutation
		fieldHandlerRegistry["media"] = registered
		resp = handler.execute(t.Context(), auth.Scope{}, request{}, registryTestPlan("media"))
		if resp.Data != nil || len(resp.Errors) != 1 || len(resp.Errors[0].Path) != 0 {
			t.Fatalf("mutation-classified error = %#v, want all-or-nothing error", resp)
		}
	})

	t.Run("role gate", func(t *testing.T) {
		original := fieldHandlerRegistry["serverinfo"]
		registered := original
		registered.roleGate = roleManager
		fieldHandlerRegistry["serverinfo"] = registered
		defer func() { fieldHandlerRegistry["serverinfo"] = original }()

		resp := (&Handler{}).execute(t.Context(), auth.Scope{DeviceRole: store.DeviceRoleViewer}, request{}, registryTestPlan("serverinfo"))
		if resp.Data != nil || len(resp.Errors) != 1 || resp.Errors[0].Message != "manager role required" {
			t.Fatalf("role-gated execute = %#v", resp)
		}
	})
}

func registryTestPlan(field string) operationPlan {
	return operationPlan{occurrences: []operationOccurrence{{
		canonical: field, responseKey: fieldHandlerRegistry[field].dataKey, input: map[string]any{},
	}}}
}

func TestSelectedFieldDispatchOrdersAreGloballyDeterministic(t *testing.T) {
	fields := map[string]bool{
		"queueadd":             true,
		"queueclear":           true,
		"notifications":        true,
		"marknotificationread": true,
	}
	want := fieldDispatchPlan{30, 88, 89}
	for i := 0; i < 100; i++ {
		if got := selectedFieldDispatchOrders(fields); !equalDispatchPlan(got, want) {
			t.Fatalf("selectedFieldDispatchOrders()=%v, want %v", got, want)
		}
	}
}

func TestNotificationsHasExactlyOneDispatch(t *testing.T) {
	got := selectedFieldDispatchOrders(map[string]bool{"notifications": true})
	want := fieldDispatchPlan{88}
	if !equalDispatchPlan(got, want) {
		t.Fatalf("notifications dispatch orders = %v, want exactly %v", got, want)
	}
}

func equalDispatchPlan(a, b fieldDispatchPlan) bool {
	return slices.Equal(a, b)
}

// unauthenticatedDispatchFields are the only top-level fields execute() may
// dispatch WITHOUT an entry in the scope registry (queryFields/mutationFields).
// requiresScope consults scopedFields = queryFields ∪ mutationFields, so any
// dispatched field absent from both runs with a zero-value (unauthenticated)
// auth.Scope — no session is ever validated for it. serverInfo is the single
// sanctioned exception: it returns a static name/version, touches no scope, and
// is deliberately reachable without a session (graph dispatch registry + the
// scopedFields comment "Everything but serverInfo"). Adding a field here is an
// explicit, reviewed decision to expose it unauthenticated.
var unauthenticatedDispatchFields = map[string]bool{
	"serverinfo": true,
}

// TestEveryDispatchedFieldIsClassified is the H7 authz-completeness gate. It
// enumerates EVERY top-level field execute() can route (the fields["..."]
// dispatch keys parsed from graph_dispatch.go) and asserts each one is EXPLICITLY
// classified — present in queryFields, in mutationFields, or in the sanctioned
// unauthenticatedDispatchFields allowlist. There is no default branch: a new
// resolver added to execute() without a registry entry would slip past
// requiresScope and run with an unauthenticated zero scope. Today that is
// silent — the existing registry test only checks mutation→dispatch, not
// dispatch→registry — so this test closes the drift hole: add a resolver,
// forget its role/scope classification, and the build fails here.
func TestEveryDispatchedFieldIsClassified(t *testing.T) {
	dispatch := dispatchKeys(t)
	classified := make(map[string]bool, len(scopedFields)+len(unauthenticatedDispatchFields))
	for name := range scopedFields {
		classified[name] = true
	}
	for name := range unauthenticatedDispatchFields {
		classified[name] = true
	}

	for name := range dispatch {
		if suppressedDispatchFields[name] {
			continue
		}
		if !classified[name] {
			t.Errorf("dispatched field %q has no explicit authz classification: add it to queryFields or mutationFields (scoped), or to unauthenticatedDispatchFields if it is intentionally public. Unclassified, it bypasses requiresScope and runs with an unauthenticated zero scope.", name)
		}
	}

	// The allowlist must not rot: every entry must actually be dispatched, and
	// none may also sit in the scope registry (that would be a contradiction —
	// a field cannot be both scoped and unauthenticated).
	for name := range unauthenticatedDispatchFields {
		if !dispatch[name] {
			t.Errorf("unauthenticatedDispatchFields lists %q but it is not dispatched in graph_dispatch.go — remove the stale allowlist entry", name)
		}
		if scopedFields[name] {
			t.Errorf("%q is in both unauthenticatedDispatchFields and the scope registry — a field is either scoped or public, not both", name)
		}
	}
	for name := range suppressedDispatchFields {
		if !dispatch[name] {
			t.Errorf("suppressedDispatchFields lists %q but it is not dispatched in graph_dispatch.go — remove the stale suppression entry", name)
		}
		if scopedFields[name] {
			t.Errorf("%q is in both suppressedDispatchFields and the scope registry — an App Store-suppressed field must be unknown", name)
		}
	}
}

// dispatchKeys parses graph_dispatch.go and returns every string literal used as a
// fields[...] index — i.e. the top-level fields execute() actually dispatches.
func dispatchKeys(t *testing.T) map[string]bool {
	t.Helper()
	f := parseGraphFile(t)

	keys := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		idx, ok := n.(*ast.IndexExpr)
		if !ok {
			return true
		}
		for _, key := range fieldKeysFromIndex(idx) {
			keys[key] = true
		}
		return true
	})
	if len(keys) == 0 {
		t.Fatal("no fields[...] dispatch keys parsed from graph_dispatch.go")
	}
	return keys
}

func parseGraphFile(t *testing.T) *ast.File {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	graphFile := filepath.Join(filepath.Dir(thisFile), "graph_dispatch.go")
	if _, err := os.Stat(graphFile); err != nil {
		t.Fatalf("graph_dispatch.go missing: %v", err)
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, graphFile, nil, 0)
	if err != nil {
		t.Fatalf("parse graph_dispatch.go: %v", err)
	}
	return f
}

func fieldKeysFromExpr(expr ast.Expr) []string {
	keys := map[string]bool{}
	ast.Inspect(expr, func(n ast.Node) bool {
		idx, ok := n.(*ast.IndexExpr)
		if !ok {
			return true
		}
		for _, key := range fieldKeysFromIndex(idx) {
			keys[key] = true
		}
		return true
	})
	return sortedKeys(keys)
}

func fieldKeysFromIndex(idx *ast.IndexExpr) []string {
	ident, ok := idx.X.(*ast.Ident)
	if !ok || ident.Name != "fields" {
		return nil
	}
	lit, ok := idx.Index.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return nil
	}
	key, err := strconv.Unquote(lit.Value)
	if err != nil {
		return nil
	}
	return []string{key}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// TestConsoleFieldsRequireAdminSession is the H6/H7 authz-tier gate. For EVERY
// field declared in the adminFields (Console) registry, a manager session with
// NO open Admin Session must be refused with "admin session required". This
// turns "a Console resolver shipped at manager tier" into a failing test: add a
// field to adminFields but omit roleAdmin from its fieldHandler, and this fails
// because the request resolves instead of being refused.
func TestConsoleFieldsRequireAdminSession(t *testing.T) {
	handler := newTestHandler(t)
	for field := range adminFields {
		// A bare top-level selection is enough to route the field into its
		// dispatch group; the Admin Session guard fires before the resolver, so
		// no valid arguments or selection set are needed to observe the gate.
		kind := "query"
		if mutationFields[field] {
			kind = "mutation"
		}
		body := `{"query":"` + kind + ` { ` + fieldHandlerRegistry[field].wireName + ` }"}`
		_, errs := post(t, handler, body, authorizeManager)
		if len(errs) == 0 || errs[0].Message != "admin session required" {
			t.Errorf("Console field %q with a non-admin manager session returned %#v, want \"admin session required\" (missing roleAdmin in fieldHandler?)", field, errs)
		}
	}
}

// TestAdminAPITokenPassesOnlyCLIOps pins ADR-0007 §2 for operator-CLI auth: an
// admin API token (no Admin Session) is accepted for exactly the adminTokenOps
// allowlist and refused for every other Console field, so the browser Admin
// Session requirement is unchanged and the allowlist cannot grow silently.
func TestAdminAPITokenPassesOnlyCLIOps(t *testing.T) {
	st := storetest.Open(t)
	_, token, err := st.CreateAPIToken(t.Context(), "cli", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)
	authorize := func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+token) }

	wantOps := []string{"apitokens", "backup", "libraries", "refreshlibrarymetadata", "revokeapitoken", "runjob", "scanlibrary"}
	if got := sortedKeys(adminTokenOps); !slices.Equal(got, wantOps) {
		t.Fatalf("adminTokenOps = %v, want %v", got, wantOps)
	}
	for field := range adminTokenOps {
		if !adminFields[field] {
			t.Errorf("adminTokenOps %q is not a Console field", field)
		}
	}
	for field := range adminFields {
		kind := "query"
		if mutationFields[field] {
			kind = "mutation"
		}
		body := `{"query":"` + kind + ` { ` + fieldHandlerRegistry[field].wireName + ` }"}`
		_, errs := post(t, handler, body, authorize)
		refused := len(errs) > 0 && errs[0].Message == "admin session required"
		if adminTokenOps[field] && refused {
			t.Errorf("CLI op %q refused for an admin API token: %#v", field, errs)
		}
		if !adminTokenOps[field] && !refused {
			t.Errorf("Console field %q with an admin API token returned %#v, want \"admin session required\"", field, errs)
		}
	}
}

// TestAdminAPITokenCLIOpsEndToEnd drives the operator-CLI operations with a
// token: list, scan, and revoke succeed, a manager session without an Admin
// Session is still refused, and a mixed request cannot smuggle a non-CLI field.
func TestAdminAPITokenCLIOpsEndToEnd(t *testing.T) {
	st := storetest.Open(t)
	info, token, err := st.CreateAPIToken(t.Context(), "cli", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)
	authorize := func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+token) }

	data, errs := post(t, handler, `{"query":"query { apiTokens { id revoked } }"}`, authorize)
	if len(errs) != 0 || !strings.Contains(string(data["apiTokens"]), info.ID) {
		t.Fatalf("token list via token: data=%s errs=%#v", data["apiTokens"], errs)
	}
	_, errs = post(t, handler, `{"query":"mutation { runJob { id status } }"}`, authorize)
	for _, e := range errs {
		if e.Message == "admin session required" {
			t.Fatalf("runJob via token refused: %#v", errs)
		}
	}
	// A non-admin manager session is still refused for the same fields.
	_, errs = post(t, handler, `{"query":"query { apiTokens { id } }"}`, authorizeManager)
	if len(errs) == 0 || errs[0].Message != "admin session required" {
		t.Fatalf("manager session without Admin Session errors = %#v, want admin gate", errs)
	}
	// Mixing a CLI op with a non-CLI Console field is refused as a whole.
	_, errs = post(t, handler, `{"query":"query { apiTokens { id } consoleConfig { backgroundWorkers } }"}`, authorize)
	if len(errs) == 0 || errs[0].Message != "admin session required" {
		t.Fatalf("mixed request errors = %#v, want admin gate", errs)
	}
	data, errs = post(t, handler,
		`{"query":"mutation R($id:String!){ revokeApiToken(id:$id){ id revoked } }","variables":{"id":"`+info.ID+`"}}`, authorize)
	if len(errs) != 0 || !strings.Contains(string(data["revokeApiToken"]), `"revoked":true`) {
		t.Fatalf("token revoke via token: data=%s errs=%#v", data["revokeApiToken"], errs)
	}
}

// TestAPITokenTiersBoundConsoleFields (CORE-2095): a diagnostics token runs
// every Console read plus job cancel and retry and nothing else that writes; an
// agent token runs every Console field except the credential-minting and
// ownership-transfer ones; neither tier can widen itself through GraphQL.
func TestAPITokenTiersBoundConsoleFields(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	for _, access := range []store.APITokenAccess{store.APITokenAccessDiagnostics, store.APITokenAccessAgent} {
		_, token, err := st.CreateAPITokenWithAccess(t.Context(), string(access), access, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		authorize := func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+token) }
		for field := range adminFields {
			kind := "query"
			if mutationFields[field] {
				kind = "mutation"
			}
			var want bool
			switch access {
			case store.APITokenAccessDiagnostics:
				want = adminTokenOps[field] || !mutationFields[field] || diagnosticsTokenMutations[field]
			case store.APITokenAccessAgent:
				want = !agentTokenDenied[field]
			}
			if sessionOnlyReads[field] && !adminTokenOps[field] {
				want = false
			}
			body := `{"query":"` + kind + ` { ` + fieldHandlerRegistry[field].wireName + ` }"}`
			_, errs := post(t, handler, body, authorize)
			refused := len(errs) > 0 && errs[0].Message == "admin session required"
			if want == refused {
				t.Errorf("%s token on %q: refused=%v errs=%#v, want allowed=%v", access, field, refused, errs, want)
			}
		}
	}
	for field := range agentTokenDenied {
		if !adminFields[field] && !suppressedDispatchFields[field] {
			t.Errorf("agentTokenDenied %q is not a Console field", field)
		}
	}
	for field := range sessionOnlyReads {
		if !adminFields[field] || mutationFields[field] {
			t.Errorf("sessionOnlyReads %q is not a Console read", field)
		}
	}
	for field := range diagnosticsTokenMutations {
		if !adminFields[field] || !mutationFields[field] {
			t.Errorf("diagnosticsTokenMutations %q is not a Console mutation", field)
		}
	}
}

// TestNonConsoleFieldsAreNotAdminGated is the reverse drift-guard: a scoped
// field that is NOT in the adminFields registry must NOT answer with the Admin
// Session gate to a non-admin manager session. This catches a field carrying
// roleAdmin in fieldHandlerRegistry but missing from adminFields — the registry must
// declare every Console field, or the H6 test above and the manifest (H23-A)
// silently understate the admin surface. A non-Console field may legitimately
// return other errors (bad args, manager-role gate); only the admin-session
// gate is forbidden here.
func TestNonConsoleFieldsAreNotAdminGated(t *testing.T) {
	handler := newTestHandler(t)
	for field := range scopedFields {
		if adminFields[field] {
			continue
		}
		body := `{"query":"query { ` + field + ` }"}`
		_, errs := post(t, handler, body, authorizeManager)
		if len(errs) > 0 && errs[0].Message == "admin session required" {
			t.Errorf("non-Console field %q has roleAdmin but is absent from adminFields — add it to adminFields", field)
		}
	}
}

// TestMutationFieldsSorted guards the exported MutationFields() contract used
// by the conformance gate: the returned slice is sorted and lists exactly the
// mutationFields keys.
func TestMutationFieldsSorted(t *testing.T) {
	got := MutationFields()
	if !sort.StringsAreSorted(got) {
		t.Errorf("MutationFields() is not sorted: %v", got)
	}
	if len(got) != len(mutationFields) {
		t.Fatalf("MutationFields() returned %d names, mutationFields has %d", len(got), len(mutationFields))
	}
	for _, name := range got {
		if !mutationFields[name] {
			t.Errorf("MutationFields() returned %q which is not in mutationFields", name)
		}
	}
}

// TestAdminAPITokenDiscoversLibraries (CORE-1397): `beamfall library list`
// reads Library ids and names with an admin API token, so `scan --library-id`
// needs no browser lookup. Reading is the only Library operation a token gets:
// every Library mutation still answers with the Admin Session gate.
func TestAdminAPITokenDiscoversLibraries(t *testing.T) {
	st := storetest.Open(t)
	library, err := st.CreateLibrary(t.Context(), "Home Videos", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := st.CreateAPIToken(t.Context(), "cli", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)
	authorize := func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+token) }
	allLibraries := store.LibraryFilter{AllLibraries: true, Revealed: true}
	before, err := st.Libraries(t.Context(), allLibraries)
	if err != nil {
		t.Fatal(err)
	}

	data, errs := post(t, handler, `{"query":"query { libraries { id title } }"}`, authorize)
	if len(errs) != 0 {
		t.Fatalf("libraries via token errors = %#v", errs)
	}
	listed := string(data["libraries"])
	if !strings.Contains(listed, string(library.ID)) || !strings.Contains(listed, "Home Videos") {
		t.Fatalf("libraries via token = %s, want %s / Home Videos", listed, library.ID)
	}

	id := string(library.ID)
	for name, body := range map[string]string{
		"createLibrary":          `{"query":"mutation { createLibrary(title:\"x\") { id } }"}`,
		"setLibraryRoots":        `{"query":"mutation { setLibraryRoots(libraryID:\"` + id + `\", roots:[\"/tmp\"]) { id } }"}`,
		"setLibraryRestricted":   `{"query":"mutation { setLibraryRestricted(libraryID:\"` + id + `\", restricted:true) { id } }"}`,
		"setLibraryPreviewClips": `{"query":"mutation { setLibraryPreviewClips(libraryID:\"` + id + `\", enabled:true) { id } }"}`,
		"setLibraryShowOnHome":   `{"query":"mutation { setLibraryShowOnHome(libraryID:\"` + id + `\", enabled:false) { id } }"}`,
		"deleteLibrary":          `{"query":"mutation { deleteLibrary(libraryID:\"` + id + `\") { id } }"}`,
	} {
		_, errs := post(t, handler, body, authorize)
		if len(errs) == 0 || errs[0].Message != "admin session required" {
			t.Errorf("%s via token errors = %#v, want admin gate", name, errs)
		}
	}
	// A manager session without an Admin Session still cannot list.
	if _, errs := post(t, handler, `{"query":"query { libraries { id } }"}`, authorizeManager); len(errs) == 0 || errs[0].Message != "admin session required" {
		t.Fatalf("manager session libraries errors = %#v, want admin gate", errs)
	}
	after, err := st.Libraries(t.Context(), allLibraries)
	if err != nil || len(after) != len(before) {
		t.Fatalf("libraries after refused mutations = %d (%v), want %d unchanged", len(after), err, len(before))
	}
	for _, l := range after {
		if l.ID == library.ID && l.IsRestricted {
			t.Fatalf("refused mutation changed the Library: %+v", l)
		}
	}
}
