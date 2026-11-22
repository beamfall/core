// Viewer-area operations: catalog browse (sourceItems), session/profile/reveal,
// entity detail, search, playback report + markers, the My Data / Privacy
// panel, and client preferences.
//
// Every query/mutation string is copied VERBATIM from the vanilla app
// (internal/web/static/app.js) — they are contract-tested server-side, so they
// must not be "improved" or reformatted.

import { ApiError, getManifest, graphql } from "@/lib/api";
import { rebindCompatibility } from "@/lib/compat";
import { normalizePlayStats, PLAY_FIELDS, PLAY_STATS_FIELDS, utcOffsetMinutes } from "./plays";
import type {
  ClientPref,
  ControlSessionResult,
  ControlSessionVars,
  EntityFilter,
  EntityInfo,
  FieldFilters,
  GenericMediaInfo,
  ItemStateInfo,
  ItemTarget,
  LibraryInfo,
  MetadataAttribution,
  MuteInfo,
  MyDataDeleteResult,
  MyDataSummary,
  MyEngagementExport,
  MyEngagementLogPage,
  MyEngagementLogVars,
  MySessionsInfo,
  NavigationManifest,
  NotificationInfo,
  NotificationReadResult,
  PlaybackAdjustmentRanges,
  PlaybackMarker,
  PlayInfo,
  PlayStatsInfo,
  PrePlayItem,
  ProfileSessionInfo,
  ProfileSummary,
  ReportPlaybackResult,
  ReportPlaybackVars,
  RevealState,
  SearchMode,
  SearchResultResponse,
  SemanticCapabilityInfo,
  SessionInfo,
  SourceItem,
  SwitchProfileResult,
} from "./types";

// --- Navigation Manifest (GET /api/manifest) ------------------------------

/** Fetch the Navigation Manifest (the Viewer's nav + layout document). */
export function fetchManifest(signal?: AbortSignal): Promise<NavigationManifest> {
  return getManifest<NavigationManifest>(signal);
}

export type HomeLibrary = Pick<LibraryInfo, "id" | "title" | "showOnHome">;

const HOME_LIBRARIES_QUERY = "query HomeLibraries { libraries { id title showOnHome } }";

/** Returns null when Core requires an Admin Session for the visibility read. */
export async function fetchHomeLibraries(signal?: AbortSignal): Promise<HomeLibrary[] | null> {
  try {
    const data = await graphql<{ libraries: HomeLibrary[] | null }>(
      HOME_LIBRARIES_QUERY,
      {},
      signal,
      {
        adminUnlock: false,
      },
    );
    return data.libraries ?? [];
  } catch (error) {
    if (
      error instanceof ApiError &&
      error.status === 403 &&
      error.message === "admin session required"
    ) {
      return null;
    }
    throw error;
  }
}

// --- Session, profiles, reveal (ADR-0004) ---------------------------------

const CURRENT_SESSION_QUERY =
  "query CurrentSession { currentSession { deviceRole profileID profile revealed revealPinSet subtitleLanguages } }";

/** CurrentSession — the session's deviceRole, bound Profile, and reveal state. */
export async function fetchCurrentSession(signal?: AbortSignal): Promise<SessionInfo> {
  const data = await graphql<{ currentSession: SessionInfo | null }>(
    CURRENT_SESSION_QUERY,
    {},
    signal,
  );
  return (
    data.currentSession ?? {
      deviceRole: "",
      profileID: "",
      profile: "",
      revealed: false,
    }
  );
}

const PROFILES_QUERY = "query Profiles { profiles { id name kind avatar pictureURL entryPinSet } }";

/** Profiles — the profile-picker roster. */
export async function fetchProfiles(signal?: AbortSignal): Promise<ProfileSummary[]> {
  const data = await graphql<{ profiles: ProfileSummary[] | null }>(PROFILES_QUERY, {}, signal);
  return data.profiles ?? [];
}

