// Typed wire shapes for the Beamfall server API surface, derived directly from
// the Go source of truth:
//   - the Navigation Manifest Document — internal/manifest/manifest.go
//   - every GraphQL response struct — internal/graph/*.go (graph.go, query.go,
//     mydata.go, graph_*.go)
// Field names and optionality mirror the Go `json:"…"` tags exactly (a Go
// `omitempty` or a pointer field becomes an optional `?` property here). These
// are platform-generic by HARD invariant #1: the wire entity is `Media`, and no
// domain vocabulary (Performer/Scene/Studio/Episode/Movie) appears anywhere.
// There are no entitlement, licensing, or account types (HARD invariant #7) —
// none exist on this server.

// --- Localization (ADR-0034) ---------------------------------------------

// A locale map (internal/plugin/locale.go `LocaleString = map[string]string`).
// On the wire an English-only entry MAY serialize as the plain-string
// shorthand, so display strings are `LocaleString | string`. Clients resolve
// against the device locale with English as the universal fallback.
export type LocaleString = Record<string, string>;

/** A display string field: either a full locale map or its plain-string shorthand. */
export type DisplayString = LocaleString | string;

// --- Navigation Manifest Document (internal/manifest/manifest.go) ---------

/** The client-facing Navigation Manifest + Layout payload (manifest.Document). */
export interface NavigationManifest {
  profileID: string;
  revealed: boolean;
  generatedAt: string;
  libraries: LibrarySummary[];
  navigation: NavigationNode[];
  layouts: Layout[];
  theme: Theme;
}

/** A top-level Library row (manifest.LibrarySummary). */
export interface LibrarySummary {
  id: string;
  title: string;
  pluginID: string | null;
  previewClips: boolean;
  /** "#RRGGBB" plugin Library accent; omitted when none. */
  accent?: string;
}

/** An allowlisted navigation entry (manifest.NavigationNode). */
export interface NavigationNode {
  id: string;
  title: DisplayString;
  view: string;
  layoutID: string;
  /** Scopes a library entry's Sources; empty on aggregate (Home) entries. */
  libraryID?: string;
}

/** An ordered set of Section primitives (manifest.Layout). */
export interface Layout {
  id: string;
  sections: Section[];
  /** "detail" distinguishes detail Layouts from the browse Layout; omitted on browse. */
  role?: string;
  /** Binds a detail Layout to one plugin-declared Entity Kind; omitted on browse. */
  entityKind?: string;
}

/** A compiled client rendering primitive parameterized by data (manifest.Section). */
export interface Section {
  id: string;
  kind: string;
  title: DisplayString;
  artworkSlot: string;
  source: SourceSpec;
  /** A filter-bar's declared options; omitted on every other kind. */
  filters?: FilterOption[];
}

/** One filter-bar affordance (manifest.FilterOption). */
export interface FilterOption {
  field: string;
  value: string;
  label?: DisplayString;
}

/** The bounded data-source vocabulary a Section consumes (manifest.SourceSpec). */
export interface SourceSpec {
  kind: string;
  /** Scopes an "entity-items" Source to one Entity Kind; omitted otherwise. */
  entityKind?: string;
  /**
   * Narrows a catalog feed by equality/range on plugin-declared Media fields.
   * Plain strings are text equality; numbers/booleans are typed equality;
   * range objects use gte/lte. Echoed back verbatim on sourceItems.
   */
  fieldFilters?: FieldFilters;
  /** Narrows to the Media related to one named entity; echoed back verbatim. */
  entityFilter?: EntityFilter | null;
}

/** A Source's entity-relation filter (manifest.EntityFilter). */
export interface EntityFilter {
  kind: string;
  name: string;
}

/** The platform-controlled style token set (manifest.Theme). */
export interface Theme {
  accentColor: string;
}

/**
 * The minimal, backward-compatible field-filter wire shape: a map of
 * plugin-declared Media field → text equality (string), typed equality
 * (number/boolean), or a {gte,lte} range object.
 */
export type FieldFilters = Record<string, FieldFilterValue>;
export type FieldFilterValue = string | number | boolean | { gte?: number; lte?: number };

// --- Viewer: catalog, entity, playback ------------------------------------

/** Stored artwork for a SourceItem (graph.SourceItemArtwork). */
export interface SourceItemArtwork {
  poster: string;
}

/**
 * One item in a Section's feed (graph.SourceItem). The wire entity is generic
 * Media; `kind` is a plugin display discriminant, never a schema identifier.
 */
export interface SourceItem {
  id: string;
  kind: string;
  title: string;
  summary?: string;
  artworkSlots?: Record<string, string>;
  /** Present only when the item really has stored artwork (ADR-0014). */
  artwork?: SourceItemArtwork;
}

/** The current server-side session scope (graph.SessionInfo, currentSession). */
export interface SessionInfo {
  deviceRole: string;
  profileID: string;
  /** The bound Profile's kind ("adult"/"managed"); wire field name `profile`. */
  profile: string;
  revealed: boolean;
}

