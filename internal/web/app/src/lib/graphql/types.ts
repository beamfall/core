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
  /** Library icon name from the fixed vocabulary (see lib/libraryIcons). */
  icon?: string;
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
  /** Suppresses client-side fallback Hero synthesis when the Profile disabled the Hero. */
  suppressAutoHero?: boolean;
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
  /** Pins a "random" Source's shuffle order; echoed back verbatim. Omitted when none. */
  seed?: string;
  /** Selects a valued Source's band (resume-progress, availability); echoed back verbatim. */
  value?: string;
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
  /** Visible but blocked by a certification ceiling: it is listed, not playable. */
  locked?: boolean;
  /** The acting Profile's playback state; absent when it has none. */
  engagement?: SourceItemEngagement;
  /**
   * A media item's playback class ("audio", "video"), for a class-aware
   * placeholder when it has no artwork (CORE-1492) and the card's art frame. An
   * entity carries the class its linked Media all share, else none (CORE-2362).
   */
  mediaClass?: string;
  /** The Library owning this item, when returned by sourceItems/searchResult. */
  libraryID?: string;
  /** Stored Media year or a Section Source Entity's earliest visible linked Media year. */
  year?: number;
  /** A Media item's display content rating (PG-13, TV-MA); absent when unrated (CORE-2398). */
  certification?: string;
  /** A Media item's audience score from 0 to 10; absent when it has none (CORE-2398). */
  audienceScore?: number;
  /** The vote count behind that score; absent when unknown (CORE-2415). */
  audienceScoreVotes?: number;
  /**
   * The item's plugin declares that it plays on its own: a player never
   * queues its siblings after it (CORE-3147). Absent means it may queue.
   */
  playsAlone?: boolean;
  /**
   * A Library icon name (CORE-1200 vocabulary) the item's plugin declares for
   * artless items, drawn in place of the media-class glyph (CORE-3336).
   */
  placeholderIcon?: string;
  /** Who made an audio Media item: its person-archetype Entities' titles (CORE-2362). */
  byline?: string[];
  /**
   * What tells a Media item apart from its siblings, rendered by the Library
   * plugin per locale (CORE-1226); absent when the plugin declares none.
   */
  subtitle?: DisplayString;
  /** A Media item's stored structural ordinals, for numbering and order (CORE-2386). */
  sequence?: number;
  index?: number;
  /** A Media item's stored runtime, for a list row (CORE-2386). */
  durationSeconds?: number;
  /**
   * The outermost container Entity holding a Media item, so a card can name
   * what it belongs to and siblings can group under it (CORE-2524); absent
   * for entities and for Media no container holds.
   */
  container?: SourceItemContainer;
}

/** The container Entity a Media item belongs to (graph.SourceItemContainer). */
export interface SourceItemContainer {
  id: string;
  title: string;
  artwork?: SourceItemArtwork;
}

/**
 * The acting Profile's playback state for a SourceItem
 * (graph.SourceItemEngagement). Position and duration are present while the
 * item is resumable; the counts are present on an ordered container.
 */
export interface SourceItemEngagement {
  positionSeconds?: number;
  durationSeconds?: number;
  completed?: boolean;
  totalCount?: number;
  remainingCount?: number;
}

/** What an item action targets: exactly one of a Media or an Entity. */
export type ItemTarget = { mediaID: string } | { entityID: string };

/** The calling Profile's state for one item (graph.ItemStateInfo). */
export interface ItemStateInfo {
  id: string;
  /** "media" or "entity". */
  kind: string;
  watched: boolean;
  watchlisted: boolean;
  /** 1–10, or 0 when the Profile has not rated the item. */
  rating: number;
}

/** The current server-side session scope (graph.SessionInfo, currentSession). */
export interface SessionInfo {
  deviceRole: string;
  profileID: string;
  /** The bound Profile's kind ("adult"/"managed"); wire field name `profile`. */
  profile: string;
  revealed: boolean;
  /** Whether the bound Profile has a Restrictions PIN (CORE-2462). Absent from
   * a server that predates the field; callers then assume a PIN is set. */
  revealPinSet?: boolean;
  /** The bound Profile's subtitle languages, most preferred first; omitted when none. */
  subtitleLanguages?: string[];
}

/** A Profile row offered to the picker (graph.ProfileSummary). */
export interface ProfileSummary {
  id: string;
  name: string;
  kind: string;
  /** Whether switching into this Profile requires an entry PIN. */
  entryPinSet?: boolean;
  /** The chosen avatar token; empty or absent means a generated avatar. */
  avatar?: string;
  /**
   * The versioned URL of the Profile's uploaded picture (CORE-1240); empty or
   * absent when it has none. It replaces the avatar wherever one is shown.
   */
  pictureURL?: string;
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
  /** The plugin's display name for `kind`; absent when it declares none. */
  kindDisplayName?: DisplayString;
  title: string;
  fields: EntityFieldInfo[];
  /** The kind's fixed archetype ("container", "person", …); drives generic page layout. */
  archetype?: string;
  /** How an audio container plays ("feed" | "long-form"); only entity(id) fills it (CORE-2288). */
  audioForm?: "feed" | "long-form";
  /** The Subscription behind a "feed" container; only entity(id) fills it (CORE-2288). */
  subscriptionID?: string;
  /** Entities grouped by kind and relation direction; only entity(id) fills it. */
  relatedEntities?: RelatedEntityGroup[];
  /** The Entity's own artwork slots; only entity(id) fills it (CORE-2406). */
  artworkSlots?: Record<string, string>;
}