const SWITCH_PROFILE_MUTATION =
  "mutation SwitchProfile($profileID: String!, $pin: String) { switchProfile(profileID: $profileID, pin: $pin) { profileID revealed } }";

/**
 * SwitchProfile — rebind the session to another Profile (always relocks).
 * A protected adult Profile needs its entry PIN; the PIN goes into this call and
 * nowhere else.
 */
export async function switchProfile(
  profileID: string,
  signalOrCommitted?: AbortSignal | (() => void),
  pin?: string,
): Promise<SwitchProfileResult> {
  const signal = typeof signalOrCommitted === "function" ? undefined : signalOrCommitted;
  const data = await graphql<{ switchProfile: SwitchProfileResult }>(
    SWITCH_PROFILE_MUTATION,
    pin ? { profileID, pin } : { profileID },
    signal,
  );
  if (typeof signalOrCommitted === "function") signalOrCommitted();
  await rebindCompatibility();
  return data.switchProfile;
}

const REVEAL_MUTATION = "mutation Reveal($pin: String!) { reveal(pin: $pin) { revealed } }";

/** Reveal — PIN-gate the restricted content (ADR-0004 Reveal contract). */
export async function reveal(pin: string, signal?: AbortSignal): Promise<RevealState> {
  const data = await graphql<{ reveal: RevealState }>(REVEAL_MUTATION, { pin }, signal);
  if (data.reveal.revealed) await rebindCompatibility();
  return data.reveal;
}

const SET_REVEAL_PIN_MUTATION =
  "mutation SetRevealPin($pin: String!) { setRevealPin(pin: $pin) { revealed } }";

/**
 * SetRevealPin — set or replace this Profile's Restrictions PIN (manager only,
 * CORE-2463). The PIN goes into this call and nowhere else.
 */
export async function setRevealPin(pin: string, signal?: AbortSignal): Promise<RevealState> {
  const data = await graphql<{ setRevealPin: RevealState }>(
    SET_REVEAL_PIN_MUTATION,
    { pin },
    signal,
  );
  return data.setRevealPin;
}

const RELOCK_MUTATION = "mutation Relock { relock { revealed } }";

/** Relock — drop restricted content from every view at once (no PIN). */
export async function relock(signalOrCommitted?: AbortSignal | (() => void)): Promise<RevealState> {
  const signal = typeof signalOrCommitted === "function" ? undefined : signalOrCommitted;
  const data = await graphql<{ relock: RevealState }>(RELOCK_MUTATION, {}, signal);
  if (typeof signalOrCommitted === "function") signalOrCommitted();
  await rebindCompatibility();
  return data.relock;
}

// --- Catalog: Section feeds (sourceItems) ---------------------------------

const SOURCE_ITEMS_QUERY =
  "query SourceItems($kind: String, $entityKind: String, $libraryID: String, $entityID: String, $excludeEditionGroupOf: String, $fieldFilters: FieldFilters, $entityFilter: EntityFilter, $limit: Int, $offset: Int, $home: Boolean) { sourceItems(kind: $kind, entityKind: $entityKind, libraryID: $libraryID, entityID: $entityID, excludeEditionGroupOf: $excludeEditionGroupOf, fieldFilters: $fieldFilters, entityFilter: $entityFilter, limit: $limit, offset: $offset, home: $home) { id kind title summary artwork { poster } artworkSlots locked mediaClass year certification audienceScore audienceScoreVotes byline subtitle sequence index durationSeconds container { id title artwork { poster } } engagement { positionSeconds durationSeconds completed totalCount remainingCount } } }";

/** Variables for one Section feed query (the Source context + paging cursor). */
export interface SourceItemsVars {
  kind: string;
  entityKind: string;
  libraryID: string;
  entityID: string;
  /** Omit the detail Media and its edition group before paging. */
  excludeEditionGroupOf?: string;
  /** Declared + view-time field filters merged; null when none. */
  fieldFilters: FieldFilters | null;
  /** Declared entity-relation filter, echoed whole; null when none. */
  entityFilter: EntityFilter | null;
  limit: number;
  offset: number;
  /** Home hydration: the server leaves out Libraries a manager hid from Home (CORE-2360). */
  home?: boolean;
}