/** A Profile row offered to the picker (graph.ProfileSummary). */
export interface ProfileSummary {
  id: string;
  name: string;
  kind: string;
}

/** The reveal/relock/switch result (graph.RevealState). */
export interface RevealState {
  revealed: boolean;
}

/** The switchProfile mutation result. */
export interface SwitchProfileResult {
  profileID: string;
  revealed: boolean;
}

/** One declared field on an entity detail (graph.EntityFieldInfo). */
export interface EntityFieldInfo {
  name: string;
  type: string;
  value: string;
  label?: DisplayString;
}

/** An entity detail (graph.EntityInfo) as the web app selects it. */
export interface EntityInfo {
  id: string;
  kind: string;
  title: string;
  fields: EntityFieldInfo[];
}

/** A generic Media detail row (graph.MediaInfo), intentionally domain-neutral. */
export interface GenericMediaInfo {
  id: string;
  title: string;
  summary?: string;
  year?: number;
  favorited: boolean;
}

/** One backing file/Version/Part row for a Media item (graph.MediaFileInfo). */
export interface MediaFileDetail {
  mediaID: string;
  path: string;
  contentFingerprint: string;
  versionLabel: string;
  partOrdinal: number;
  osHash?: string;
  pHash?: string;
}

/** One generic Entity relation row (graph.EntityRelationInfo). */
export interface EntityRelationDetail {
  relation: string;
  targetKind: "entity" | "media";
  entity?: EntityInfo;
  media?: GenericMediaInfo;
  attributes: Record<string, string>;
  sortOrder?: number;
}

/** One generic Media relation row (graph.MediaRelationInfo). */
export interface MediaRelationDetail {
  relation: string;
  media: GenericMediaInfo;
}

/** Result for generic relation writes. */
export interface RelationWriteResult {
  relation: string;
  sourceID: string;
  targetID: string;
}

/** A playback Marker (graph.MarkerInfo) as the web player selects it. */
export interface PlaybackMarker {
  startSeconds: number;
  endSeconds?: number | null;
  segmentKind: string;
  title: string;
}

/** The reportPlayback mutation result. */
export interface ReportPlaybackResult {
  mediaID: string;
}

/** Variables for the reportPlayback mutation. */
export interface ReportPlaybackVars {
  mediaID: string;
  positionSeconds: number;
  durationSeconds: number;
  finished: boolean;
  eventTimeMS: number;
}

// --- Viewer: My Data / Privacy panel (internal/graph/mydata.go) -----------

export interface MyPlaybackProgress {
  mediaID: string;
  positionSeconds: number;
  durationSeconds: number;
  watched: boolean;
  playCount: number;
  updatedAt: string;
}

export interface MyFavorite {
  mediaID: string;
  favoritedAt: string;
}

export interface MyQueueEntry {
  mediaID: string;
  addedAt: string;
}

/** The engagement export (graph.MyEngagementExportInfo). */
export interface MyEngagementExport {
  schema: string;
  exportedAt: string;
  playbackProgress: MyPlaybackProgress[];
  favorites: MyFavorite[];
  queue: MyQueueEntry[];
}

export interface MySessionInfo {
  id: string;
  deviceID: string;
  deviceName: string;
  deviceRole: string;
  current: boolean;
  expiresAt?: string;
}

export interface MyStreamTokenInfo {
  targetKind: string;
  expiresAt: string;
}

/** One reveal-filtered inbox row (graph.NotificationInfo). */
export interface NotificationInfo {
  id: string;
  kind: string;
  title: string;
  body: string;
  ref: string;
  createdAt: string;
  read: boolean;
}

/** Result of marking a notification read (graph.NotificationReadResult). */
export interface NotificationReadResult {
  id: string;
  found: boolean;
}

/** Active sessions + Stream Tokens for the My Data panel (graph.MySessionsInfo). */
export interface MySessionsInfo {
  sessions: MySessionInfo[];
  streamTokens: MyStreamTokenInfo[];
}

/** The deleteMyEngagement mutation result. */
export interface MyDataDeleteResult {
  scope: string;
}

/** One per-Library play tally in the Summary (graph.MyLibraryBreakdownInfo). */
export interface MyLibraryBreakdown {
  libraryID: string;
  plays: number;
}

/**
 * The My-Data Summary aggregate (ADR-0036 §2.2 layer 1,
 * graph.MyDataSummaryInfo). Computed on read from the caller's own
 * reveal-filtered engagement; never stored.
 */
export interface MyDataSummary {
  plays: number;
  watched: number;
  favorites: number;
  queueEntries: number;
  libraryCount: number;
  libraryBreakdown: MyLibraryBreakdown[];
  firstActivity?: string;
  lastActivity?: string;
}

/** The kind discriminant of an event-log row (graph.MyEngagementEventKind). */
export type MyEngagementEventKind = "play" | "favorite" | "queue";

/** One event-log row (ADR-0036 §2.2 layer 2, graph.MyEngagementEventInfo). */
export interface MyEngagementEvent {
  kind: MyEngagementEventKind;
  mediaID: string;
  libraryID: string;
  occurredAt: string;
}