export type RelatedEntityDirection = "child" | "parent";

/** One kind's share of an Entity's related Entities (graph.RelatedEntityGroupInfo). */
export interface RelatedEntityGroup {
  kind: string;
  /**
   * How the group relates to the Entity (CORE-2036): "child" Entities point at
   * it, "parent" Entities are the ones it points at. One kind related both ways
   * comes as one group per direction.
   */
  direction?: RelatedEntityDirection;
  kindDisplayName?: DisplayString;
  /** The plugin's plural of `kindDisplayName`, for the group heading; absent when it declares none. */
  kindPluralDisplayName?: DisplayString;
  /** The group kind's fixed archetype ("container", "person", …). */
  archetype?: string;
  items: SourceItem[];
  /**
   * The group split by a plugin-declared field (CORE-2769): consecutive runs
   * of `items`, in order, each with the field value, its heading and size.
   * Absent when the kind is not split.
   */
  sections?: RelatedEntitySection[];
}

/** One run of a related group's items sharing a field value (graph.RelatedEntitySectionInfo). */
export interface RelatedEntitySection {
  /** The field value; "" for the items carrying none. */
  value: string;
  displayName?: DisplayString;
  count: number;
}

/** A generic Media detail row (graph.MediaInfo), intentionally domain-neutral. */
export interface GenericMediaInfo {
  id: string;
  title: string;
  summary?: string;
  year?: number;
  favorited: boolean;
  /** Probed or hinted runtime in whole seconds; omitted when unknown. */
  durationSeconds?: number;
  /** The Media class ("audio", "video", …); omitted when unknown. */
  mediaClass?: string;
  /** Plays on its own: no sibling queue follows it (CORE-3147); media(id) only. */
  playsAlone?: boolean;
  /** The plugin-declared icon for an artless item (CORE-3336); media(id) only. */
  placeholderIcon?: string;
  /**
   * The owning Library, for the readable address (CORE-2329); only the
   * media(id) read fills it, and a server that predates it omits it.
   */
  libraryID?: string;
  /** Present artwork slots → URL; only the media(id) read fills it. */
  artworkSlots?: Record<string, string>;
  /** Linked Entities in plugin declaration order; only the media(id) read fills it. */
  credits?: MediaCredit[];
  /** The identifying scraper's declared credit; only the media(id) read fills it. */
  metadataAttribution?: MetadataAttribution;
  /** The stored structural ordinals (CORE-2469); only the media(id) read fills them. */
  sequence?: number;
  index?: number;
  /** The plugin's subtitle without its ordinal segments: the item's own title (CORE-2469). */
  headline?: DisplayString;
  /** The plugin's display name for the item's class (CORE-2469). */
  classLabel?: DisplayString;
  /** The stored video frame size in pixels (CORE-2469). */
  videoWidth?: number;
  videoHeight?: number;
}

/**
 * A plugin-declared metadata credit (graph.MetadataAttributionInfo). Text,
 * provider name and link are plugin data; clients link only https URLs.
 */
export interface MetadataAttribution {
  providerName?: DisplayString;
  text: DisplayString;
  url: string;
}