/**
 * SourceItems — hydrate one Section through its Source. Graph arguments ride the
 * variables object verbatim (fieldFilters/entityFilter/entityKind echo back like
 * kind); entityID is the context entity for entity-media Sources (empty
 * elsewhere — the server ignores it then).
 */
export async function fetchSourceItems(
  vars: SourceItemsVars,
  signal?: AbortSignal,
): Promise<SourceItem[]> {
  const data = await graphql<{ sourceItems: SourceItem[] | null }>(
    SOURCE_ITEMS_QUERY,
    vars as unknown as Record<string, unknown>,
    signal,
  );
  return data.sourceItems ?? [];
}

// --- Entity detail --------------------------------------------------------

const ENTITY_QUERY =
  "query Entity($id: String!) { entity(id: $id) { id kind kindDisplayName title archetype artworkSlots fields { name type value label } relatedEntities { kind kindDisplayName kindPluralDisplayName archetype items { id kind title summary artwork { poster } artworkSlots mediaClass year byline engagement { positionSeconds durationSeconds completed totalCount remainingCount } } } } }";

/** Entity — the detail header (declared fields, server-stringified) and its related Entities. */
export async function fetchEntity(id: string, signal?: AbortSignal): Promise<EntityInfo | null> {
  const data = await graphql<{ entity: EntityInfo | null }>(ENTITY_QUERY, { id }, signal);
  return data.entity ?? null;
}

const MEDIA_QUERY =
  "query Media($id: String!) { media(id: $id) { id title mediaClass libraryID artworkSlots credits { entityID title kind kindDisplayName archetype relation attributes artworkSlots } metadataAttribution { providerName text url } } }";

/** Media — the player's now-playing read: title, artwork, linked credits and metadata credit. */
export async function fetchMedia(
  id: string,
  signal?: AbortSignal,
): Promise<GenericMediaInfo | null> {
  const data = await graphql<{ media: GenericMediaInfo | null }>(MEDIA_QUERY, { id }, signal);
  return data.media ?? null;
}

const METADATA_CREDITS_QUERY = "query MetadataCredits { metadataCredits { text url } }";

/** metadataCredits — every active scraper's declared credit, deduplicated. */
export async function fetchMetadataCredits(signal?: AbortSignal): Promise<MetadataAttribution[]> {
  const data = await graphql<{ metadataCredits: MetadataAttribution[] | null }>(
    METADATA_CREDITS_QUERY,
    {},
    signal,
  );
  return data.metadataCredits ?? [];
}

const MEDIA_FOR_ENTITY_QUERY =
  "query MediaForEntity($entityID: String!) { mediaForEntity(entityID: $entityID) { id kind title summary locked artwork { poster } artworkSlots mediaClass year certification audienceScore audienceScoreVotes byline subtitle sequence index durationSeconds engagement { positionSeconds durationSeconds completed totalCount remainingCount } } }";

/** MediaForEntity — the hand-rolled related-Media fallback list for a detail view. */
export async function fetchMediaForEntity(
  entityID: string,
  signal?: AbortSignal,
): Promise<SourceItem[]> {
  const data = await graphql<{ mediaForEntity: SourceItem[] | null }>(
    MEDIA_FOR_ENTITY_QUERY,
    { entityID },
    signal,
  );
  // Two relations to the same Media return it twice; every view lists, counts
  // and queues it once, in its first position.
  const seen = new Set<string>();
  return (data.mediaForEntity ?? []).filter((item) => {
    if (seen.has(item.id)) return false;
    seen.add(item.id);
    return true;
  });
}

const RELATED_MEDIA_QUERY =
  "query RelatedMedia($mediaID: String!, $limit: Int) { relatedMedia(mediaID: $mediaID, limit: $limit) { id kind title summary artwork { poster } artworkSlots mediaClass year certification audienceScore audienceScoreVotes byline engagement { positionSeconds durationSeconds completed totalCount remainingCount } } }";