/** One page of the event log + total match count (graph.MyEngagementLogInfo). */
export interface MyEngagementLogPage {
  events: MyEngagementEvent[];
  total: number;
}

/** The event-log query: optional Library + date-range filter, paginated. */
export interface MyEngagementLogVars {
  libraryID?: string;
  from?: string;
  to?: string;
  limit?: number;
  offset?: number;
}

// --- Client preferences (graph.ClientPrefInfo) ----------------------------

/** A per-Profile client preference key/value (graph.ClientPrefInfo). */
export interface ClientPref {
  key: string;
  value: string;
}

// --- Mode-aware search (graph_enrichment.go) ------------------------------

/**
 * The search mode (graph.SearchMode). `auto` is served, never user-selected —
 * it degrades to keyword or semantic, and the response echoes the mode actually
 * served so the client can be honest about a fallback.
 */
export type SearchMode = "keyword" | "semantic" | "auto" | "recommend";

/** One explainable search result (graph.SearchResultItem) — SourceItem + how found. */
export interface SearchResultItem extends SourceItem {
  mode: SearchMode;
  score: number;
}

/**
 * The explainable search envelope (graph.SearchResultResponse). `mode` is the
 * mode actually served. `restrictedResultsPresent` is the leak-safe boolean —
 * true when the caller has hidden restricted Libraries — carrying NO title, id,
 * or count of hidden content (ADR-0083 §32).
 */
export interface SearchResultResponse {
  mode: SearchMode;
  items: SearchResultItem[];
  restrictedResultsPresent: boolean;
}

/** Per-Library semantic availability (graph.SemanticCapabilityInfo). */
export interface SemanticCapabilityInfo {
  libraryID: string;
  pluginActive: boolean;
  state: string;
}

// --- Manage / Console: libraries, plugins, jobs, schedules ----------------

/** A Library as the manage area selects it (graph.LibraryInfo). */
export interface LibraryInfo {
  id: string;
  title: string;
  pluginID: string | null;
  previewClips: boolean;
  restricted: boolean;
  roots?: string[];
}

/** The setLibraryPreviewClips result. */
export interface LibraryPreviewClipsResult {
  id: string;
  previewClips: boolean;
}

/**
 * One Library's skip-segment detection toggle (ENR-7, ADR-0048 Decision 3/6):
 * the operator opt-in that lets the built detector run on a real Library.
 * Shared shape for both the skipDetectionEnabled read and the
 * setSkipDetectionEnabled mutation result.
 */
export interface SkipDetectionInfo {
  libraryID: string;
  enabled: boolean;
}

/** The scanLibrary mutation result. */
export interface ScanLibraryResult {
  id: string;
  status: string;
}

/** The setLibraryRoots mutation result. */
export interface LibraryRootsResult {
  id: string;
  roots: string[];
}

/** The createLibrary mutation result. */
export interface CreateLibraryResult {
  id: string;
  title: string;
  pluginID: string | null;
}

/** A plugin as the manage area selects it (graph.PluginInfo, narrowed). */
export interface PluginSummary {
  id: string;
  displayName: DisplayString;
}

/** Job progress detail (graph.JobProgressInfo). */
export interface JobProgressInfo {
  current: number;
  total: number;
  unit: string;
  message?: string;
}

/** A job row as the manage area selects it (graph.JobInfo, narrowed). */
export interface JobInfo {
  id: string;
  kind: string;
  class?: string;
  status: string;
  progress: JobProgressInfo;
  attempts?: number;
  createdAt?: string;
  updatedAt?: string;
  startedAt?: string;
  finishedAt?: string;
  error?: string;
}

/** One durable job history/log entry (graph.JobEventInfo). */
export interface JobEventInfo {
  id: string;
  jobID: string;
  occurredAt: string;
  type: string;
  payload?: string;
}

/** Live resource-governor status for Operations (graph.OperationsGovernorInfo). */
export interface OperationsGovernorInfo {
  available: boolean;
  generationWorkers: number;
  backgroundCPUBudget: number;
  activeTranscodes: number;
  activeGeneration: number;
  generationAllowed: boolean;
  lowPowerMode: boolean;
  transcodePreemptingBackground: boolean;
}

/** A Schedule's structured trigger (graph.ScheduleTriggerInfo). */
export interface ScheduleTriggerInfo {
  kind: string;
  n: number;
  unit: string;
  weekday: number;
  hour: number;
  minute: number;
}

/** A persisted Schedule (graph.ScheduleInfo). */
export interface ScheduleInfo {
  id: string;
  jobKind: string;
  enabled: boolean;
  orphaned: boolean;
  nextDue: string;
  lastEnqueued: string;
  trigger: ScheduleTriggerInfo;
}

/** The updateSchedule enabled-only result (a narrowed ScheduleInfo). */
export interface ScheduleEnabledResult {
  id: string;
  enabled: boolean;
}

// --- Manage / Console: trash, artwork, subscriptions, tokens, activity ----