/** One Entity a Media item is linked to (graph.MediaCreditInfo). */
export interface MediaCredit {
  entityID: string;
  title: string;
  kind: string;
  kindDisplayName?: DisplayString;
  /** The kind's declared plural display name, absent when it declares none (CORE-3337). */
  kindPluralDisplayName?: DisplayString;
  /** The kind's fixed archetype ("container", "person", …). */
  archetype?: string;
  relation: string;
  /** The relation's render-only attributes (plugin-declared names); absent when none (CORE-2406). */
  attributes?: Record<string, string>;
  /** The credited Entity's artwork slots (a person's headshot is its poster). */
  artworkSlots?: Record<string, string>;
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
  /** Listed but blocked by a locked certification ceiling: not playable. */
  locked?: boolean;
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

/**
 * serverInfo.playbackAdjustments (graph.PlaybackAdjustmentRanges, CORE-1658):
 * the inclusive pitch shift (whole semitones) and tempo ranges playback-info
 * accepts. Absent on a server without playback adjustment.
 */
export interface PlaybackAdjustmentRanges {
  pitchShiftSemitonesMin: number;
  pitchShiftSemitonesMax: number;
  tempoMin: number;
  tempoMax: number;
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
  /** Whether the player was paused: play history credits no time after a paused report. */
  paused?: boolean;
  /** The client's random ID for this play, kept across pauses and seeks. */
  playbackID?: string;
  /** Milliseconds actually played under playbackID so far (not paused, seeking or
   *  buffering): play history credits its growth. */
  playedMS?: number;
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
export interface MyWatchlistEntry {
  targetKind: "media" | "entity";
  targetID: string;
  addedAt: string;
}

export interface MyItemRating {
  targetKind: "media" | "entity";
  targetID: string;
  rating: number;
  ratedAt: string;
}

export interface MyEngagementExport {
  schema: string;
  exportedAt: string;
  playbackProgress: MyPlaybackProgress[];
  favorites: MyFavorite[];
  queue: MyQueueEntry[];
  watchlist: MyWatchlistEntry[];
  ratings: MyItemRating[];
}

export interface MySessionInfo {
  id: string;
  deviceID: string;
  deviceName: string;
  deviceRole: string;
  current: boolean;
  expiresAt?: string;
}

/** One active playback in the caller's Profile (graph.ProfileSessionInfo). Title and
 * libraryID are empty when the Media is outside the caller's current visibility. */
export interface ProfileSessionInfo {
  sessionID: string;
  deviceID: string;
  deviceName: string;
  deviceRole: string;
  current: boolean;
  mediaID?: string;
  title?: string;
  positionSeconds: number;
  durationSeconds: number;
}

/** controlSession arguments; each verb takes only its own (store.SessionControlArgs). */
export interface ControlSessionVars {
  sessionID: string;
  command: string;
  direction?: "next" | "previous";
  positionMs?: number;
  volume?: number;
  trackID?: string;
  mediaID?: string;
  text?: string;
}

export interface ControlSessionResult {
  sessionID: string;
  command: string;
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

/** One of the calling Profile's Mutes (graph.MyMuteInfo). Kind is entity, tag or media. */
export interface MuteInfo {
  kind: string;
  targetID: string;
  includeDescendants: boolean;
  mutedAt: string;
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
  /** Row detail, present only when the item's Library is visible to the caller. */
  libraryID?: string;
  libraryTitle?: string;
  year?: number;
  /** Core media class of a Media row ("video", "audio", "image"). */
  mediaClass?: string;
  /** Plugin-declared kind of an Entity row, with its localized display name. */
  entityKind?: string;
  entityKindDisplayName?: Record<string, string>;
}

/**
 * The explainable search envelope (graph.SearchResultResponse). `mode` is the
 * mode actually served. `restrictedResultsPresent` is the leak-safe boolean —
 * true when the caller has hidden restricted Libraries — carrying NO title, id,
 * or count of hidden content (ADR-0083 §32).
 */
export interface SearchResultResponse {
  /** Keyword paging; absent from older servers. */
  hasMore?: boolean;
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

/** A plugin's declared media-presentation & engagement posture (graph.PluginPresentationInfo). */
export interface PluginPresentationInfo {
  resumeMedia?: boolean;
  playsAlone?: boolean;
  selectPlays?: boolean;
  interactiveSidecars?: string[];
  defaultIcon?: string;
}

/** A Library as the manage area selects it (graph.LibraryInfo). */
export interface LibraryInfo {
  id: string;
  title: string;
  pluginID: string | null;
  previewClips: boolean;
  /** False when a manager keeps this Library off Home (CORE-2360); absent or true = shown. */
  showOnHome?: boolean;
  restricted: boolean;
  roots?: string[];
  /** The icon clients draw: the owner's choice, else the default. */
  icon?: string;
  /** The owner's own choice; absent while the Library uses its default. */
  iconChoice?: string;
  /** The icon the Library gets when the owner has not chosen one. */
  defaultIcon?: string;
  mediaClasses?: string[];
  presentation?: PluginPresentationInfo;
  capabilities?: string[];
}

/**
 * A deleted Library waiting in "Recently deleted" (graph.TrashedLibraryInfo).
 * trashedAt and purgeAfter are RFC 3339 times. A Library that is not purgeable
 * (a Custodial one) has no purgeAfter: the server keeps it until it is restored.
 */
export interface TrashedLibraryInfo {
  id: string;
  title: string;
  trashedAt: string;
  mediaCount: number;
  purgeable: boolean;
  purgeAfter?: string | null;
}

/** What a permanent Library delete removed (graph.LibraryPurgeResult). */
export interface LibraryPurgeResult {
  libraryID: string;
  title: string;
  mediaRemoved: number;
  entitiesRemoved: number;
}

/** The setLibraryPreviewClips result. */
export interface LibraryPreviewClipsResult {
  id: string;
  previewClips: boolean;
}

/** The setLibraryRestricted result (CORE-2463). */
export interface LibraryRestrictedResult {
  id: string;
  restricted: boolean;
}

/** The setLibraryShowOnHome result (CORE-2360). */
export interface LibraryShowOnHomeResult {
  id: string;
  showOnHome: boolean;
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

/** Viewer-readable plugin metadata for deriving experience modes without plugin IDs. */
export interface ViewerPluginInfo {
  id: string;
  displayName: DisplayString;
  mediaLabel?: DisplayString;
  mediaClasses?: string[];
  presentation?: PluginPresentationInfo;
  capabilities?: string[];
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
  target?: JobTargetInfo | null;
}

/** The Library a job works on (graph.JobTargetInfo); absent when redacted. */
export interface JobTargetInfo {
  libraryID: string;
  libraryTitle: string;
  root?: string;
  backfill?: boolean;
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
  /** Wrap-key fingerprint the set was sealed to; empty for a plaintext set. */
  keyFingerprint?: string;
  /** "current" for the data directory's wrap key, "archived" for an earlier one, empty when unknown. */
  keyState?: BackupKeyState;
}

/** How a set's wrap key relates to the current one (BKUP-14 key-to-archive map). */
export type BackupKeyState = "current" | "archived" | "";

/**
 * The result of the verifyBackup mutation (graph.VerifyBackupInfo, MIGREV-1):
 * pass/fail plus a sanitized reason on failure.
 */
export interface VerifyBackupInfo {
  ok: boolean;
  error?: string;
}

/** One database snapshot in a Backup Set (graph.BackupContentsDatabaseInfo, CORE-1720). */
export interface BackupContentsDatabaseInfo {
  name: string;
  database: string;
  schemaVersion: number;
  supportedSchemaVersion: number;
  sizeBytes: number;
}

/**
 * What an encrypted Backup Set holds and whether this server can restore it
 * (graph.BackupContentsInfo, CORE-1720). refusal says why when compatible is
 * false.
 */
export interface BackupContentsInfo {
  createdAt: string;
  createdBy: string;
  reason: string;
  restoreInstructionsVersion: number;
  supportedRestoreInstructionsVersion: number;
  databases: BackupContentsDatabaseInfo[];
  operationalFiles: string[];
  libraryRootCount: number;
  compatible: boolean;
  refusal: string;
}

/** The outcome of the last staged restore a start ran (graph.RestoreResultInfo). */
export interface RestoreResultInfo {
  appliedAt: string;
  ok: boolean;
  error: string;
  rollbackCopy: string;
  backupCreatedAt: string;
}

/**
 * The staged restore, if any, and the last applied one (graph.StagedRestoreInfo,
 * CORE-1720). The backup key is never part of it.
 */
export interface StagedRestoreInfo {
  staged: boolean;
  stagedAt: string;
  envelope: string;
  replaceIdentity: boolean;
  contents: BackupContentsInfo | null;
  lastResult: RestoreResultInfo | null;
}

/**
 * Whether a bootstrap backup key still waits to be saved (graph.BackupKeyStatusInfo,
 * CORE-2032). It never carries the key.
 */
export interface BackupKeyStatusInfo {
  pending: boolean;
  expiresAt: string;
  daysLeft: number;
}

/**
 * The pending backup key, revealed to the owner in an Admin Session
 * (graph.RevealedBackupKeyInfo, CORE-2032). Keep it in component state only.
 */
export interface RevealedBackupKeyInfo {
  escrowID: string;
  backupKey: string;
  expiresAt: string;
  daysLeft: number;
}

/** One scheduled-backup outcome (graph.BackupRunInfo, CORE-0778). */
export interface BackupRunInfo {
  at: string;
  failureClass: string;
  set: string;
}

/**
 * One restore-drill outcome (graph.BackupDrillInfo, CORE-0778 BKUP-15). The
 * counts are set only on the result of verifyBackupNow.
 */
export interface BackupDrillInfo {
  at: string;
  verified: boolean;
  set: string;
  reason: string;
  backupCreatedAt?: string;
  databases?: number;
  libraries?: number;
  durationMs?: number;
}

/** The update-gate policy (ADR-0192 §6): soft by default, hard as an opt-in. */
export type BackupUpgradeGate = "soft" | "hard";

/** The two distinct backup failure banners (ADR-0192 §6). */
export type BackupBanner = "backup-failed" | "integrity-failed" | "unverified-upgrade";

/**
 * The scheduled-backup state behind the backup banners, recovery evidence and
 * update gate (graph.BackupStatusInfo, CORE-0778 BKUP-14). No key material.
 */
export interface UpgradeBypassInfo {
  at: string;
  targetVersion: string;
  actorProfileID: string;
  policy: string;
  failedSet: string;
  reason: string;
  appliedAt: string;
  valid?: boolean;
}

export interface UpgradeEventInfo {
  at: string;
  fromVersion: string;
  toVersion: string;
  bypassed: boolean;
  reason: string;
}

export interface StagedUpdateInfo {
  staged: boolean;
  status: string;
  stagedAt: string;
  targetVersion: string;
  channel: string;
  commit: string;
  artifactName: string;
  artifactSHA256: string;
  sourceDistDir: string;
  bypassAuthorized: boolean;
  appliedAt: string;
  rollbackBinary: string;
}

export interface BackupStatusInfo {
  backupFailedBanner: boolean;
  integrityFailedBanner: boolean;
  lastSuccess: BackupRunInfo | null;
  lastFailure: BackupRunInfo | null;
  lastIntegrityFailure: BackupRunInfo | null;
  /** The newest backup whose set passed its integrity check in the same job. */
  lastIntegrityPass?: BackupRunInfo | null;
  /** True only when a recorded integrity pass is newer than every failure; acknowledging a banner never sets it. */
  updateGateClear?: boolean;
  upgradeGate: BackupUpgradeGate;
  lastDrill: BackupDrillInfo | null;
  unverifiedUpgradeBanner?: boolean;
  failedSet?: string;
  lastBinaryVersion?: string;
  lastUpgradeBypass?: UpgradeBypassInfo | null;
  lastUnverifiedUpgrade?: UpgradeEventInfo | null;
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
  /** Set when a Version merge (not undone) folded this Media into a survivor. */
  mergedInto?: TrashMergeInfo;
}

/** The Version merge that sent a Media to Trash (graph.TrashMergeInfo). */
export interface TrashMergeInfo {
  batchID: string;
  survivorID: string;
  survivorTitle: string;
  mergedAt: string;
}

/** A Media whose live Versions include likely identical copies (graph.DuplicateCopySetInfo). */
export interface DuplicateCopySet {
  mediaID: string;
  title: string;
  libraryID: string;
  copies: DuplicateCopy[];
}

/** One on-disk file of a DuplicateCopySet. */
export interface DuplicateCopy {
  path: string;
  sizeBytes: number;
  label: string;
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

/** Feed health (CORE-2955). `healthStatus` is ok, failing, warning after
 * seven days of failure, or retired. `lastErrorClass` is a closed class. */
export interface SubscriptionHealth {
  healthStatus: string;
  lastErrorClass?: string | null;
  failingSince?: string | null;
  nextPollAt?: string | null;
}

/** A followed feed as the feed home shows it (graph.SubscriptionInfo, the
 * CORE-2957 selection). Never selects feedURL: it is secret-class. */
export interface FeedSubscription extends SubscriptionHealth {
  id: string;
  libraryID: string;
  title: string;
  downloadPolicy: string;
  downloadLatestN: number;
  lastPolledAt?: string | null;
  entryCount: number;
  downloadedCount: number;
  catalogBytes: number;
}

/** The viewer-safe follow state and new-entry route: new, queue-end or
 * queue-front (graph.FollowInfo, CORE-2958). */
export interface FollowInfo {
  id: string;
  title: string;
  followed: boolean;
  newEntryRoute: string;
}

/** The viewer-safe refreshSubscription response. */
export interface SubscriptionRefreshInfo extends SubscriptionHealth {
  id: string;
  title: string;
}

/** One searchFeeds result (graph.FeedSearchResult). */
export interface FeedSearchResult {
  title: string;
  feedURL: string;
  artworkURL?: string | null;
  description?: string | null;
}

/** One catalogue entry of a Subscription (graph.SubscriptionEntryInfo).
 * `mediaID` is a Media id the ordinary play path accepts, downloaded or not. */
export interface SubscriptionEntry {
  mediaID: string;
  title: string;
  publishedAt?: string | null;
  feedPosition: number;
  enclosureLength: number;
  downloadState: string;
  availability: string;
  /** Whether the calling Profile has played the entry (CORE-2959). */
  played: boolean;
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

// --- Manage / Console: health, bulk ops, file ops -------------------------

/**
 * Per-Library health counts (graph.LibraryHealthInfo). A dimension that does
 * not apply to the Library's plugin is absent from `counts`, never zero.
 */
export interface LibraryHealthInfo {
  libraryID: string;
  title: string;
  counts: Partial<Record<string, number>>;
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

/** Automatic router port mapping, UPnP IGD or NAT-PMP (graph.PortMappingStatus, CORE-1969). */
export interface PortMappingStatus {
  enabled: boolean;
  /** disabled | mapping | mapped | failed */
  state: string;
  /** upnp | natpmp, empty before a gateway answers. */
  protocol?: string;
  internalPort: number;
  externalPort?: number;
  externalAddress?: string;
  /** 0 = permanent lease. */
  leaseSeconds?: number;
  expiresAt?: string;
  /** Reason code when state is failed. */
  reason?: string;
  gatewayCode?: number;
}

/** One path's reachability outcome (graph.ReachabilityVerdict). */
export interface ReachabilityVerdict {
  /** direct | relay */
  path: string;
  reachable: boolean;
  /**
   * How the probe was sent, when one ran: gateway_loopback (direct path, from
   * inside the network through the router's NAT hairpin, so a pass is not an
   * outside test) or relay.
   */
  method?: string;
  /** Reason code when unreachable; hairpin_unverified means unknown, not closed. */
  reason?: string;
}

/** The last external reachability self-test (graph.ReachabilityReport). */
export interface ReachabilityReport {
  checkedAt: string;
  verdicts: ReachabilityVerdict[];
}

/** Remote-access status (graph.RemoteAccessInfo). */
export interface RemoteAccessInfo {
  directHTTPS?: DirectHTTPSStatus | null;
  relay?: RelayStatus | null;
  plainHTTPPublicWarning: boolean;
  endpoints: RemoteEndpointInfo[];
  portMapping?: PortMappingStatus | null;
  reachability?: ReachabilityReport | null;
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
  /** Plugin-declared presentation for the AuthRef credential (CORE-1127). */
  credentialHelp?: PluginCredentialHelpInfo;
}

/** How a plugin names its credential and where an owner gets one (never secret bytes). */
export interface PluginCredentialHelpInfo {
  label: DisplayString;
  helpURL?: string;
  helpLabel?: DisplayString;
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
  /**
   * Server-wide remote (off-LAN) streaming cap in bits per second; 0 means
   * unlimited (CORE-1717). Optional so older fixtures compile.
   */
  remoteMaxBitrate?: number;
  /**
   * Operator switch that refuses new Viewer pairing codes and the redemption of
   * outstanding ones (CORE-0393). Manager pairing and demo enrollment are
   * unaffected. The server always sends it; optional so older fixtures compile.
   */
  viewerPairingCodesDisabled?: boolean;
  /** events.db compaction retention window. */
  eventsRetentionDays: number;
  /**
   * Owner-set lifetimes of a sign-in session (days) and of an Admin Session
   * (minutes); 0 means it never expires (CORE-2303). The server always sends
   * them; optional so older fixtures compile.
   */
  sessionLifetimeDays?: number;
  adminSessionMinutes?: number;
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
  /** False once the device has no session left (it signed out) and must enroll again (CORE-1607). */
  signedIn?: boolean;
  /** The Player Profile this device is bound to as a player display (ADR-0240 D2). */
  playerProfileID?: string;
  /** The display name shown to controllers for a bound player display. */
  playerDisplayName?: string;
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
  /** Stored concurrent-transcode cap; 0 = automatic. */
  maxConcurrentTranscodes: number;
  /** The cap the transcoder enforces now. */
  effectiveMaxConcurrentTranscodes: number;
  /** BEAMFALL_TRANSCODE_CAP or --transcode-cap overrides the stored cap. */
  maxConcurrentTranscodesEnvOverride: boolean;
  maxConcurrentTranscodesRestartRequired: boolean;
  /** Stored transcode temp directory; "" = server default. */
  tempDir: string;
  /** The directory the running transcoder uses. */
  effectiveTempDir: string;
  /** BEAMFALL_TRANSCODE_TMP or --transcode-tmp overrides the stored directory. */
  tempDirEnvOverride: boolean;
  tempDirRestartRequired: boolean;
  /** HDR-to-SDR tone-map switch (applies live). */
  toneMapEnabled: boolean;
}

/** A transcode-config write: a null cap returns it to automatic. */
export interface TranscodeConfigUpdate {
  hardwareEnabled: boolean;
  codec: string;
  quality: string;
  device: string;
  maxConcurrentTranscodes: number | null;
  tempDir: string;
  toneMapEnabled: boolean;
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
  /** How the item reaches the device: "direct", "remux" or "transcode". */
  playMethod: string;
  /** Why it is not direct play (a stable reason code); empty when direct or unknown. */
  playReason: string;
  /** Delivered bitrate in bits per second; 0 when unknown. */
  bitrate: number;
  positionSeconds: number;
  durationSeconds: number;
}

/** One host resource sample (graph.HostMetricSample). */
export interface HostMetricSample {
  at: string;
  cpuPercent: number;
  cpuKnown: boolean;
  memoryUsedBytes: number;
  memoryTotalBytes: number;
  sentBytesPerSecond: number;
  receivedBytesPerSecond: number;
}

/** The server's recent resource use (graph.HostMetricsInfo). */
export interface HostMetricsInfo {
  intervalSeconds: number;
  samples: HostMetricSample[];
}

/** How a play was delivered: as stored, remuxed or transcoded (CORE-1703).
 * "unknown" is a play recorded before the method was, or with no decision. */
export type PlayMethod = "direct" | "remux" | "transcode" | "unknown";

/** One past play (graph.PlayInfo). */
export interface PlayInfo {
  profileID: string;
  profileName: string;
  mediaID: string;
  title: string;
  libraryID: string;
  libraryTitle: string;
  /** The Device that played it; absent for plays recorded before devices were. */
  deviceID?: string;
  deviceName?: string;
  deviceClient?: string;
  playMethod: PlayMethod;
  startedAt: string;
  watchedSeconds: number;
  finished: boolean;
}

/** Filters for the play history read. */
export interface PlayHistoryFilter {
  profileID?: string;
  libraryID?: string;
  mediaID?: string;
  deviceID?: string;
  playMethod?: PlayMethod;
  since?: string;
  until?: string;
  limit?: number;
}

/** Filters and range for the play stats read: a 7, 30 or 90 day window, or
 * since (and optionally until), not both. */
export interface PlayStatsFilter {
  profileID?: string;
  libraryID?: string;
  mediaID?: string;
  deviceID?: string;
  playMethod?: PlayMethod;
  windowDays?: 7 | 30 | 90;
  since?: string;
  until?: string;
}

/** One row of a play breakdown: how many plays and how long. */
export interface PlayTally {
  plays: number;
  watchedSeconds: number;
}

/** Totals and breakdowns over a date range (graph.PlayStatsInfo). Days run in
 * the caller's time zone and omit days with no plays. */
export interface PlayStatsInfo {
  windowDays: number;
  since: string;
  until: string;
  totalPlays: number;
  totalWatchedSeconds: number;
  profiles: ({ profileID: string; profileName: string } & PlayTally)[];
  items: ({ mediaID: string; title: string; libraryID: string } & PlayTally)[];
  devices: ({ deviceID: string; deviceName: string; deviceClient: string } & PlayTally)[];
  methods: ({ playMethod: PlayMethod } & PlayTally)[];
  days: ({ day: string } & PlayTally)[];
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

/**
 * One Media's editable built-in metadata with its field locks
 * (graph.MediaMetadataInfo, CORE-1715). `refreshable` is false until an
 * identification recorded the candidate a per-item refresh re-applies.
 */
export interface MediaMetadataInfo {
  mediaID: string;
  title: string;
  sortTitle: string;
  summary: string;
  year: number;
  refreshable: boolean;
  locks: FieldLockInfo[];
  operatorOrdinal?: { sequence: string; index: number } | null;
}

/** The built-in fields an item edit may set; an absent field is left alone. */
export interface MediaMetadataEdit {
  title?: string;
  sortTitle?: string;
  summary?: string;
  year?: number;
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
  /** The Profile's remote bitrate cap in bits per second; 0 or absent is no cap (CORE-1717). */
  remoteMaxBitrate?: number;
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
  /** The avatar token (see ProfileSummary.avatar). */
  avatar?: string;
  /** The uploaded picture's URL (see ProfileSummary.pictureURL). */
  pictureURL?: string;
  /** Player Profiles only: whether walk-up browsing is on (ADR-0240 D7). */
  walkUpEnabled?: boolean;
}

/** A Player Profile's controllers list (graph.PlayerControllersInfo, ADR-0240 D4). */
export interface PlayerControllersInfo {
  profileID: string;
  allAdults: boolean;
  include: string[];
  exclude: string[];
}

/** The item playing on a player display, filtered for the caller (ADR-0240 D6). */
export interface PlayerDisplayItemInfo {
  mediaID?: string;
  title?: string;
  libraryID?: string;
  artworkURL?: string;
  /** True when the caller may not see the item; only the position is shown. */
  privateItem?: boolean;
  label?: string;
  positionSeconds: number;
  durationSeconds: number;
}

/** One player display the caller controls (graph.PlayerDisplayInfo, ADR-0240 D9). */
export interface PlayerDisplayInfo {
  handle: string;
  /** Absent while the display has no live Session. */
  sessionID?: string;
  playerProfileID: string;
  displayName: string;
  presence: "ready" | "playing" | "offline";
  lastSeenAt?: string;
  currentItem?: PlayerDisplayItemInfo | null;
  inParty?: boolean;
}

/** controlRendererSession's result (graph.ControlRendererSessionResult). */
export interface ControlRendererSessionResult {
  status: string;
  sessionID: string;
  command: string;
  revision: number;
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

/** One Library the Jellyfin-compatible API may share (graph.JellyfinCompatLibraryInfo). */
export interface JellyfinCompatLibraryInfo {
  id: string;
  title: string;
  allowed: boolean;
}

/** One enabled adult Profile and its integration sign-in opt-in (graph.JellyfinCompatProfileInfo). */
export interface JellyfinCompatProfileInfo {
  id: string;
  name: string;
  integrationSignIn: boolean;
}

/** The Jellyfin-compatible API settings (graph.JellyfinCompatInfo, CORE-2683). */
export interface JellyfinCompatInfo {
  enabled: boolean;
  allowedCIDRs: string[];
  trustedProxyCIDRs: string[];
  libraries: JellyfinCompatLibraryInfo[];
  profiles: JellyfinCompatProfileInfo[];
}

/** A setJellyfinCompat patch: only the given fields change. */
export interface JellyfinCompatUpdate {
  enabled?: boolean;
  allowedCIDRs?: string[];
  trustedProxyCIDRs?: string[];
  libraryIDs?: string[];
}

/** A compatibility integration key (graph.JellyfinCompatKeyInfo, CORE-2725). No secret. */
export interface JellyfinCompatKeyInfo {
  id: string;
  profileID: string;
  profileName: string;
  name: string;
  scopes: string[];
  createdAt: string;
  lastUsedAt?: string;
  revokedAt?: string;
}

/** createJellyfinCompatKey's result: the token is returned once, here only. */
export interface CreatedJellyfinCompatKeyInfo {
  token: string;
  key: JellyfinCompatKeyInfo;
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

/**
 * One entry of a pre-play queue (graph.PrePlayItem, CORE-1701): an item a
 * client plays before a feature. `role` is "clip" (one of the Owner's clips)
 * or "suggested" (a plugin policy's pick, shown with its `label`).
 */
export interface PrePlayItem {
  mediaID: string;
  title: string;
  durationSeconds: number;
  role: string;
  label: string;
}

/** The Owner's pre-play clip list (graph.PrePlayClipsInfo). */
export interface PrePlayClipsInfo {
  items: PrePlayItem[];
}

/**
 * One webhook endpoint (graph.WebhookEndpointInfo). url is redacted to scheme
 * and host; a stored signing key, token or password shows only as secretSet.
 * problem is empty for a running endpoint, else why it cannot run.
 */
export interface WebhookEndpointInfo {
  id: string;
  name: string;
  format: string;
  url: string;
  eventKinds: string[];
  disabled: boolean;
  allowPrivateNetwork: boolean;
  secretSet: boolean;
  from: string;
  to: string[];
  username: string;
  problem: string;
}

/** The outcome of one webhook test send (graph.WebhookTestResultInfo). */
export interface WebhookTestResultInfo {
  ok: boolean;
  statusClass: string;
  diagnostic: string;
  durationMs: number;
}

/** One webhook delivery log row (graph.WebhookDeliveryInfo); at is RFC 3339. */
export interface WebhookDeliveryInfo {
  endpointID: string;
  eventKind: string;
  outcome: string;
  statusClass: string;
  attempts: number;
  durationMs: number;
  at: string;
  test: boolean;
}

/**
 * Recent server log lines for the console (graph.ServerLogsInfo, CORE-1716).
 * Lines are redacted on the server and come oldest first; available is false
 * when the server keeps no log tail.
 */
export interface ServerLogsInfo {
  available: boolean;
  capacity: number;
  lines: string[];
}

/** Shared webhook options and the console's choices (graph.WebhookSettingsInfo). */
export interface WebhookSettingsInfo {
  publicBaseURL: string;
  formats: string[];
  eventKinds: string[];
}

/** One Home shelf in the calling Profile's layout (CORE-1972). */
export interface HomeLayoutSection {
  id: string;
  kind: string;
  title: LocaleString | string;
  hidden: boolean;
}

/** The calling Profile's Home order and visibility (CORE-1972). */
export interface HomeLayoutInfo {
  customized: boolean;
  sections: HomeLayoutSection[];
}

// --- Home Studio (composable Home customization) ---

export type HomeShelfStyle = "shelf" | "grid" | "list";
export type HomeArtworkSlot = "auto" | "poster" | "landscape" | "square";
export type HomeRandomSeedMode = "" | "session" | "hourly" | "daily";
export type HomeShelfSourceType =
  | "builtin"
  | "rules"
  | "playlist"
  | "collection"
  | "saved-filter"
  | "library"
  | "entity";

export interface SmartRuleNode {
  field?: string;
  op?: string;
  value?: unknown;
  group?: SmartRuleGroup;
}

export interface SmartRuleGroup {
  match: "all" | "any" | "none";
  conditions: SmartRuleNode[];
}

export interface HomeShelfSort {
  by: string;
  desc: boolean;
  randomSeedMode?: HomeRandomSeedMode;
}

export interface HomeShelfSource {
  type: HomeShelfSourceType;
  builtinKind?: string;
  libraryIDs?: string[];
  rules?: SmartRuleGroup;
  sort?: HomeShelfSort;
  rollUpContainers?: boolean;
  playlistID?: string;
  collectionID?: string;
  savedFilterID?: string;
  entityKind?: string;
  entityID?: string;
  entityName?: string;
}

export interface HomeShelfDisplay {
  style: HomeShelfStyle;
  artworkSlot: HomeArtworkSlot;
  limit: number;
}

export interface HomeShelf {
  id: string;
  title: string;
  hidden: boolean;
  hideWhenEmpty: boolean;
  display: HomeShelfDisplay;
  source: HomeShelfSource;
}

export interface HomeHeroConfig {
  enabled: boolean;
  mode: "auto" | "shelf";
  source?: HomeShelfSource;
}

export interface HomeConfigInfo {
  customized: boolean;
  hasOwnerTemplate: boolean;
  canEditTemplate: boolean;
  hero: HomeHeroConfig;
  shelves: HomeShelf[];
}

export interface HomeConfigInput {
  version?: number;
  hero: HomeHeroConfig;
  shelves: HomeShelf[];
}

export interface HomeSchemaLibrary {
  id: string;
  title: string;
  pluginID?: string | null;
  icon?: string;
  accent?: string;
  mediaClasses?: string[];
  presentation?: PluginPresentationInfo;
  capabilities?: string[];
}

export interface HomeFilterField {
  key: string;
  label: LocaleString | string;
  valueType: "text" | "number" | "date" | "boolean" | "entity";
  group: "general" | "activity" | "metadata" | "entities";
  operators: string[];
  suggestions?: string[];
}

export interface HomePinnableTarget {
  id: string;
  name: string;
  libraryID: string;
}

export interface HomeSchemaPinnables {
  collections: HomePinnableTarget[];
  smartCollections: HomePinnableTarget[];
  savedFilters: HomePinnableTarget[];
}

export interface HomeFilterSchemaInfo {
  libraries: HomeSchemaLibrary[];
  fields: HomeFilterField[];
  pinnables: HomeSchemaPinnables;
}

export interface HomeShelfPreviewInfo {
  totalCount: number;
  items: SourceItem[];
}