/**
 * RelatedMedia — Media sharing a Media item's credits, ranked and bounded on the
 * server with the same Mute and visibility rules as browse (CORE-2444).
 */
export async function fetchRelatedMedia(
  mediaID: string,
  limit: number,
  signal?: AbortSignal,
): Promise<SourceItem[]> {
  const data = await graphql<{ relatedMedia: SourceItem[] | null }>(
    RELATED_MEDIA_QUERY,
    { mediaID, limit },
    signal,
  );
  return data.relatedMedia ?? [];
}

// --- Search ---------------------------------------------------------------

const SEARCH_QUERY =
  "query Search($query: String!) { search(query: $query) { id kind title summary artwork { poster } artworkSlots mediaClass year certification audienceScore audienceScoreVotes byline engagement { positionSeconds durationSeconds completed totalCount remainingCount } } }";

/** Search — keyword catalog search (reveal-filtered server-side). */
export async function search(term: string): Promise<SourceItem[]> {
  const data = await graphql<{ search: SourceItem[] | null }>(SEARCH_QUERY, {
    query: term,
  });
  return data.search ?? [];
}

const SEARCH_RESULT_QUERY =
  "query SearchResult($query: String!, $mode: SearchMode, $offset: Int, $limit: Int) { searchResult(query: $query, mode: $mode, offset: $offset, limit: $limit) { mode hasMore restrictedResultsPresent items { id kind title summary score artwork { poster } artworkSlots libraryID libraryTitle year mediaClass certification audienceScore audienceScoreVotes byline subtitle sequence index container { id title artwork { poster } } entityKind entityKindDisplayName engagement { positionSeconds durationSeconds completed totalCount remainingCount } } } }";

/**
 * searchResult — the explainable, mode-aware search (ADR-0041 #106). `mode`
 * requests keyword/semantic/auto/recommend; the response echoes the mode
 * actually served (auto/semantic degrade to keyword when unavailable) and the
 * leak-safe restrictedResultsPresent flag (no hidden title/id/count). Pass a
 * `signal` to cancel a request the user has already typed past.
 */
export async function searchResult(
  term: string,
  mode: SearchMode,
  signal?: AbortSignal,
  page?: { offset: number; limit?: number },
): Promise<SearchResultResponse> {
  const data = await graphql<{ searchResult: SearchResultResponse | null }>(
    SEARCH_RESULT_QUERY,
    { query: term, mode, ...page },
    signal,
  );
  return (
    data.searchResult ?? {
      mode: "keyword",
      items: [],
      restrictedResultsPresent: false,
    }
  );
}

const SEMANTIC_CAPABILITY_QUERY =
  "query SemanticCapability { semanticCapability { libraryID pluginActive state } }";

/**
 * fetchSemanticCapability — per-Library semantic availability so the viewer
 * exposes the semantic/auto modes only when the plugin is present (reveal-
 * filtered: a hidden Library's index state is never an oracle).
 */
export async function fetchSemanticCapability(): Promise<SemanticCapabilityInfo[]> {
  const data = await graphql<{
    semanticCapability: SemanticCapabilityInfo[] | null;
  }>(SEMANTIC_CAPABILITY_QUERY, {});
  return data.semanticCapability ?? [];
}

// --- Playback -------------------------------------------------------------

const REPORT_PLAYBACK_MUTATION =
  "mutation ReportPlayback($mediaID: ID!, $positionSeconds: Int!, $durationSeconds: Int!, $finished: Boolean!, $eventTimeMS: Int!, $paused: Boolean) { reportPlayback(mediaID: $mediaID, positionSeconds: $positionSeconds, durationSeconds: $durationSeconds, finished: $finished, eventTimeMS: $eventTimeMS, paused: $paused) { mediaID } }";