/** The backup mutation result (graph.BackupInfo) — the written manifest path. */
export interface BackupInfo {
  manifest: string;
}

/**
 * One published backup set in a destination's history (graph.BackupHistoryEntry,
 * MIGREV-1). `valid` is the confined integrity result; `error` is a sanitized
 * failure reason (never a checksum of an arbitrary server file). The set's
 * manifest lives at `<directory>/manifest.json`.
 */
export interface BackupHistoryEntry {
  directory: string;
  createdAt: string;
  sizeBytes: number;
  valid: boolean;
  error?: string;
}

/**
 * The result of the verifyBackup mutation (graph.VerifyBackupInfo, MIGREV-1):
 * pass/fail plus a sanitized reason on failure.
 */
export interface VerifyBackupInfo {
  ok: boolean;
  error?: string;
}

/**
 * The full metadata export (graph.MetadataExportInfo, ADR-0005 §5). The lists
 * are left untyped here because the UI only summarizes counts and downloads the
 * whole payload verbatim — the Go resolver is the schema of record.
 */
export interface MetadataExportInfo {
  schema: string;
  exportedAt: string;
  libraries: unknown[];
  media: unknown[];
  entities: unknown[];
  relations: unknown[];
  mediaLinks: unknown[];
  collections: unknown[];
}

/** A trashed Media entry (graph.TrashEntry, narrowed). */
export interface TrashEntry {
  id: string;
  title: string;
  trashedAt: string;
}

/** A first-class Core Tag (graph.TagInfo). */
export interface TagInfo {
  id: string;
  name: string;
  parentTagID?: string;
  trashedAt?: string;
  createdAt: string;
  updatedAt: string;
  /** Live total of assignments across Media/Entity/Marker targets (graph.TagInfo). */
  assignmentCount?: number;
}

/** A Tag assignment target kind. */
export type TagTargetType = "media" | "entity" | "marker";

/** A bulk Tag assignment mode. */
export type TagBulkMode = "add" | "remove" | "replace";

/** One target's current first-class Tag set (graph.TagAssignmentInfo). */
export interface TagAssignmentInfo {
  targetType: TagTargetType;
  targetID: string;
  libraryID: string;
  tags: TagInfo[];
}

/** A bulk Tag assignment result (graph.TagBulkAssignmentInfo). */
export interface TagBulkAssignmentInfo {
  affected: number;
  tags: TagInfo[];
}

/** Section visibility + order overrides for one Layout (graph.SectionOverrideInfo). */
export interface SectionOverrideInfo {
  hiddenSectionIDs: string[];
  sectionOrder: string[];
}

/**
 * The server-level Navigation Manifest + Layout override blob
 * (graph.NavOverrideInfo / WEB-5J / ADR-0061 §Layout editor). An empty blob
 * means "use plugin/library defaults".
 */
export interface NavOverrideInfo {
  /** Nav node IDs currently suppressed. */
  hiddenNodeIDs: string[];
  /** Operator-defined node order; empty = default order. */
  nodeOrder: string[];
  /** Node ID → locale map ({ en: "My Label" }). */
  nodeLabelOverrides: Record<string, Record<string, string>>;
  /** Operator accent override ("#RRGGBB"); empty = platform default. */
  themeAccentColor: string;
  /** Layout ID → its section override descriptor. */
  layoutSectionOverrides: Record<string, SectionOverrideInfo>;
}

/** One nav-override audit-log entry (graph.NavOverrideHistoryInfo). */
export interface NavOverrideHistoryInfo {
  id: number;
  scope: string;
  data: NavOverrideInfo;
  createdAt: string;
}

/** A manageable Marker row (graph.MarkerInfo). */
export interface MarkerInfo {
  id: string;
  mediaID: string;
  title?: string;
  startSeconds: number;
  endSeconds?: number;
  segmentKind?: string;
  tags: string[];
  trashedAt?: string;
}

/** Shared create/update Marker variables. */
export interface MarkerInput {
  title?: string;
  startSeconds: number;
  endSeconds?: number | null;
  segmentKind?: string;
  tags?: string[];
}

/** An artwork candidate (graph.ArtworkCandidateInfo). */
export interface ArtworkCandidate {
  id: string;
  slot: string;
  applied: boolean;
  pluginID?: string;
  scraperID?: string;
  previewURL?: string;
}

/** One identify-search candidate (graph.IdentifyCandidate). */
export interface IdentifyCandidate {
  id: string;
  title: string;
  summary?: string;
  year?: number;
  artwork?: Record<string, string>;
}

/** A subscription (graph.SubscriptionInfo, narrowed to the web selection). */
export interface SubscriptionInfo {
  id: string;
  feedURL: string;
  title: string;
  root: string;
  retentionKind: string;
  retentionN: number | null;
}

/** The setSubscriptionRetention result. */
export interface SubscriptionRetentionResult {
  id: string;
  retentionKind: string;
  retentionN: number | null;
}

/** A Smart Collection source/sort row (graph.SmartCollectionInfo). */
export interface SmartCollectionInfo {
  id: string;
  libraryID: string;
  name: string;
  sourceExpr: string;
  sortExpr: string;
  createdAt?: string;
}

/** One ordered Media member of a manual Collection (graph.CollectionMemberInfo). */
export interface CollectionMemberInfo {
  mediaID: string;
  sortOrder: number;
  addedAt?: string;
}

/** A manual ordered Collection/Sub-collection (graph.CollectionInfo). */
export interface CollectionInfo {
  id: string;
  libraryID: string;
  parentID?: string;
  name: string;
  sortOrder: number;
  trashedAt?: string;
  createdAt?: string;
  updatedAt?: string;
  memberCount: number;
  members: CollectionMemberInfo[];
}

/** A bare {id} reference (removeSubscription / addSubscription results). */
export interface IDRef {
  id: string;
}

/** An admin API token (graph.APITokenInfo). */
export interface APITokenInfo {
  id: string;
  name: string;
  createdAt: string;
  lastUsedAt?: string;
  revoked: boolean;
}

/** The createApiToken result — token shown once (graph.CreatedAPITokenInfo). */
export interface CreatedAPITokenInfo {
  token: string;
  info: APITokenInfo;
}

/** A scoped API key (graph.ScopedAPIKeyInfo). */
export interface ScopedAPIKeyInfo {
  id: string;
  name: string;
  scopes: string[];
  profileID?: string;
  createdAt: string;
  lastUsedAt?: string;
  revoked: boolean;
}

/** The createApiKey result — key shown once (graph.CreatedScopedAPIKeyInfo). */
export interface CreatedScopedAPIKeyInfo {
  key: string;
  info: ScopedAPIKeyInfo;
}

/** An activity-log entry as the web app selects it (graph.ActivityEntryInfo). */
export interface ActivityEntryInfo {
  id: string;
  occurredAt: string;
  action: string;
  reversible: boolean;
  targetType?: string;
  targetID?: string;
}

// --- Manage / Console: health, bulk ops, file ops -------------------------

/** Per-Library health counts (graph.LibraryHealthInfo). */
export interface LibraryHealthInfo {
  libraryID: string;
  title: string;
  counts: Record<string, number>;
}

/** A bulk health operation result (graph.BulkOpResult). */
export interface BulkOpResult {
  affected: number;
}

/** One Media item in a health dimension drill-down (graph.HealthItemInfo). */
export interface HealthItemInfo {
  mediaID: string;
  libraryID: string;
  title: string;
}

/** One duplicate cluster in the duplicates drill-down (graph.DuplicateGroupInfo). */
export interface DuplicateGroupInfo {
  /** Items[0] is the suggested canonical survivor (lowest Media ID). */
  items: HealthItemInfo[];
}

/** A bulk-edit/organize batch result (graph.BatchResultInfo). */
export interface BatchResultInfo {
  batchID: string;
  kind: string;
  affected: number;
}

/** A typed Media field value (graph.BulkEditFieldInfo). */
export interface MediaFieldInfo {
  name: string;
  type: string;
  value: string;
}

/** One previewed bulk-edit change (graph.BulkEditChangeInfo). */
export interface BulkEditChangeInfo {
  mediaID: string;
  libraryID: string;
  title: string;
  old: MediaFieldInfo[];
  new: MediaFieldInfo[];
}

/** One previewed file rename (graph.FileChangeInfo). */
export interface FileChangeInfo {
  mediaID: string;
  title: string;
  oldPath: string;
  newPath: string;
}

/** One server folder-tree node (graph.FolderEntryInfo). */
export interface FolderEntryInfo {
  name: string;
  path: string;
  kind: "folder" | "file";
  size: number;
  modifiedAt: string;
  children?: FolderEntryInfo[];
}

export type PathPickerRole =
  | "libraryRoot"
  | "incomingFolder"
  | "downloadDestination"
  | "backupDestination";

export interface PathPickerRootInfo {
  name: string;
  path: string;
  entryClass: "ordinary" | "mount-point";
}

export interface PathPickerEntryInfo {
  name: string;
  path: string;
  kind: "folder" | "file";
  accessError: "none" | "permission-denied" | "ownership-mismatch" | "read-only-mount";
  entryClass: "ordinary" | "mount-point";
  selectable: boolean;
  reasonCode?:
    | "outside-roots"
    | "overlaps-library-root"
    | "not-inside-library-root"
    | "blocklisted"
    | "permission-denied"
    | "server-cannot-see-path";
  mediaCount: number;
  sampleNames?: string[];
  previewTimeoutMs: number;
  maxPreviewEntries: number;
  truncated: boolean;
  children?: PathPickerEntryInfo[];
}

export interface PathPickerInfo {
  runtime: "bare" | "container" | "unknown";
  roles: PathPickerRole[];
  role: PathPickerRole;
  purposeKey: string;
  roots: PathPickerRootInfo[];
  entries: PathPickerEntryInfo[];
  previewTimeoutMs: number;
  maxPreviewEntries: number;
}