/** ReportPlayback — progress beacon (drives Continue Watching + watched state). */
export async function reportPlayback(vars: ReportPlaybackVars): Promise<ReportPlaybackResult> {
  const data = await graphql<{ reportPlayback: ReportPlaybackResult }>(
    REPORT_PLAYBACK_MUTATION,
    vars as unknown as Record<string, unknown>,
  );
  return data.reportPlayback;
}

const PLAYBACK_MARKERS_QUERY =
  "query PlaybackMarkers($mediaID: ID!) { markers(mediaID: $mediaID) { startSeconds endSeconds segmentKind title } }";

/** PlaybackMarkers — chapter/segment markers for the player transport. */
export async function fetchPlaybackMarkers(mediaID: string): Promise<PlaybackMarker[]> {
  const data = await graphql<{ markers: PlaybackMarker[] | null }>(PLAYBACK_MARKERS_QUERY, {
    mediaID,
  });
  return data.markers ?? [];
}

const PLAYBACK_ADJUSTMENTS_QUERY =
  "query PlaybackAdjustments { serverInfo { playbackAdjustments { pitchShiftSemitonesMin pitchShiftSemitonesMax tempoMin tempoMax } } }";

/**
 * PlaybackAdjustments — the pitch shift and tempo ranges the server accepts
 * (CORE-1658), or null when it advertises none: the field is absent, the read
 * fails (a server that predates it), or the ranges are not usable.
 */
export async function fetchPlaybackAdjustmentRanges(
  signal?: AbortSignal,
): Promise<PlaybackAdjustmentRanges | null> {
  try {
    const data = await graphql<{
      serverInfo: { playbackAdjustments?: PlaybackAdjustmentRanges | null } | null;
    }>(PLAYBACK_ADJUSTMENTS_QUERY, {}, signal);
    const ranges = data.serverInfo?.playbackAdjustments;
    if (!ranges) return null;
    const { pitchShiftSemitonesMin, pitchShiftSemitonesMax, tempoMin, tempoMax } = ranges;
    const usable =
      [pitchShiftSemitonesMin, pitchShiftSemitonesMax, tempoMin, tempoMax].every(Number.isFinite) &&
      pitchShiftSemitonesMin <= 0 &&
      pitchShiftSemitonesMax >= 0 &&
      tempoMin > 0 &&
      tempoMin <= 1 &&
      tempoMax >= 1 &&
      (pitchShiftSemitonesMin < pitchShiftSemitonesMax || tempoMin < tempoMax);
    return usable ? ranges : null;
  } catch {
    return null;
  }
}

// --- My Data / Privacy panel (ADR-0036) -----------------------------------

const MY_SESSIONS_QUERY =
  "query MySessions { mySessions { sessions { id deviceID deviceName deviceRole current expiresAt } streamTokens { targetKind expiresAt } } }";

/** MySessions — the user's active sessions + Stream Tokens (read-only view). */
export async function fetchMySessions(): Promise<MySessionsInfo> {
  const data = await graphql<{ mySessions: MySessionsInfo }>(MY_SESSIONS_QUERY, {});
  return data.mySessions;
}

// --- Remote control (CORE-1678) -------------------------------------------

const PROFILE_SESSIONS_QUERY =
  "query ProfileSessions { profileSessions { sessionID deviceID deviceName deviceRole current mediaID title positionSeconds durationSeconds } }";

/** ProfileSessions — the active playbacks in the bound Profile, for the remote. */
export async function fetchProfileSessions(): Promise<ProfileSessionInfo[]> {
  const data = await graphql<{ profileSessions: ProfileSessionInfo[] | null }>(
    PROFILE_SESSIONS_QUERY,
    {},
  );
  return data.profileSessions ?? [];
}

const CONTROL_SESSION_MUTATION =
  "mutation ControlSession($sessionID: String!, $command: String!, $direction: String, $positionMs: Float, $volume: Int, $trackID: String, $mediaID: String, $text: String) { controlSession(sessionID: $sessionID, command: $command, direction: $direction, positionMs: $positionMs, volume: $volume, trackID: $trackID, mediaID: $mediaID, text: $text) { sessionID command } }";