/** A managed folder/file mutation result (graph.ManagedPathInfo). */
export interface ManagedPathInfo {
  path: string;
  kind: "folder" | "file";
}

/** A recoverable managed folder/file delete result (graph.ManagedTrashInfo). */
export interface ManagedTrashInfo {
  trashID: string;
  originalPath: string;
  kind: "folder" | "file";
}

// --- Manage / Console: remote access, server/transcode config -------------

/** Direct-HTTPS/ACME carrier status (graph.DirectHTTPSStatus). */
export interface DirectHTTPSStatus {
  enabled: boolean;
  domain: string;
  certState: string;
}

/** Relay carrier status (graph.RelayStatus). */
export interface RelayStatus {
  enabled: boolean;
  endpoint: string;
  publicHost: string;
  connectionState: string;
  lastError?: string;
}

/** One reachable remote-access endpoint (graph.RemoteEndpoint). */
export interface RemoteEndpointInfo {
  kind: string;
  url: string;
}

/** Remote-access status (graph.RemoteAccessInfo). */
export interface RemoteAccessInfo {
  directHTTPS?: DirectHTTPSStatus | null;
  relay?: RelayStatus | null;
  plainHTTPPublicWarning: boolean;
  endpoints: RemoteEndpointInfo[];
}

/** A plugin's Console activation state (graph.PluginActivationInfo). */
export interface PluginActivationInfo {
  id: string;
  displayName: DisplayString;
  active: boolean;
  controlPolicy?: string;
  config: PluginConfigFieldInfo[];
  permissions: PluginPermissionInfo[];
  status: PluginStatusInfo[];
  actions: PluginActionDescriptor[];
  scrapers: PluginScraperInfo[];
}

/** Typed, non-secret plugin configuration field (graph.PluginConfigFieldInfo). */
export interface PluginConfigFieldInfo {
  name: string;
  type: string;
  label: DisplayString;
  required: boolean;
  default?: string;
  value?: string;
  configured: boolean;
  options?: PluginConfigOptionInfo[];
}

export interface PluginConfigOptionInfo {
  value: string;
  label: DisplayString;
}

/** Core-owned Integration Plugin permission descriptor. */
export interface PluginPermissionInfo {
  kind: string;
  label: DisplayString;
  description?: DisplayString;
  highRisk: boolean;
}

/** Runtime/declaration status row for the Console plugin surface. */
export interface PluginStatusInfo {
  id: string;
  label: DisplayString;
  state: string;
  detail?: string;
}

/** Server-declared Plugin Action Descriptor. */
export interface PluginActionDescriptor {
  id: string;
  label: DisplayString;
  icon?: string;
  risk: string;
  requiredPermissions?: string[];
  inputFields?: PluginConfigFieldInfo[];
  confirmation?: DisplayString;
}

/** One plugin-declared scraper endpoint plus its AuthRef state. */
export interface PluginScraperInfo {
  pluginID: string;
  scraperID: string;
  displayName: string;
  kind: string;
  endpoint: string;
  authRef: string;
  authStyle: string;
  credentialRequired: boolean;
  credentialConfigured: boolean;
  credentialStatus: string;
  credentialError?: string;
}

/** AuthRef-scoped scraper credential state (never secret bytes). */
export interface ScraperCredentialInfo {
  authRef: string;
  credentialConfigured: boolean;
  credentialStatus: string;
  credentialError?: string;
  scraperIDs: string[];
}

/** Server resource/plugin config (graph.ConsoleConfigInfo). */
export interface ConsoleConfigInfo {
  backgroundWorkers: number;
  updateCheckEnabled: boolean;
  updateCheckChannel: string;
  /** Remote (off-LAN) pairing-code enrollment toggle (ADR-0081). */
  remoteEnrollmentEnabled: boolean;
  /** events.db compaction retention window. */
  eventsRetentionDays: number;
  plugins: PluginActivationInfo[];
}

/** One enrolled device in the Console device-administration list (graph.DeviceInfo, ADR-0081). */
export interface DeviceInfo {
  deviceID: string;
  name: string;
  /** Device capability class: "viewer" or "manager". */
  role: string;
  revoked: boolean;
  /** True for the browser making this request — never offered for revocation. */
  current: boolean;
}

/** Result of revoking one device (graph.RevokeDeviceResult). */
export interface RevokeDeviceResult {
  deviceID: string;
  transcodesAborted: number;
}

/** Result of revoking all other devices (graph.RevokeAllDevicesResult, ADR-0081). */
export interface RevokeAllDevicesResult {
  revokedCount: number;
  transcodesAborted: number;
}

/**
 * Coarse outcome of one version check (graph.UpdateStatusInfo.state). "upToDate"
 * is the only value that means the running version is current: an empty
 * latestVersion with updateAvailable=false is produced by BOTH a current server
 * and a check that failed, and this is what separates them. Unknown values are
 * treated as "unavailable" by the renderer, never as a reassuring answer.
 */
export type UpdateCheckState = "unavailable" | "disabled" | "upToDate" | "updateAvailable";

/** Opt-in update-awareness status (graph.UpdateStatusInfo). */
export interface UpdateStatusInfo {
  state: UpdateCheckState;
  enabled: boolean;
  channel: string;
  currentVersion: string;
  latestVersion: string;
  updateAvailable: boolean;
}

/** Transcode policy config (graph.TranscodeConfigInfo). */
export interface HardwareAccelerationInfo {
  accelerator: string;
  status: "unavailable" | "configured-but-failed" | "working" | string;
}

export interface TranscodeConfigInfo {
  hardwareEnabled: boolean;
  codec: string;
  quality: string;
  device: string;
  configuredPreference?: string;
  hardwareAcceleration: HardwareAccelerationInfo[];
}

// --- Manage / Console: live sessions, access schedules --------------------

/** A live playback session (graph.LiveSessionInfo, narrowed to the web selection). */
export interface LiveSessionInfo {
  sessionID: string;
  deviceID: string;
  deviceName: string;
  profileID: string;
  profileName: string;
  mediaID: string;
  title: string;
  transcoding: boolean;
  positionSeconds: number;
  durationSeconds: number;
}

/** The terminateSession result. */
export interface TerminateSessionResult {
  sessionID: string;
}

/**
 * The playback rendition cache health snapshot (graph.PlaybackCacheInfo,
 * ADR-0057). Per-status object counts, the disk the ready objects occupy, and
 * the configured disk budget that governs eviction. `configured` is false when
 * no cache root is wired — the snapshot still reports any rows, but the writes
 * refuse and the panel renders the unconfigured state.
 */
export interface PlaybackCacheInfo {
  configured: boolean;
  ready: number;
  generating: number;
  failed: number;
  evicted: number;
  readyBytes: number;
  budgetBytes: number;
}

/** One capability's runtime state (graph.CapabilityStateInfo, ADR-0124 §5): its
 * id, kind ("workflow" | "probe"), runtime-enabled flag, whether the operator
 * may flip it (only Workflow caps), and the degraded reason a probe-absent
 * capability reports. The capability-control surface is suppressed entirely on
 * App Store binaries (both UI and API, invariant 8), so this is only ever
 * fetched by the web Console / direct-distribution Manager. */
export interface CapabilityStateInfo {
  id: string;
  kind: "workflow" | "probe";
  enabled: boolean;
  flippable: boolean;
  reason?: string;
}

/** The result of a playback-cache mutation (graph.PlaybackCacheOpResult): how
 * many cache objects the operation affected (renditions enqueued, or evicted). */
export interface PlaybackCacheOpResult {
  affected: number;
}

/** One allowed access window (graph.AccessWindowInfo). */
export interface AccessWindowInfo {
  weekday: number;
  startMinute: number;
  endMinute: number;
}

/** A Profile's access schedule (graph.AccessScheduleInfo). */
export interface AccessScheduleInfo {
  profileID: string;
  windows: AccessWindowInfo[];
}

// --- Manage / Console: setup wizard, importers, watcher, field locks ------

/** First-run setup readiness (graph.SetupState). */
export interface SetupState {
  adminPasswordSet: boolean;
  hasLibrary: boolean;
  hasOwnerProfile: boolean;
  pluginsActivated: boolean;
  complete: boolean;
}

/** A migration importer (graph.ImporterInfo). */
export interface ImporterInfo {
  pluginID: string;
  id: string;
  displayName: DisplayString;
  categories: string[];
  input: string;
  conflict: string;
  /** Secret-store ref for a live-source ("credentials") importer; "" otherwise. */
  credentialRef: string;
  /** True when the importer reads a live source behind a secret. */
  credentialRequired: boolean;
  /** True when a secret has been stored under credentialRef and is readable. */
  credentialConfigured: boolean;
  /** Coarse readiness word: not-required / configured / missing / invalid / … */
  credentialStatus: string;
}

/** An unmatched scan/import record (graph.UnmatchedItemInfo, ADR-0003 §4). */
export interface UnmatchedItemInfo {
  mediaID: string;
  title: string;
  pluginID: string;
  scraperID: string;
  confidence: number;
  threshold: number;
  candidateID?: string;
  candidateTitle?: string;
  queuedAt?: string;
  /**
   * ADR-0068 §A failure-reason taxonomy: "no-candidates" | "below-threshold" |
   * "ambiguous". Shape words, rendered through the taxonomy string table.
   */
  failureReason?: string;
}

/**
 * One SCORED Fix-Match candidate (graph.FixMatchCandidateInfo, METAPARITY-4).
 * `scored` is false when the plugin registered no MatchScorer — confidence and
 * threshold are then meaningless and must not render as 0%.
 */
export interface FixMatchCandidateInfo {
  id: string;
  title: string;
  summary?: string;
  year?: number;
  artwork?: Record<string, string>;
  pluginID: string;
  scraperID: string;
  confidence: number;
  threshold: number;
  scored: boolean;
  reason?: string;
}

/** One field of the preview-before-apply diff (graph.FixMatchFieldDiff). */
export interface FixMatchFieldDiff {
  field: string;
  current: string;
  proposed: string;
  changed: boolean;
}