/** ControlSession — send one transport command to another session in the Profile. */
export async function controlSession(vars: ControlSessionVars): Promise<ControlSessionResult> {
  const data = await graphql<{ controlSession: ControlSessionResult }>(CONTROL_SESSION_MUTATION, {
    ...vars,
  });
  return data.controlSession;
}

const NOTIFICATIONS_QUERY =
  "query Notifications($unreadOnly: Boolean, $limit: Int) { notifications(unreadOnly: $unreadOnly, limit: $limit) { id kind title body ref createdAt read } }";

/** Notifications — reveal-filtered, per-Profile in-app inbox fallback. */
export async function fetchNotifications(
  vars: { unreadOnly?: boolean; limit?: number } = {},
): Promise<NotificationInfo[]> {
  const data = await graphql<{ notifications: NotificationInfo[] | null }>(
    NOTIFICATIONS_QUERY,
    vars as Record<string, unknown>,
  );
  return data.notifications ?? [];
}

const MARK_NOTIFICATION_READ_MUTATION =
  "mutation MarkNotificationRead($id: ID!) { markNotificationRead(id: $id) { id found } }";

/** MarkNotificationRead — idempotently marks a visible inbox row read. */
export async function markNotificationRead(id: string): Promise<NotificationReadResult> {
  const data = await graphql<{
    markNotificationRead: NotificationReadResult;
  }>(MARK_NOTIFICATION_READ_MUTATION, { id });
  return data.markNotificationRead;
}

const MY_ENGAGEMENT_QUERY =
  "query MyEngagement { myEngagement { schema exportedAt playbackProgress { mediaID positionSeconds durationSeconds watched playCount updatedAt } favorites { mediaID favoritedAt } queue { mediaID addedAt } watchlist { targetKind targetID addedAt } ratings { targetKind targetID rating ratedAt } } }";

/** MyEngagement — the download-as-JSON engagement export. */
export async function fetchMyEngagement(): Promise<MyEngagementExport> {
  const data = await graphql<{ myEngagement: MyEngagementExport }>(MY_ENGAGEMENT_QUERY, {});
  return data.myEngagement;
}

const MY_DATA_SUMMARY_QUERY =
  "query MyDataSummary { myDataSummary { plays watched favorites queueEntries libraryCount libraryBreakdown { libraryID plays } firstActivity lastActivity } }";

/** MyDataSummary — the read-computed aggregate over the caller's own data. */
export async function fetchMyDataSummary(): Promise<MyDataSummary> {
  const data = await graphql<{ myDataSummary: MyDataSummary }>(MY_DATA_SUMMARY_QUERY, {});
  return data.myDataSummary;
}

const MY_PLAY_HISTORY_QUERY = `query MyPlayHistory($limit: Int) { myPlayHistory(limit: $limit) { ${PLAY_FIELDS} } }`;

/** MyPlayHistory — the caller's own plays, newest first (CORE-1703). */
export async function fetchMyPlayHistory(limit: number, signal?: AbortSignal): Promise<PlayInfo[]> {
  const data = await graphql<{ myPlayHistory: PlayInfo[] | null }>(
    MY_PLAY_HISTORY_QUERY,
    { limit },
    signal,
  );
  return data.myPlayHistory ?? [];
}

const MY_PLAY_STATS_QUERY = `query MyPlayStats($windowDays: Int, $utcOffsetMinutes: Int) { myPlayStats(windowDays: $windowDays, utcOffsetMinutes: $utcOffsetMinutes) { ${PLAY_STATS_FIELDS} } }`;

/** MyPlayStats — the caller's own totals and breakdowns over 7, 30 or 90 days. */
export async function fetchMyPlayStats(
  windowDays: 7 | 30 | 90,
  signal?: AbortSignal,
): Promise<PlayStatsInfo> {
  const data = await graphql<{ myPlayStats: PlayStatsInfo | null }>(
    MY_PLAY_STATS_QUERY,
    { windowDays, utcOffsetMinutes: utcOffsetMinutes() },
    signal,
  );
  return normalizePlayStats(data.myPlayStats);
}