/**
 * One pending Media-independent Unmatched Record (graph.UnmatchedRecordInfo,
 * ADR-0068). `sourceHandle` is display context — never a durable identity.
 */
export interface UnmatchedRecordInfo {
  id: string;
  origin: string;
  libraryID?: string;
  mediaID?: string;
  importRunID?: string;
  importerID?: string;
  sourceHandle?: string;
  candidateID?: string;
  candidateTitle?: string;
  confidence: number;
  failureReason?: string;
  queuedAt?: string;
}

/**
 * One active scoped suppression with its provenance
 * (graph.UnmatchedSuppressionInfo, METAPARITY-7).
 */
export interface UnmatchedSuppressionInfo {
  scope: string;
  key: string;
  recordID?: string;
  candidateID?: string;
  sourceHandle?: string;
  actorProfileID?: string;
  createdAt?: string;
}

/** Per-Library Watcher state (graph.WatcherStatusInfo). */
export interface WatcherStatusInfo {
  libraryID: string;
  title: string;
  state: string;
  reason?: string;
}

/** A Media field lock (graph.FieldLockInfo). */
export interface FieldLockInfo {
  field: string;
  locked: boolean;
}

// --- Manage / Console: enforced filters, qBittorrent ingest ---------------

/** A Profile's per-plugin certification ceiling (graph.CertificationCeilingInfo). */
export interface CertificationCeilingInfo {
  pluginID: string;
  ceiling: string;
  allowUnrated: boolean;
}

/** A Profile with its enforced certification ceilings (enforced-filters select). */
export interface EnforcedFilterProfile {
  id: string;
  name: string;
  kind: string;
  libraryIDs: string[];
  certificationCeilings: CertificationCeilingInfo[];
}

/** A plugin with its certification scale (enforced-filters select). */
export interface EnforcedFilterPlugin {
  id: string;
  displayName: DisplayString;
  certificationScale: string[] | null;
}

/** A Profile row for the Console Profiles admin lane (graph.ProfileInfo subset). */
export interface ProfileAdminInfo {
  id: string;
  name: string;
  kind: string;
  libraryIDs: string[];
  disabled: boolean;
}

/** The setCertificationCeiling mutation result (a narrowed Profile). */
export interface CertificationCeilingResult {
  id: string;
  certificationCeilings: CertificationCeilingInfo[];
}

/** A qBittorrent category→Library route (graph.QBittorrentRoute). */
export interface QBittorrentRoute {
  category: string;
  libraryID: string;
}

/** qBittorrent ingest config (graph.QBittorrentConfig, narrowed). */
export interface QBittorrentConfig {
  enabled: boolean;
  routes: QBittorrentRoute[];
}

/** One qBittorrent ingest status entry (graph.QBittorrentStatusEntry, narrowed). */
export interface QBittorrentStatusEntry {
  releaseName: string;
  category: string;
  outcome: string;
  libraryID?: string;
  detail?: string;
}

/** qBittorrent ingest config + recent status (graph.QBittorrentIngestInfo). */
export interface QBittorrentIngestInfo {
  config: QBittorrentConfig;
  status: QBittorrentStatusEntry[];
}

// --- Admin session --------------------------------------------------------

/** Admin Session state (graph.AdminSessionInfo, narrowed). */
export interface AdminSessionInfo {
  active: boolean;
  /**
   * ISO expiry of the Admin Session window, returned by both the adminSession
   * query and verifyAdminSession mutation while active so page reloads preserve
   * the same visible expiry the server enforces (ADR-0085).
   */
  expiresAt?: string;
}

// --- Mutation input shapes (echoed into GraphQL variables) ----------------

/** A structured Schedule trigger input (ScheduleTriggerInput). */
export interface ScheduleTriggerInput {
  kind: string;
  n?: number;
  unit?: string;
  weekday?: number;
  hour?: number;
  minute?: number;
}

/** A typed Media field input (MediaFieldInput). */
export interface MediaFieldInput {
  name: string;
  type: string;
  value: string;
}

/** An access-window input (AccessWindowInput). */
export interface AccessWindowInput {
  weekday: number;
  startMinute: number;
  endMinute: number;
}

/** One active AccessGrant (ACCESS-3, ADR-0113 §2.3). */
export interface AccessGrantInfo {
  profileID: string;
  mediaID: string;
  requestID: string;
  actorProfileID: string;
  createdAt: string;
  updatedAt: string;
}

/** One retention rule (ADR-0051, RET-2). */
export interface RetentionRuleInfo {
  id: string;
  name: string;
  smartCollectionID: string;
  sourceExpr: string;
  targetLibraryID: string;
  graceDays: number;
  revealedAtAuthoring: boolean;
  enabled: boolean;
  createdAt?: string;
  updatedAt?: string;
}

/** Result of setRetentionCandidateHold. */
export interface RetentionCandidateHoldResult {
  ruleID: string;
  mediaID: string;
  hold: boolean;
}