const MY_ENGAGEMENT_LOG_QUERY =
  "query MyEngagementLog($libraryID: String, $from: String, $to: String, $limit: Int, $offset: Int) { myEngagementLog(libraryID: $libraryID, from: $from, to: $to, limit: $limit, offset: $offset) { total events { kind mediaID libraryID occurredAt } } }";

/** MyEngagementLog — one paginated, Library/date-filtered page of the log. */
export async function fetchMyEngagementLog(
  vars: MyEngagementLogVars,
): Promise<MyEngagementLogPage> {
  const data = await graphql<{ myEngagementLog: MyEngagementLogPage }>(
    MY_ENGAGEMENT_LOG_QUERY,
    vars as unknown as Record<string, unknown>,
  );
  return data.myEngagementLog;
}

const DELETE_MY_ENGAGEMENT_MUTATION =
  "mutation DeleteMyEngagement($scope: String!, $pin: String) { deleteMyEngagement(scope: $scope, pin: $pin) { scope } }";

/** DeleteMyEngagement — hard-DELETE the user's own engagement events. */
export async function deleteMyEngagement(scope: string, pin = ""): Promise<MyDataDeleteResult> {
  const data = await graphql<{ deleteMyEngagement: MyDataDeleteResult }>(
    DELETE_MY_ENGAGEMENT_MUTATION,
    { scope, pin },
  );
  return data.deleteMyEngagement;
}

// --- Client preferences ---------------------------------------------------

const CLIENT_PREFS_QUERY = "query ClientPrefs { clientPrefs { key value } }";

/** ClientPrefs — the per-Profile client preference key/value pairs. */
export async function fetchClientPrefs(): Promise<ClientPref[]> {
  const data = await graphql<{ clientPrefs: ClientPref[] | null }>(CLIENT_PREFS_QUERY, {});
  return data.clientPrefs ?? [];
}

const PRE_PLAY_QUEUE_QUERY =
  "query PrePlayQueue($mediaID: String!) { prePlayQueue(mediaID: $mediaID) { mediaID title durationSeconds role label } }";

/**
 * PrePlayQueue — the items to play before `mediaID` (CORE-1701): the Owner's
 * clips, then any items the active plugin suggests. Empty unless the Profile
 * opted in and the feature is eligible.
 */
export async function fetchPrePlayQueue(
  mediaID: string,
  signal?: AbortSignal,
): Promise<PrePlayItem[]> {
  const data = await graphql<{ prePlayQueue: PrePlayItem[] | null }>(
    PRE_PLAY_QUEUE_QUERY,
    { mediaID },
    signal,
  );
  return data.prePlayQueue ?? [];
}

const SET_CLIENT_PREF_MUTATION =
  "mutation SetClientPref($key: String!, $value: String!) { setClientPref(key: $key, value: $value) { key } }";

/** SetClientPref — persist one per-Profile client preference. */
export async function setClientPref(key: string, value: string): Promise<{ key: string }> {
  const data = await graphql<{ setClientPref: { key: string } }>(SET_CLIENT_PREF_MUTATION, {
    key,
    value,
  });
  return data.setClientPref;
}

// --- Hide (per-Profile Mute) ----------------------------------------------

const MUTES_QUERY = "query Mutes { mutes { kind targetID includeDescendants mutedAt } }";

/** Mutes — the calling Profile's hidden items, Entities and Tags, newest first. */
export async function fetchMutes(signal?: AbortSignal): Promise<MuteInfo[]> {
  const data = await graphql<{ mutes: MuteInfo[] | null }>(MUTES_QUERY, {}, signal);
  return data.mutes ?? [];
}

const SET_MUTE_MUTATION =
  "mutation SetMute($kind: String!, $targetID: String!, $includeDescendants: Boolean) { setMute(kind: $kind, targetID: $targetID, includeDescendants: $includeDescendants) { muted } }";

/** SetMute — hide one Media item ("media") or an Entity and everything under it ("entity"). */
export async function setMute(kind: string, targetID: string): Promise<void> {
  await graphql<{ setMute: { muted: boolean } }>(SET_MUTE_MUTATION, {
    kind,
    targetID,
    includeDescendants: false,
  });
}

const CLEAR_MUTE_MUTATION =
  "mutation ClearMute($kind: String!, $targetID: String!) { clearMute(kind: $kind, targetID: $targetID) { muted } }";

/** ClearMute — show a hidden item again. */
export async function clearMute(kind: string, targetID: string): Promise<void> {
  await graphql<{ clearMute: { muted: boolean } }>(CLEAR_MUTE_MUTATION, { kind, targetID });
}

const CLEAR_ALL_MUTES_MUTATION = "mutation ClearAllMutes { clearAllMutes { cleared } }";

/** ClearAllMutes — show everything the calling Profile hid; returns how many. */
export async function clearAllMutes(): Promise<number> {
  const data = await graphql<{ clearAllMutes: { cleared: number } }>(CLEAR_ALL_MUTES_MUTATION, {});
  return data.clearAllMutes.cleared;
}

// --- Item actions: watched, watchlist, rating -----------------------------

const ITEM_STATE_FIELDS = "id kind watched watchlisted rating";

const ITEM_STATE_QUERY = `query ItemState($mediaID: ID, $entityID: ID) { itemState(mediaID: $mediaID, entityID: $entityID) { ${ITEM_STATE_FIELDS} } }`;

/** ItemState — the calling Profile's watched, watchlist and rating state for one item. */
export async function fetchItemState(
  target: ItemTarget,
  signal?: AbortSignal,
): Promise<ItemStateInfo> {
  const data = await graphql<{ itemState: ItemStateInfo }>(ITEM_STATE_QUERY, { ...target }, signal);
  return data.itemState;
}

const SET_WATCHED_MUTATION = `mutation SetWatched($mediaID: ID, $entityID: ID, $watched: Boolean!) { setWatched(mediaID: $mediaID, entityID: $entityID, watched: $watched) { ${ITEM_STATE_FIELDS} } }`;

/** SetWatched — mark one Media, or every Media under an Entity, watched or unwatched. */
export async function setWatched(target: ItemTarget, watched: boolean): Promise<ItemStateInfo> {
  const data = await graphql<{ setWatched: ItemStateInfo }>(SET_WATCHED_MUTATION, {
    ...target,
    watched,
  });
  return data.setWatched;
}

const SET_WATCHLISTED_MUTATION = `mutation SetWatchlisted($mediaID: ID, $entityID: ID, $watchlisted: Boolean!) { setWatchlisted(mediaID: $mediaID, entityID: $entityID, watchlisted: $watchlisted) { ${ITEM_STATE_FIELDS} } }`;

/** SetWatchlisted — add an item to, or remove it from, the calling Profile's watchlist. */
export async function setWatchlisted(
  target: ItemTarget,
  watchlisted: boolean,
): Promise<ItemStateInfo> {
  const data = await graphql<{ setWatchlisted: ItemStateInfo }>(SET_WATCHLISTED_MUTATION, {
    ...target,
    watchlisted,
  });
  return data.setWatchlisted;
}

const SET_RATING_MUTATION = `mutation SetRating($mediaID: ID, $entityID: ID, $rating: Int!) { setRating(mediaID: $mediaID, entityID: $entityID, rating: $rating) { ${ITEM_STATE_FIELDS} } }`;

/** SetRating — set the calling Profile's rating (1–10); 0 clears it. */
export async function setRating(target: ItemTarget, rating: number): Promise<ItemStateInfo> {
  const data = await graphql<{ setRating: ItemStateInfo }>(SET_RATING_MUTATION, {
    ...target,
    rating,
  });
  return data.setRating;
}
