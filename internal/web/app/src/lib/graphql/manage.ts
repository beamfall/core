// Manage-area operations: libraries, plugins, jobs, schedules, trash, artwork
// candidates, subscriptions, bulk edit / organize / undo, library health,
// field locks, importers, watcher status, and first-run setup state. These are
// the manager-gated curation surfaces (ADR-0007).
//
// Every query/mutation string is copied VERBATIM from internal/web/static/app.js
// (contract-tested server-side).

import { graphql } from "@/lib/api";
import type {
  ArtworkCandidate,
  BackupHistoryEntry,
  BackupInfo,
  BatchResultInfo,
  BulkEditChangeInfo,
  BulkOpResult,
  CollectionInfo,
  CreateLibraryResult,
  DuplicateGroupInfo,
  EntityFieldInfo,
  EntityRelationDetail,
  FieldLockInfo,
  FileChangeInfo,
  FixMatchCandidateInfo,
  FixMatchFieldDiff,
  FolderEntryInfo,
  GenericMediaInfo,
  HealthItemInfo,
  IDRef,
  IdentifyCandidate,
  ImporterInfo,
  JobInfo,
  LibraryHealthInfo,
  LibraryInfo,
  LibraryPreviewClipsResult,
  LibraryRootsResult,
  ManagedPathInfo,
  ManagedTrashInfo,
  MarkerInfo,
  MarkerInput,
  MediaFieldInput,
  MediaFileDetail,
  MediaRelationDetail,
  MetadataExportInfo,
  NavOverrideHistoryInfo,
  NavOverrideInfo,
  PathPickerInfo,
  PathPickerRole,
  PathPickerRootInfo,
  PluginSummary,
  RelationWriteResult,
  ScanLibraryResult,
  ScheduleEnabledResult,
  ScheduleInfo,
  ScheduleTriggerInput,
  SectionOverrideInfo,
  SetupState,
  SkipDetectionInfo,
  SmartCollectionInfo,
  SubscriptionInfo,
  SubscriptionRetentionResult,
  TagAssignmentInfo,
  TagBulkAssignmentInfo,
  TagBulkMode,
  TagInfo,
  TagTargetType,
  TrashEntry,
  UnmatchedItemInfo,
  UnmatchedRecordInfo,
  UnmatchedSuppressionInfo,
  VerifyBackupInfo,
  WatcherStatusInfo,
} from "./types";

// --- Libraries ------------------------------------------------------------

const LIBRARIES_QUERY =
  "query Libraries { libraries { id title pluginID previewClips restricted roots } }";

/** Libraries — the manageable Library list with roots + preview-clip toggle. */
export async function fetchLibraries(signal?: AbortSignal): Promise<LibraryInfo[]> {
  const data = await graphql<{ libraries: LibraryInfo[] | null }>(LIBRARIES_QUERY, {}, signal);
  return data.libraries ?? [];
}

const SET_LIBRARY_PREVIEW_CLIPS_MUTATION =
  "mutation SetLibraryPreviewClips($libraryID: String!, $enabled: Boolean!) { setLibraryPreviewClips(libraryID: $libraryID, enabled: $enabled) { id previewClips } }";

/** SetLibraryPreviewClips — toggle preview-clip generation for a Library. */
export async function setLibraryPreviewClips(
  libraryID: string,
  enabled: boolean,
): Promise<LibraryPreviewClipsResult> {
  const data = await graphql<{
    setLibraryPreviewClips: LibraryPreviewClipsResult;
  }>(SET_LIBRARY_PREVIEW_CLIPS_MUTATION, { libraryID, enabled });
  return data.setLibraryPreviewClips;
}

const SCAN_LIBRARY_MUTATION =
  "mutation ScanLibrary($libraryID: String!, $root: String!) { scanLibrary(libraryID: $libraryID, root: $root) { id status } }";

/** ScanLibrary — enqueue a scan of one Library root. */
export async function scanLibrary(libraryID: string, root: string): Promise<ScanLibraryResult> {
  const data = await graphql<{ scanLibrary: ScanLibraryResult }>(SCAN_LIBRARY_MUTATION, {
    libraryID,
    root,
  });
  return data.scanLibrary;
}

const SET_LIBRARY_ROOTS_MUTATION =
  "mutation SetLibraryRoots($libraryID: String!, $roots: [String!]!) { setLibraryRoots(libraryID: $libraryID, roots: $roots) { id roots } }";

/** SetLibraryRoots — replace a Library's root path list. */
export async function setLibraryRoots(
  libraryID: string,
  roots: string[],
): Promise<LibraryRootsResult> {
  const data = await graphql<{ setLibraryRoots: LibraryRootsResult }>(SET_LIBRARY_ROOTS_MUTATION, {
    libraryID,
    roots,
  });
  return data.setLibraryRoots;
}

const CREATE_LIBRARY_MUTATION =
  "mutation CreateLibrary($title: String!, $pluginID: String, $familyScope: String) { createLibrary(title: $title, pluginID: $pluginID, familyScope: $familyScope) { id title pluginID } }";

/**
 * CreateLibrary — create a Library, optionally bound to a plugin and a
 * familyScope (ADR-0028 §1: the Film & TV plugin's films/tvshows/mixed scope,
 * read by the hand-rolled resolver via trimmedVariable). familyScope is
 * ignored for plugins without scopes (Music/Podcast).
 */
export async function createLibrary(
  title: string,
  pluginID: string | null,
  familyScope?: string,
): Promise<CreateLibraryResult> {
  const data = await graphql<{ createLibrary: CreateLibraryResult }>(CREATE_LIBRARY_MUTATION, {
    title,
    pluginID,
    familyScope,
  });
  return data.createLibrary;
}

const SKIP_DETECTION_ENABLED_QUERY =
  "query SkipDetectionEnabled { skipDetectionEnabled { libraryID enabled } }";

/**
 * SkipDetectionEnabled — the per-Library skip-segment detection toggle
 * (ENR-7, ADR-0048 Decision 3/6), for every Library visible to the operator.
 */
export async function fetchSkipDetectionEnabled(
  signal?: AbortSignal,
): Promise<SkipDetectionInfo[]> {
  const data = await graphql<{ skipDetectionEnabled: SkipDetectionInfo[] | null }>(
    SKIP_DETECTION_ENABLED_QUERY,
    {},
    signal,
  );
  return data.skipDetectionEnabled ?? [];
}

const SET_SKIP_DETECTION_ENABLED_MUTATION =
  "mutation SetSkipDetectionEnabled($libraryID: String!, $enabled: Boolean!) { setSkipDetectionEnabled(libraryID: $libraryID, enabled: $enabled) { libraryID enabled } }";

/** SetSkipDetectionEnabled — toggle skip-segment detection for a Library. */
export async function setSkipDetectionEnabled(
  libraryID: string,
  enabled: boolean,
): Promise<SkipDetectionInfo> {
  const data = await graphql<{ setSkipDetectionEnabled: SkipDetectionInfo }>(
    SET_SKIP_DETECTION_ENABLED_MUTATION,
    { libraryID, enabled },
  );
  return data.setSkipDetectionEnabled;
}

// --- Plugins --------------------------------------------------------------

const PLUGINS_QUERY = "query Plugins { plugins { id displayName } }";

/** Plugins — installed plugins for Library binding (id + display label). */
export async function fetchPlugins(signal?: AbortSignal): Promise<PluginSummary[]> {
  const data = await graphql<{ plugins: PluginSummary[] | null }>(PLUGINS_QUERY, {}, signal);
  return data.plugins ?? [];
}

// --- Generic Media / Entity curation (WEB-5B) -----------------------------

const MEDIA_FIELDS_QUERY =
  "query MediaFields($mediaID: String!) { mediaFields(mediaID: $mediaID) { name type value label } }";

/** MediaFields — plugin-declared field values for one visible Media item. */
export async function fetchMediaFields(mediaID: string): Promise<EntityFieldInfo[]> {
  const data = await graphql<{ mediaFields: EntityFieldInfo[] | null }>(MEDIA_FIELDS_QUERY, {
    mediaID,
  });
  return data.mediaFields ?? [];
}

const MEDIA_FILES_QUERY =
  "query MediaFiles($mediaID: String!) { mediaFiles(mediaID: $mediaID) { mediaID path contentFingerprint versionLabel partOrdinal osHash pHash } }";

/** MediaFiles — backing Version/Part rows for one visible Media item. */
export async function fetchMediaFiles(mediaID: string): Promise<MediaFileDetail[]> {
  const data = await graphql<{ mediaFiles: MediaFileDetail[] | null }>(MEDIA_FILES_QUERY, {
    mediaID,
  });
  return data.mediaFiles ?? [];
}

const ENTITY_RELATIONS_QUERY =
  "query EntityRelations($entityID: String!, $relation: String) { entityRelations(entityID: $entityID, relation: $relation) { relation targetKind sortOrder attributes entity { id kind title fields { name type value label } } media { id title summary year favorited } } }";

/** EntityRelations — generic relation/order rows for one visible Entity. */
export async function fetchEntityRelations(
  entityID: string,
  relation?: string | null,
): Promise<EntityRelationDetail[]> {
  const data = await graphql<{
    entityRelations: EntityRelationDetail[] | null;
  }>(ENTITY_RELATIONS_QUERY, { entityID, relation });
  return data.entityRelations ?? [];
}

const MEDIA_RELATIONS_QUERY =
  "query MediaRelations($mediaID: String!, $relation: String!) { mediaRelations(mediaID: $mediaID, relation: $relation) { relation media { id title summary year favorited } } }";

/** MediaRelations — Extras/Editions-style Media relation rows. */
export async function fetchMediaRelations(
  mediaID: string,
  relation: "extra-of" | "edition-of",
): Promise<MediaRelationDetail[]> {
  const data = await graphql<{ mediaRelations: MediaRelationDetail[] | null }>(
    MEDIA_RELATIONS_QUERY,
    { mediaID, relation },
  );
  return data.mediaRelations ?? [];
}

const UPSERT_ENTITY_RELATION_MUTATION =
  "mutation UpsertEntityRelation($fromEntityID: String!, $relation: String!, $toEntityID: String, $toMediaID: String, $sortOrder: Int) { upsertEntityRelation(fromEntityID: $fromEntityID, relation: $relation, toEntityID: $toEntityID, toMediaID: $toMediaID, sortOrder: $sortOrder) { relation sourceID targetID } }";

/**
 * UpsertEntityRelation — insert or re-order one generic Entity relation row
 * (manager-gated, reveal-filtered server-side). Exactly one of toEntityID /
 * toMediaID must be set; sortOrder re-orders the relation. Relation names are
 * plugin/core vocabulary, never invented client-side (HARD invariant #1).
 */
export async function upsertEntityRelation(vars: {
  fromEntityID: string;
  relation: string;
  toEntityID?: string | null;
  toMediaID?: string | null;
  sortOrder?: number | null;
}): Promise<RelationWriteResult> {
  const data = await graphql<{ upsertEntityRelation: RelationWriteResult }>(
    UPSERT_ENTITY_RELATION_MUTATION,
    vars,
  );
  return data.upsertEntityRelation;
}

const LINK_MEDIA_RELATION_MUTATION =
  "mutation LinkMediaRelation($fromMediaID: String!, $relation: String!, $toMediaID: String!) { linkMediaRelation(fromMediaID: $fromMediaID, relation: $relation, toMediaID: $toMediaID) { relation sourceID targetID } }";

/** LinkMediaRelation — link an Extra/Edition Media to a source Media. */
export async function linkMediaRelation(
  fromMediaID: string,
  relation: "extra-of" | "edition-of",
  toMediaID: string,
): Promise<RelationWriteResult> {
  const data = await graphql<{ linkMediaRelation: RelationWriteResult }>(
    LINK_MEDIA_RELATION_MUTATION,
    { fromMediaID, relation, toMediaID },
  );
  return data.linkMediaRelation;
}

// --- Jobs -----------------------------------------------------------------

const JOBS_QUERY = "query Jobs { jobs { id kind status progress { current total unit message } } }";

/** Jobs — the job substrate queue with progress detail. */
export async function fetchJobs(signal?: AbortSignal): Promise<JobInfo[]> {
  const data = await graphql<{ jobs: JobInfo[] | null }>(JOBS_QUERY, {}, signal);
  return data.jobs ?? [];
}

// --- Schedules (ADR-0032) -------------------------------------------------

const SCHEDULES_QUERY =
  "query Schedules { schedules { id jobKind enabled orphaned nextDue lastEnqueued trigger { kind n unit weekday hour minute } } }";

/** Schedules — persisted enqueue-only triggers with their structured triggers. */
export async function fetchSchedules(): Promise<ScheduleInfo[]> {
  const data = await graphql<{ schedules: ScheduleInfo[] | null }>(SCHEDULES_QUERY, {});
  return data.schedules ?? [];
}

const UPDATE_SCHEDULE_ENABLED_MUTATION =
  "mutation UpdateSchedule($id: String!, $enabled: Boolean) { updateSchedule(id: $id, enabled: $enabled) { id enabled } }";

/** UpdateSchedule (enabled) — flip a Schedule's enabled flag. */
export async function setScheduleEnabled(
  id: string,
  enabled: boolean,
): Promise<ScheduleEnabledResult> {
  const data = await graphql<{ updateSchedule: ScheduleEnabledResult }>(
    UPDATE_SCHEDULE_ENABLED_MUTATION,
    { id, enabled },
  );
  return data.updateSchedule;
}

const UPDATE_SCHEDULE_TRIGGER_MUTATION =
  "mutation UpdateSchedule($id: String!, $trigger: ScheduleTriggerInput) { updateSchedule(id: $id, trigger: $trigger) { id jobKind enabled orphaned nextDue lastEnqueued trigger { kind n unit weekday hour minute } } }";

/** UpdateSchedule (trigger) — replace a Schedule's structured trigger. */
export async function setScheduleTrigger(
  id: string,
  trigger: ScheduleTriggerInput,
): Promise<ScheduleInfo> {
  const data = await graphql<{ updateSchedule: ScheduleInfo }>(UPDATE_SCHEDULE_TRIGGER_MUTATION, {
    id,
    trigger,
  });
  return data.updateSchedule;
}

// --- Trash (ADR-0005) -----------------------------------------------------

const TRASH_LIST_QUERY = "query TrashList { trashList { id title trashedAt } }";

/** TrashList — the server-side Trash (retention window). */
export async function fetchTrashList(): Promise<TrashEntry[]> {
  const data = await graphql<{ trashList: TrashEntry[] | null }>(TRASH_LIST_QUERY, {});
  return data.trashList ?? [];
}

const RESTORE_MEDIA_MUTATION =
  "mutation RestoreMedia($mediaID: String!) { restoreMedia(mediaID: $mediaID) { id title trashedAt } }";

/** RestoreMedia — restore one trashed Media. */
export async function restoreMedia(mediaID: string): Promise<TrashEntry[]> {
  const data = await graphql<{ restoreMedia: TrashEntry[] | null }>(RESTORE_MEDIA_MUTATION, {
    mediaID,
  });
  return data.restoreMedia ?? [];
}

const PURGE_TRASH_MUTATION =
  "mutation PurgeTrash($before: String!, $confirm: Boolean!) { purgeTrash(before: $before, confirm: $confirm) { affected } }";

/** PurgeTrash — permanently purge retained Trash before a cutoff. */
export async function purgeTrash(before: string, confirm: boolean): Promise<BulkOpResult> {
  const data = await graphql<{ purgeTrash: BulkOpResult }>(PURGE_TRASH_MUTATION, {
    before,
    confirm,
  });
  return data.purgeTrash;
}

// --- Backup + metadata export (ADR-0005 §5) -------------------------------

const BACKUP_MUTATION = "mutation Backup($dest: String!) { backup(dest: $dest) { manifest } }";

/** Backup — run a database backup to dest; returns the written manifest path. */
export async function runBackup(dest: string): Promise<BackupInfo> {
  const data = await graphql<{ backup: BackupInfo }>(BACKUP_MUTATION, { dest });
  return data.backup;
}

const BACKUP_HISTORY_QUERY =
  "query BackupHistory($dest: String!) { backupHistory(dest: $dest) { directory createdAt sizeBytes valid error } }";

/**
 * BackupHistory — the published backup sets under dest, newest first, each with
 * its confined integrity result (MIGREV-1). dest is guarded to the sanctioned
 * backup allowlist by the store.
 */
export async function fetchBackupHistory(
  dest: string,
  signal?: AbortSignal,
): Promise<BackupHistoryEntry[]> {
  const data = await graphql<{ backupHistory: BackupHistoryEntry[] }>(
    BACKUP_HISTORY_QUERY,
    { dest },
    signal,
  );
  return data.backupHistory;
}

const VERIFY_BACKUP_MUTATION =
  "mutation VerifyBackup($manifest: String!) { verifyBackup(manifest: $manifest) { ok error } }";

/**
 * VerifyBackup — re-run confined integrity verification over a published set,
 * identified by its manifest path (MIGREV-1). Returns pass/fail plus a sanitized
 * reason; it never restores or mutates the set.
 */
export async function verifyBackup(manifest: string): Promise<VerifyBackupInfo> {
  const data = await graphql<{ verifyBackup: VerifyBackupInfo }>(VERIFY_BACKUP_MUTATION, {
    manifest,
  });
  return data.verifyBackup;
}

const METADATA_EXPORT_QUERY =
  "query MetadataExport { metadataExport { schema exportedAt libraries { id pluginID restricted title } media { artworkPins { pluginID scraperID slot sourceURL } class codec container durationSeconds fields { label name type value } id libraryID markers { endSeconds id segmentKind startSeconds tags title } mutatedBy summary title year } entities { fields { label name type value } id kind libraryID title } relations { attributes fromEntityID relation toEntityID toMediaID } mediaLinks { attributes fromMediaID relation toMediaID } collections { createdAt id libraryID name sourceExpr } } }";

/** MetadataExport — the full reveal-filtered portability export (ADR-0005 §5). */
export async function fetchMetadataExport(signal?: AbortSignal): Promise<MetadataExportInfo> {
  const data = await graphql<{ metadataExport: MetadataExportInfo }>(
    METADATA_EXPORT_QUERY,
    {},
    signal,
  );
  return data.metadataExport;
}

// --- Tags (ADR-0061 / WEB-5D substrate) ----------------------------------

const TAG_FIELDS = "id name parentTagID trashedAt createdAt updatedAt assignmentCount";

const TAGS_QUERY =
  "query Tags($includeTrashed: Boolean) { tags(includeTrashed: $includeTrashed) { " +
  TAG_FIELDS +
  " } }";

/** Tags — first-class Core Tags, optionally including recoverable deleted Tags. */
export async function fetchTags(includeTrashed = false): Promise<TagInfo[]> {
  const data = await graphql<{ tags: TagInfo[] | null }>(TAGS_QUERY, {
    includeTrashed,
  });
  return data.tags ?? [];
}

const TAG_ASSIGNMENTS_QUERY =
  "query TagAssignments($targetType: String!, $targetID: String!) { tagAssignments(targetType: $targetType, targetID: $targetID) { targetType targetID libraryID tags { " +
  TAG_FIELDS +
  " } } }";

/** TagAssignments — current first-class Tags for a Media, Entity, or Marker. */
export async function fetchTagAssignments(
  targetType: TagTargetType,
  targetID: string,
): Promise<TagAssignmentInfo> {
  const data = await graphql<{ tagAssignments: TagAssignmentInfo }>(TAG_ASSIGNMENTS_QUERY, {
    targetType,
    targetID,
  });
  return data.tagAssignments;
}

const CREATE_TAG_MUTATION =
  "mutation CreateTag($name: String!, $parentTagID: String) { createTag(name: $name, parentTagID: $parentTagID) { " +
  TAG_FIELDS +
  " } }";

/** CreateTag — create one first-class Core Tag. */
export async function createTag(name: string, parentTagID?: string): Promise<TagInfo> {
  const data = await graphql<{ createTag: TagInfo }>(CREATE_TAG_MUTATION, {
    name,
    parentTagID,
  });
  return data.createTag;
}

const UPDATE_TAG_MUTATION =
  "mutation UpdateTag($tagID: String!, $name: String!, $parentTagID: String) { updateTag(tagID: $tagID, name: $name, parentTagID: $parentTagID) { " +
  TAG_FIELDS +
  " } }";

/** UpdateTag — rename and/or reparent one live Tag. */
export async function updateTag(
  tagID: string,
  name: string,
  parentTagID?: string,
): Promise<TagInfo> {
  const data = await graphql<{ updateTag: TagInfo }>(UPDATE_TAG_MUTATION, {
    tagID,
    name,
    parentTagID,
  });
  return data.updateTag;
}

const DELETE_TAG_MUTATION =
  "mutation DeleteTag($tagID: String!) { deleteTag(tagID: $tagID) { " + TAG_FIELDS + " } }";

/** DeleteTag — soft-delete one Tag so it can be restored. */
export async function deleteTag(tagID: string): Promise<TagInfo> {
  const data = await graphql<{ deleteTag: TagInfo }>(DELETE_TAG_MUTATION, {
    tagID,
  });
  return data.deleteTag;
}

const RESTORE_TAG_MUTATION =
  "mutation RestoreTag($tagID: String!) { restoreTag(tagID: $tagID) { " + TAG_FIELDS + " } }";

/** RestoreTag — restore one soft-deleted Tag. */
export async function restoreTag(tagID: string): Promise<TagInfo> {
  const data = await graphql<{ restoreTag: TagInfo }>(RESTORE_TAG_MUTATION, {
    tagID,
  });
  return data.restoreTag;
}

const ASSIGN_TAG_MUTATION =
  "mutation AssignTag($tagID: String!, $targetType: String!, $targetID: String!) { assignTag(tagID: $tagID, targetType: $targetType, targetID: $targetID) { targetType targetID libraryID tags { " +
  TAG_FIELDS +
  " } } }";

/** AssignTag — attach one Tag to one Media, Entity, or Marker. */
export async function assignTag(
  tagID: string,
  targetType: TagTargetType,
  targetID: string,
): Promise<TagAssignmentInfo> {
  const data = await graphql<{ assignTag: TagAssignmentInfo }>(ASSIGN_TAG_MUTATION, {
    tagID,
    targetType,
    targetID,
  });
  return data.assignTag;
}

const UNASSIGN_TAG_MUTATION =
  "mutation UnassignTag($tagID: String!, $targetType: String!, $targetID: String!) { unassignTag(tagID: $tagID, targetType: $targetType, targetID: $targetID) { targetType targetID libraryID tags { " +
  TAG_FIELDS +
  " } } }";

/** UnassignTag — detach one Tag from one Media, Entity, or Marker. */
export async function unassignTag(
  tagID: string,
  targetType: TagTargetType,
  targetID: string,
): Promise<TagAssignmentInfo> {
  const data = await graphql<{ unassignTag: TagAssignmentInfo }>(UNASSIGN_TAG_MUTATION, {
    tagID,
    targetType,
    targetID,
  });
  return data.unassignTag;
}

const BULK_ASSIGN_TAGS_MUTATION =
  "mutation BulkAssignTags($tagIDs: [String!]!, $targetType: String!, $targetIDs: [String!]!, $mode: String!) { bulkAssignTags(tagIDs: $tagIDs, targetType: $targetType, targetIDs: $targetIDs, mode: $mode) { affected tags { " +
  TAG_FIELDS +
  " } } }";

/** BulkAssignTags — add/remove/replace Tags across selected targets. */
export async function bulkAssignTags(
  tagIDs: string[],
  targetType: TagTargetType,
  targetIDs: string[],
  mode: TagBulkMode,
): Promise<TagBulkAssignmentInfo> {
  const data = await graphql<{ bulkAssignTags: TagBulkAssignmentInfo }>(BULK_ASSIGN_TAGS_MUTATION, {
    tagIDs,
    targetType,
    targetIDs,
    mode,
  });
  return data.bulkAssignTags;
}

// --- Markers (ADR-0033 / ADR-0048 / ADR-0061) -----------------------------

const MARKER_FIELDS = "id mediaID title startSeconds endSeconds segmentKind tags trashedAt";

const MARKERS_QUERY =
  "query Markers($mediaID: String!) { markers(mediaID: $mediaID) { " + MARKER_FIELDS + " } }";

/** Markers — live Marker rows for one visible Media item. */
export async function fetchMarkers(mediaID: string): Promise<MarkerInfo[]> {
  const data = await graphql<{ markers: MarkerInfo[] | null }>(MARKERS_QUERY, { mediaID });
  return data.markers ?? [];
}

const TRASHED_MARKERS_QUERY =
  "query TrashedMarkers($mediaID: String!) { trashedMarkers(mediaID: $mediaID) { " +
  MARKER_FIELDS +
  " } }";

/** TrashedMarkers — soft-deleted Marker rows available for restore. */
export async function fetchTrashedMarkers(mediaID: string): Promise<MarkerInfo[]> {
  const data = await graphql<{ trashedMarkers: MarkerInfo[] | null }>(TRASHED_MARKERS_QUERY, {
    mediaID,
  });
  return data.trashedMarkers ?? [];
}

const CREATE_MARKER_MUTATION =
  "mutation CreateMarker($mediaID: String!, $title: String, $startSeconds: Float!, $endSeconds: Float, $segmentKind: String, $tags: [String!]) { createMarker(mediaID: $mediaID, title: $title, startSeconds: $startSeconds, endSeconds: $endSeconds, segmentKind: $segmentKind, tags: $tags) { " +
  MARKER_FIELDS +
  " } }";

/** CreateMarker — create a point/range Marker on one Media item. */
export async function createMarker(mediaID: string, input: MarkerInput): Promise<MarkerInfo> {
  const data = await graphql<{ createMarker: MarkerInfo }>(CREATE_MARKER_MUTATION, {
    mediaID,
    ...input,
  });
  return data.createMarker;
}

const UPDATE_MARKER_MUTATION =
  "mutation UpdateMarker($markerID: String!, $title: String, $startSeconds: Float!, $endSeconds: Float, $segmentKind: String, $tags: [String!]) { updateMarker(markerID: $markerID, title: $title, startSeconds: $startSeconds, endSeconds: $endSeconds, segmentKind: $segmentKind, tags: $tags) { " +
  MARKER_FIELDS +
  " } }";

/** UpdateMarker — replace one Marker's editable fields. */
export async function updateMarker(markerID: string, input: MarkerInput): Promise<MarkerInfo> {
  const data = await graphql<{ updateMarker: MarkerInfo }>(UPDATE_MARKER_MUTATION, {
    markerID,
    ...input,
  });
  return data.updateMarker;
}

const DELETE_MARKER_MUTATION =
  "mutation DeleteMarker($markerID: String!) { deleteMarker(markerID: $markerID) { " +
  MARKER_FIELDS +
  " } }";

/** DeleteMarker — soft-delete one Marker so it can be restored. */
export async function deleteMarker(markerID: string): Promise<MarkerInfo> {
  const data = await graphql<{ deleteMarker: MarkerInfo }>(DELETE_MARKER_MUTATION, { markerID });
  return data.deleteMarker;
}

const RESTORE_MARKER_MUTATION =
  "mutation RestoreMarker($markerID: String!) { restoreMarker(markerID: $markerID) { " +
  MARKER_FIELDS +
  " } }";

/** RestoreMarker — restore one soft-deleted Marker. */
export async function restoreMarker(markerID: string): Promise<MarkerInfo> {
  const data = await graphql<{ restoreMarker: MarkerInfo }>(RESTORE_MARKER_MUTATION, { markerID });
  return data.restoreMarker;
}

// --- Artwork candidates (ADR-0009) ----------------------------------------

const ARTWORK_CANDIDATES_QUERY =
  "query ArtworkCandidates($mediaID: String!, $slot: String!) { artworkCandidates(mediaID: $mediaID, slot: $slot) { id slot applied pluginID scraperID previewURL } }";

/** ArtworkCandidates — the candidate artwork for one Media slot. */
export async function fetchArtworkCandidates(
  mediaID: string,
  slot: string,
): Promise<ArtworkCandidate[]> {
  const data = await graphql<{ artworkCandidates: ArtworkCandidate[] | null }>(
    ARTWORK_CANDIDATES_QUERY,
    { mediaID, slot },
  );
  return data.artworkCandidates ?? [];
}

const PIN_ARTWORK_CANDIDATE_MUTATION =
  "mutation PinArtworkCandidate($mediaID: String!, $slot: String!, $candidateID: String!) { pinArtworkCandidate(mediaID: $mediaID, slot: $slot, candidateID: $candidateID) { id slot applied pluginID scraperID previewURL } }";

/** PinArtworkCandidate — pin one candidate as the applied artwork for a slot. */
export async function pinArtworkCandidate(
  mediaID: string,
  slot: string,
  candidateID: string,
): Promise<ArtworkCandidate> {
  const data = await graphql<{ pinArtworkCandidate: ArtworkCandidate }>(
    PIN_ARTWORK_CANDIDATE_MUTATION,
    { mediaID, slot, candidateID },
  );
  return data.pinArtworkCandidate;
}

// --- Subscriptions (ADR-0030) ---------------------------------------------

const SUBSCRIPTIONS_QUERY =
  "query Subscriptions($libraryID: String!) { subscriptions(libraryID: $libraryID) { id feedURL title root retentionKind retentionN } }";

/** Subscriptions — the feed Subscriptions for one Library. */
export async function fetchSubscriptions(libraryID: string): Promise<SubscriptionInfo[]> {
  const data = await graphql<{ subscriptions: SubscriptionInfo[] | null }>(SUBSCRIPTIONS_QUERY, {
    libraryID,
  });
  return data.subscriptions ?? [];
}

const SET_SUBSCRIPTION_RETENTION_MUTATION =
  "mutation SetSubscriptionRetention($id: String!, $kind: String!, $n: Int) { setSubscriptionRetention(id: $id, kind: $kind, n: $n) { id retentionKind retentionN } }";

/** SetSubscriptionRetention — set a Subscription's retention policy. */
export async function setSubscriptionRetention(
  id: string,
  kind: string,
  n: number | null,
): Promise<SubscriptionRetentionResult> {
  const data = await graphql<{
    setSubscriptionRetention: SubscriptionRetentionResult;
  }>(SET_SUBSCRIPTION_RETENTION_MUTATION, { id, kind, n });
  return data.setSubscriptionRetention;
}

const REMOVE_SUBSCRIPTION_MUTATION =
  "mutation RemoveSubscription($id: String!) { removeSubscription(id: $id) { id } }";

/** RemoveSubscription — remove one feed Subscription. */
export async function removeSubscription(id: string): Promise<IDRef> {
  const data = await graphql<{ removeSubscription: IDRef }>(REMOVE_SUBSCRIPTION_MUTATION, { id });
  return data.removeSubscription;
}

const ADD_SUBSCRIPTION_MUTATION =
  "mutation AddSubscription($libraryID: String!, $feedURL: String!, $root: String, $authHeader: String) { addSubscription(libraryID: $libraryID, feedURL: $feedURL, root: $root, authHeader: $authHeader) { id } }";

/** AddSubscription — add a feed Subscription to a Library. */
export async function addSubscription(vars: {
  libraryID: string;
  feedURL: string;
  root?: string | null;
  authHeader?: string | null;
}): Promise<IDRef> {
  const data = await graphql<{ addSubscription: IDRef }>(ADD_SUBSCRIPTION_MUTATION, vars);
  return data.addSubscription;
}

// --- Collections / Smart Collections (ADR-0001 / ADR-0061) ---------------

const SMART_COLLECTION_FIELDS = "id libraryID name sourceExpr sortExpr createdAt";
const COLLECTION_FIELDS =
  "id libraryID parentID name sortOrder trashedAt createdAt updatedAt memberCount members { mediaID sortOrder addedAt }";

const SMART_COLLECTIONS_QUERY =
  "query SmartCollections($libraryID: String!) { smartCollections(libraryID: $libraryID) { " +
  SMART_COLLECTION_FIELDS +
  " } }";

export async function fetchSmartCollections(libraryID: string): Promise<SmartCollectionInfo[]> {
  const data = await graphql<{ smartCollections: SmartCollectionInfo[] | null }>(
    SMART_COLLECTIONS_QUERY,
    { libraryID },
  );
  return data.smartCollections ?? [];
}

const CREATE_SMART_COLLECTION_MUTATION =
  "mutation CreateSmartCollection($libraryID: String!, $name: String!, $sourceExpr: String!, $sortExpr: String) { createSmartCollection(libraryID: $libraryID, name: $name, sourceExpr: $sourceExpr, sortExpr: $sortExpr) { " +
  SMART_COLLECTION_FIELDS +
  " } }";

export async function createSmartCollection(vars: {
  libraryID: string;
  name: string;
  sourceExpr: string;
  sortExpr?: string | null;
}): Promise<SmartCollectionInfo> {
  const data = await graphql<{ createSmartCollection: SmartCollectionInfo }>(
    CREATE_SMART_COLLECTION_MUTATION,
    vars,
  );
  return data.createSmartCollection;
}

const UPDATE_SMART_COLLECTION_MUTATION =
  "mutation UpdateSmartCollection($id: String!, $name: String, $sourceExpr: String, $sortExpr: String) { updateSmartCollection(id: $id, name: $name, sourceExpr: $sourceExpr, sortExpr: $sortExpr) { " +
  SMART_COLLECTION_FIELDS +
  " } }";

export async function updateSmartCollection(vars: {
  id: string;
  name?: string | null;
  sourceExpr?: string | null;
  sortExpr?: string | null;
}): Promise<SmartCollectionInfo> {
  const data = await graphql<{ updateSmartCollection: SmartCollectionInfo }>(
    UPDATE_SMART_COLLECTION_MUTATION,
    vars,
  );
  return data.updateSmartCollection;
}

const DELETE_SMART_COLLECTION_MUTATION =
  "mutation DeleteSmartCollection($id: String!) { deleteSmartCollection(id: $id) { " +
  SMART_COLLECTION_FIELDS +
  " } }";

export async function deleteSmartCollection(id: string): Promise<SmartCollectionInfo> {
  const data = await graphql<{ deleteSmartCollection: SmartCollectionInfo }>(
    DELETE_SMART_COLLECTION_MUTATION,
    { id },
  );
  return data.deleteSmartCollection;
}

const COLLECTIONS_QUERY =
  "query Collections($libraryID: String!, $includeTrashed: Boolean) { collections(libraryID: $libraryID, includeTrashed: $includeTrashed) { " +
  COLLECTION_FIELDS +
  " } }";

export async function fetchCollections(
  libraryID: string,
  includeTrashed = false,
): Promise<CollectionInfo[]> {
  const data = await graphql<{ collections: CollectionInfo[] | null }>(COLLECTIONS_QUERY, {
    libraryID,
    includeTrashed,
  });
  return data.collections ?? [];
}

const CREATE_COLLECTION_MUTATION =
  "mutation CreateCollection($libraryID: String!, $name: String!, $parentID: String, $sortOrder: Int) { createCollection(libraryID: $libraryID, name: $name, parentID: $parentID, sortOrder: $sortOrder) { " +
  COLLECTION_FIELDS +
  " } }";

export async function createCollection(vars: {
  libraryID: string;
  name: string;
  parentID?: string | null;
  sortOrder?: number | null;
}): Promise<CollectionInfo> {
  const data = await graphql<{ createCollection: CollectionInfo }>(
    CREATE_COLLECTION_MUTATION,
    vars,
  );
  return data.createCollection;
}

const UPDATE_COLLECTION_MUTATION =
  "mutation UpdateCollection($id: String!, $name: String, $parentID: String, $sortOrder: Int) { updateCollection(id: $id, name: $name, parentID: $parentID, sortOrder: $sortOrder) { " +
  COLLECTION_FIELDS +
  " } }";

export async function updateCollection(vars: {
  id: string;
  name?: string | null;
  parentID?: string | null;
  sortOrder?: number | null;
}): Promise<CollectionInfo> {
  const data = await graphql<{ updateCollection: CollectionInfo }>(
    UPDATE_COLLECTION_MUTATION,
    vars,
  );
  return data.updateCollection;
}

const DELETE_COLLECTION_MUTATION =
  "mutation DeleteCollection($id: String!) { deleteCollection(id: $id) { " +
  COLLECTION_FIELDS +
  " } }";

export async function deleteCollection(id: string): Promise<CollectionInfo> {
  const data = await graphql<{ deleteCollection: CollectionInfo }>(DELETE_COLLECTION_MUTATION, {
    id,
  });
  return data.deleteCollection;
}

const RESTORE_COLLECTION_MUTATION =
  "mutation RestoreCollection($id: String!) { restoreCollection(id: $id) { " +
  COLLECTION_FIELDS +
  " } }";

export async function restoreCollection(id: string): Promise<CollectionInfo> {
  const data = await graphql<{ restoreCollection: CollectionInfo }>(RESTORE_COLLECTION_MUTATION, {
    id,
  });
  return data.restoreCollection;
}

const ADD_COLLECTION_MEDIA_MUTATION =
  "mutation AddCollectionMedia($collectionID: String!, $mediaIDs: [String!]!) { addCollectionMedia(collectionID: $collectionID, mediaIDs: $mediaIDs) { " +
  COLLECTION_FIELDS +
  " } }";

export async function addCollectionMedia(
  collectionID: string,
  mediaIDs: string[],
): Promise<CollectionInfo> {
  const data = await graphql<{ addCollectionMedia: CollectionInfo }>(
    ADD_COLLECTION_MEDIA_MUTATION,
    { collectionID, mediaIDs },
  );
  return data.addCollectionMedia;
}

const REMOVE_COLLECTION_MEDIA_MUTATION =
  "mutation RemoveCollectionMedia($collectionID: String!, $mediaIDs: [String!]!) { removeCollectionMedia(collectionID: $collectionID, mediaIDs: $mediaIDs) { " +
  COLLECTION_FIELDS +
  " } }";

export async function removeCollectionMedia(
  collectionID: string,
  mediaIDs: string[],
): Promise<CollectionInfo> {
  const data = await graphql<{ removeCollectionMedia: CollectionInfo }>(
    REMOVE_COLLECTION_MEDIA_MUTATION,
    { collectionID, mediaIDs },
  );
  return data.removeCollectionMedia;
}

const REORDER_COLLECTION_MEDIA_MUTATION =
  "mutation ReorderCollectionMedia($collectionID: String!, $mediaIDs: [String!]!) { reorderCollectionMedia(collectionID: $collectionID, mediaIDs: $mediaIDs) { " +
  COLLECTION_FIELDS +
  " } }";

export async function reorderCollectionMedia(
  collectionID: string,
  mediaIDs: string[],
): Promise<CollectionInfo> {
  const data = await graphql<{ reorderCollectionMedia: CollectionInfo }>(
    REORDER_COLLECTION_MEDIA_MUTATION,
    { collectionID, mediaIDs },
  );
  return data.reorderCollectionMedia;
}

// --- Bulk edit / organize / undo (ADR-0042 bulk ops) ----------------------

const BULK_EDIT_PREVIEW_QUERY =
  "query BulkEditPreview($mediaIDs: [String!]!, $fields: [MediaFieldInput!]!) { bulkEditPreview(mediaIDs: $mediaIDs, fields: $fields) { mediaID libraryID title old { name type value } new { name type value } } }";

/** BulkEditPreview — preview a typed-field edit across many Media. */
export async function bulkEditPreview(
  mediaIDs: string[],
  fields: MediaFieldInput[],
): Promise<BulkEditChangeInfo[]> {
  const data = await graphql<{ bulkEditPreview: BulkEditChangeInfo[] | null }>(
    BULK_EDIT_PREVIEW_QUERY,
    { mediaIDs, fields },
  );
  return data.bulkEditPreview ?? [];
}

const BULK_EDIT_APPLY_MUTATION =
  "mutation BulkEditApply($mediaIDs: [String!]!, $fields: [MediaFieldInput!]!) { bulkEditApply(mediaIDs: $mediaIDs, fields: $fields) { batchID kind affected } }";

/** BulkEditApply — apply a typed-field edit across many Media. */
export async function bulkEditApply(
  mediaIDs: string[],
  fields: MediaFieldInput[],
): Promise<BatchResultInfo> {
  const data = await graphql<{ bulkEditApply: BatchResultInfo }>(BULK_EDIT_APPLY_MUTATION, {
    mediaIDs,
    fields,
  });
  return data.bulkEditApply;
}

const ORGANIZE_PREVIEW_QUERY =
  "query OrganizePreview($libraryID: String!, $template: String!) { organizePreview(libraryID: $libraryID, template: $template) { mediaID title oldPath newPath } }";

/** OrganizePreview — preview a template-driven rename/move for a Library. */
export async function organizePreview(
  libraryID: string,
  template: string,
): Promise<FileChangeInfo[]> {
  const data = await graphql<{ organizePreview: FileChangeInfo[] | null }>(ORGANIZE_PREVIEW_QUERY, {
    libraryID,
    template,
  });
  return data.organizePreview ?? [];
}

const FOLDER_TREE_QUERY =
  "query FolderTree($libraryID: String!, $root: String, $depth: Int) { folderTree(libraryID: $libraryID, root: $root, depth: $depth) { name path kind size modifiedAt children { name path kind size modifiedAt children { name path kind size modifiedAt children { name path kind size modifiedAt } } } } }";

/** FolderTree — bounded server folder/file tree rooted in a Library root. */
export async function fetchFolderTree(
  libraryID: string,
  root?: string,
  depth = 2,
): Promise<FolderEntryInfo> {
  const data = await graphql<{ folderTree: FolderEntryInfo }>(FOLDER_TREE_QUERY, {
    libraryID,
    root: root || null,
    depth,
  });
  return data.folderTree;
}

const BROWSE_SERVER_FOLDERS_QUERY =
  "query BrowseServerFolders($path: String, $depth: Int) { browseServerFolders(path: $path, depth: $depth) { name path kind children { name path kind } } }";

/**
 * BrowseServerFolders (WEB-6) — directories-only browse of the server
 * filesystem for the first-run setup folder picker. Unlike folderTree this is
 * NOT Library-scoped (no Library exists yet at setup); it is manager-gated. An
 * empty/undefined path lists the filesystem root.
 */
export async function browseServerFolders(path?: string, depth = 1): Promise<FolderEntryInfo> {
  const data = await graphql<{ browseServerFolders: FolderEntryInfo }>(
    BROWSE_SERVER_FOLDERS_QUERY,
    { path: path || null, depth },
  );
  return data.browseServerFolders;
}

const PATH_PICKER_QUERY =
  "query PathPicker($role: String!, $path: String, $libraryID: String, $distributionClass: String, $depth: Int) { pathPicker(role: $role, path: $path, libraryID: $libraryID, distributionClass: $distributionClass, depth: $depth) { runtime roles role purposeKey previewTimeoutMs maxPreviewEntries roots { name path entryClass } entries { name path kind accessError entryClass selectable reasonCode mediaCount sampleNames previewTimeoutMs maxPreviewEntries truncated children { name path kind accessError entryClass selectable reasonCode mediaCount sampleNames previewTimeoutMs maxPreviewEntries truncated } } } }";

export async function fetchPathPicker(options: {
  role: PathPickerRole;
  path?: string;
  libraryID?: string;
  distributionClass?: string;
  depth?: number;
}): Promise<PathPickerInfo> {
  const data = await graphql<{ pathPicker: PathPickerInfo }>(PATH_PICKER_QUERY, {
    role: options.role,
    path: options.path || null,
    libraryID: options.libraryID || null,
    distributionClass: options.distributionClass || null,
    depth: options.depth ?? 1,
  });
  return data.pathPicker;
}

const ADD_PATH_PICKER_ROOT_MUTATION =
  "mutation AddPathPickerRoot($path: String!) { addPathPickerRoot(path: $path) { name path entryClass } }";

export async function addPathPickerRoot(path: string): Promise<PathPickerRootInfo> {
  const data = await graphql<{ addPathPickerRoot: PathPickerRootInfo }>(
    ADD_PATH_PICKER_ROOT_MUTATION,
    { path },
  );
  return data.addPathPickerRoot;
}

const CREATE_MANAGED_FOLDER_MUTATION =
  "mutation CreateManagedFolder($libraryID: String!, $parentPath: String!, $name: String!) { createManagedFolder(libraryID: $libraryID, parentPath: $parentPath, name: $name) { path kind } }";

/** CreateManagedFolder — create one folder under a managed Library root. */
export async function createManagedFolder(
  libraryID: string,
  parentPath: string,
  name: string,
): Promise<ManagedPathInfo> {
  const data = await graphql<{ createManagedFolder: ManagedPathInfo }>(
    CREATE_MANAGED_FOLDER_MUTATION,
    { libraryID, parentPath, name },
  );
  return data.createManagedFolder;
}

const RENAME_MANAGED_PATH_MUTATION =
  "mutation RenameManagedPath($libraryID: String!, $path: String!, $name: String!) { renameManagedPath(libraryID: $libraryID, path: $path, name: $name) { path kind } }";

/** RenameManagedPath — rename one managed folder/file in place. */
export async function renameManagedPath(
  libraryID: string,
  path: string,
  name: string,
): Promise<ManagedPathInfo> {
  const data = await graphql<{ renameManagedPath: ManagedPathInfo }>(RENAME_MANAGED_PATH_MUTATION, {
    libraryID,
    path,
    name,
  });
  return data.renameManagedPath;
}

const MOVE_MANAGED_PATH_MUTATION =
  "mutation MoveManagedPath($libraryID: String!, $path: String!, $destinationParentPath: String!) { moveManagedPath(libraryID: $libraryID, path: $path, destinationParentPath: $destinationParentPath) { path kind } }";

/** MoveManagedPath — move one managed folder/file to another folder. */
export async function moveManagedPath(
  libraryID: string,
  path: string,
  destinationParentPath: string,
): Promise<ManagedPathInfo> {
  const data = await graphql<{ moveManagedPath: ManagedPathInfo }>(MOVE_MANAGED_PATH_MUTATION, {
    libraryID,
    path,
    destinationParentPath,
  });
  return data.moveManagedPath;
}

const DELETE_MANAGED_PATH_MUTATION =
  "mutation DeleteManagedPath($libraryID: String!, $path: String!) { deleteManagedPath(libraryID: $libraryID, path: $path) { trashID originalPath kind } }";

/** DeleteManagedPath — recoverably delete one managed folder/file. */
export async function deleteManagedPath(
  libraryID: string,
  path: string,
): Promise<ManagedTrashInfo> {
  const data = await graphql<{ deleteManagedPath: ManagedTrashInfo }>(
    DELETE_MANAGED_PATH_MUTATION,
    { libraryID, path },
  );
  return data.deleteManagedPath;
}

const RESTORE_MANAGED_PATH_MUTATION =
  "mutation RestoreManagedPath($libraryID: String!, $trashID: String!) { restoreManagedPath(libraryID: $libraryID, trashID: $trashID) { path kind } }";

/** RestoreManagedPath — restore one managed folder/file delete by trash id. */
export async function restoreManagedPath(
  libraryID: string,
  trashID: string,
): Promise<ManagedPathInfo> {
  const data = await graphql<{ restoreManagedPath: ManagedPathInfo }>(
    RESTORE_MANAGED_PATH_MUTATION,
    { libraryID, trashID },
  );
  return data.restoreManagedPath;
}

const ORGANIZE_APPLY_MUTATION =
  "mutation OrganizeApply($libraryID: String!, $template: String!) { organizeApply(libraryID: $libraryID, template: $template) { batchID kind affected } }";

/** OrganizeApply — apply a template-driven rename/move for a Library. */
export async function organizeApply(libraryID: string, template: string): Promise<BatchResultInfo> {
  const data = await graphql<{ organizeApply: BatchResultInfo }>(ORGANIZE_APPLY_MUTATION, {
    libraryID,
    template,
  });
  return data.organizeApply;
}

const UNDO_BATCH_MUTATION =
  "mutation UndoBatch($batchID: String!) { undoBatch(batchID: $batchID) { batchID kind affected } }";

/** UndoBatch — roll back a previously applied bulk-edit/organize batch. */
export async function undoBatch(batchID: string): Promise<BatchResultInfo> {
  const data = await graphql<{ undoBatch: BatchResultInfo }>(UNDO_BATCH_MUTATION, { batchID });
  return data.undoBatch;
}

// --- Library health (ADR-0042) --------------------------------------------

const LIBRARY_HEALTH_QUERY = "query LibraryHealth { libraryHealth { libraryID title counts } }";

/** LibraryHealth — the per-Library health dimension counts. */
export async function fetchLibraryHealth(signal?: AbortSignal): Promise<LibraryHealthInfo[]> {
  const data = await graphql<{ libraryHealth: LibraryHealthInfo[] | null }>(
    LIBRARY_HEALTH_QUERY,
    {},
    signal,
  );
  return data.libraryHealth ?? [];
}

/**
 * BulkOp — run one health bulk operation for a Library. The mutation field name
 * (e.g. "trashBroken", "rescanUnmatched") is injected into the operation string
 * verbatim, exactly as app.js builds it; pass it as `field`.
 */
export async function runHealthBulkOp(field: string, libraryID: string): Promise<BulkOpResult> {
  const query =
    "mutation BulkOp($libraryID: String!) { " + field + "(libraryID: $libraryID) { affected } }";
  const data = await graphql<Record<string, BulkOpResult>>(query, {
    libraryID,
  });
  return data[field];
}

const HEALTH_DRILLDOWN_QUERY =
  "query HealthDrillItems($libraryID: String!, $dimension: String!, $limit: Int) { libraryHealthItems(libraryID: $libraryID, dimension: $dimension, limit: $limit) { mediaID libraryID title } }";

/** LibraryHealthItems — the per-item drill-down for one Library and dimension. */
export async function fetchHealthDrilldown(
  libraryID: string,
  dimension: string,
  signal?: AbortSignal,
): Promise<HealthItemInfo[]> {
  const data = await graphql<{ libraryHealthItems: HealthItemInfo[] | null }>(
    HEALTH_DRILLDOWN_QUERY,
    { libraryID, dimension, limit: 500 },
    signal,
  );
  return data.libraryHealthItems ?? [];
}

const DUPLICATE_GROUPS_QUERY =
  "query DuplicateGroups($libraryID: String!) { libraryDuplicateGroups(libraryID: $libraryID) { items { mediaID libraryID title } } }";

/** LibraryDuplicateGroups — the duplicate clusters for one Library. */
export async function fetchDuplicateGroups(
  libraryID: string,
  signal?: AbortSignal,
): Promise<DuplicateGroupInfo[]> {
  const data = await graphql<{
    libraryDuplicateGroups: DuplicateGroupInfo[] | null;
  }>(DUPLICATE_GROUPS_QUERY, { libraryID }, signal);
  return data.libraryDuplicateGroups ?? [];
}

/**
 * BulkOpSelected — run a health bulk mutation on a caller-supplied subset of
 * Media IDs. The field name (e.g. "bulkTrashBroken") is suffixed with
 * "Selected" server-side (e.g. "bulkTrashBrokenSelected"), exactly as app.js
 * builds it.
 */
export async function runHealthBulkOpSelected(
  field: string,
  libraryID: string,
  mediaIDs: string[],
): Promise<BulkOpResult> {
  const selectedField = field + "Selected";
  const query =
    "mutation BulkDrillOp($libraryID: String!, $mediaIDs: [String!]!) { " +
    selectedField +
    "(libraryID: $libraryID, mediaIDs: $mediaIDs) { affected } }";
  const data = await graphql<Record<string, BulkOpResult>>(query, {
    libraryID,
    mediaIDs,
  });
  return data[selectedField];
}

// --- Field locks (ADR-0009) -----------------------------------------------

const MEDIA_FIELD_LOCKS_QUERY =
  "query MediaFieldLocks($mediaID: String!) { mediaFieldLocks(mediaID: $mediaID) { field locked } }";

/** MediaFieldLocks — the per-field scrape lock state for one Media. */
export async function fetchMediaFieldLocks(mediaID: string): Promise<FieldLockInfo[]> {
  const data = await graphql<{ mediaFieldLocks: FieldLockInfo[] | null }>(MEDIA_FIELD_LOCKS_QUERY, {
    mediaID,
  });
  return data.mediaFieldLocks ?? [];
}

const SET_MEDIA_FIELD_LOCK_MUTATION =
  "mutation SetMediaFieldLock($mediaID: String!, $field: String!, $locked: Boolean!) { setMediaFieldLock(mediaID: $mediaID, field: $field, locked: $locked) { field locked } }";

/** SetMediaFieldLock — lock/unlock one Media field against re-scrape. */
export async function setMediaFieldLock(
  mediaID: string,
  field: string,
  locked: boolean,
): Promise<FieldLockInfo> {
  const data = await graphql<{ setMediaFieldLock: FieldLockInfo }>(SET_MEDIA_FIELD_LOCK_MUTATION, {
    mediaID,
    field,
    locked,
  });
  return data.setMediaFieldLock;
}

// --- Migration importers (ADR-0037) ---------------------------------------

const IMPORTERS_QUERY =
  "query Importers { importers { pluginID id displayName categories input conflict credentialRef credentialRequired credentialConfigured credentialStatus } }";

/** Importers — the available migration Importers. */
export async function fetchImporters(signal?: AbortSignal): Promise<ImporterInfo[]> {
  const data = await graphql<{ importers: ImporterInfo[] | null }>(IMPORTERS_QUERY, {}, signal);
  return data.importers ?? [];
}

const RUN_IMPORT_MUTATION =
  "mutation RunImport($pluginID: String!, $importerID: String!, $sourcePath: String, $categories: [String!], $profileID: String, $libraryID: String) { runImport(pluginID: $pluginID, importerID: $importerID, sourcePath: $sourcePath, categories: $categories, profileID: $profileID, libraryID: $libraryID) { id } }";

/** The operator's Profile/Library mapping for a run (ADR-0037 §2.2). Each is
 * optional — an empty Profile defaults to the operator's own, an empty Library
 * matches across every Library. */
export interface ImportMapping {
  profileID?: string;
  libraryID?: string;
}

/** RunImport — enqueue a migration Importer run (ADR-0037 §2.1). */
export async function runImport(
  pluginID: string,
  importerID: string,
  sourcePath?: string,
  categories?: string[],
  mapping?: ImportMapping,
): Promise<IDRef> {
  const data = await graphql<{ runImport: IDRef }>(RUN_IMPORT_MUTATION, {
    pluginID,
    importerID,
    sourcePath: sourcePath ?? null,
    categories: categories ?? null,
    profileID: mapping?.profileID ? mapping.profileID : null,
    libraryID: mapping?.libraryID ? mapping.libraryID : null,
  });
  return data.runImport;
}

const IMPORT_STATUS_QUERY =
  "query ImportStatus($jobID: String!) { importStatus(jobID: $jobID) { id kind status progress { current total unit message } } }";

/** ImportStatus — the match-rate report for one migration import job (ADR-0037 §2.3). */
export async function fetchImportStatus(jobID: string, signal?: AbortSignal): Promise<JobInfo> {
  const data = await graphql<{ importStatus: JobInfo }>(IMPORT_STATUS_QUERY, { jobID }, signal);
  return data.importStatus;
}

const UNMATCHED_QUEUE_QUERY =
  "query UnmatchedQueue { unmatchedQueue { mediaID title pluginID scraperID confidence threshold candidateID candidateTitle queuedAt failureReason } }";

/** UnmatchedQueue — items the scanner or importer could not match (ADR-0003 §4). */
export async function fetchUnmatchedQueue(): Promise<UnmatchedItemInfo[]> {
  const data = await graphql<{ unmatchedQueue: UnmatchedItemInfo[] | null }>(
    UNMATCHED_QUEUE_QUERY,
    {},
  );
  return data.unmatchedQueue ?? [];
}

const DISMISS_UNMATCHED_MUTATION =
  "mutation DismissUnmatched($mediaID: String!) { dismissUnmatched(mediaID: $mediaID) }";

/** DismissUnmatched — suppress one unmatched record from the queue. */
export async function dismissUnmatched(mediaID: string): Promise<boolean> {
  const data = await graphql<{ dismissUnmatched: boolean }>(DISMISS_UNMATCHED_MUTATION, {
    mediaID,
  });
  return data.dismissUnmatched;
}

const IDENTIFY_SEARCH_QUERY =
  "query IdentifySearch($mediaID: String!, $pluginID: String!, $scraperID: String!, $query: String!) { identifySearch(mediaID: $mediaID, pluginID: $pluginID, scraperID: $scraperID, query: $query) { id title summary year artwork } }";

/** IdentifySearch — free-text candidate search for one visible Media item. */
export async function identifySearch(vars: {
  mediaID: string;
  pluginID: string;
  scraperID: string;
  query: string;
}): Promise<IdentifyCandidate[]> {
  const data = await graphql<{ identifySearch: IdentifyCandidate[] | null }>(
    IDENTIFY_SEARCH_QUERY,
    vars,
  );
  return data.identifySearch ?? [];
}

const FIX_MATCH_MUTATION =
  "mutation FixMatch($mediaID: String!, $pluginID: String!, $scraperID: String!, $candidateID: String!) { fixMatch(mediaID: $mediaID, pluginID: $pluginID, scraperID: $scraperID, candidateID: $candidateID) { id title summary year favorited } }";

/** FixMatch — apply a selected candidate and clear the unmatched row. */
export async function fixMatch(vars: {
  mediaID: string;
  pluginID: string;
  scraperID: string;
  candidateID: string;
}): Promise<GenericMediaInfo> {
  const data = await graphql<{ fixMatch: GenericMediaInfo }>(FIX_MATCH_MUTATION, vars);
  return data.fixMatch;
}

// --- Fix Match full contract (METAPARITY-4, ADR-0068 §A) -------------------

const FIX_MATCH_CANDIDATES_QUERY =
  "query FixMatchCandidates($mediaID: String!, $pluginID: String!, $scraperID: String!, $query: String!) { fixMatchCandidates(mediaID: $mediaID, pluginID: $pluginID, scraperID: $scraperID, query: $query) { id title summary year pluginID scraperID confidence threshold scored reason } }";

/** FixMatchCandidates — candidates scored against the operator's query, best first. */
export async function fixMatchCandidates(vars: {
  mediaID: string;
  pluginID: string;
  scraperID: string;
  query: string;
}): Promise<FixMatchCandidateInfo[]> {
  const data = await graphql<{ fixMatchCandidates: FixMatchCandidateInfo[] | null }>(
    FIX_MATCH_CANDIDATES_QUERY,
    vars,
  );
  return data.fixMatchCandidates ?? [];
}

const IDENTIFY_LOOKUP_QUERY =
  "query IdentifyLookup($mediaID: String!, $pluginID: String!, $scraperID: String!, $candidateID: String!) { identifyLookup(mediaID: $mediaID, pluginID: $pluginID, scraperID: $scraperID, candidateID: $candidateID) { id title summary year } }";

/** IdentifyLookup — fetch one candidate by a pasted provider id, writing nothing. */
export async function identifyLookup(vars: {
  mediaID: string;
  pluginID: string;
  scraperID: string;
  candidateID: string;
}): Promise<IdentifyCandidate> {
  const data = await graphql<{ identifyLookup: IdentifyCandidate }>(IDENTIFY_LOOKUP_QUERY, vars);
  return data.identifyLookup;
}

const FIX_MATCH_PREVIEW_QUERY =
  "query FixMatchPreview($mediaID: String!, $pluginID: String!, $scraperID: String!, $candidateID: String!) { fixMatchPreview(mediaID: $mediaID, pluginID: $pluginID, scraperID: $scraperID, candidateID: $candidateID) { field current proposed changed } }";

/** FixMatchPreview — what applying a candidate would change, before any write. */
export async function fixMatchPreview(vars: {
  mediaID: string;
  pluginID: string;
  scraperID: string;
  candidateID: string;
}): Promise<FixMatchFieldDiff[]> {
  const data = await graphql<{ fixMatchPreview: FixMatchFieldDiff[] | null }>(
    FIX_MATCH_PREVIEW_QUERY,
    vars,
  );
  return data.fixMatchPreview ?? [];
}

// --- Media-independent record inbox (METAPARITY-6/7, ADR-0068) -------------

const UNMATCHED_RECORDS_QUERY =
  "query UnmatchedRecords { unmatchedRecords { id origin libraryID mediaID importRunID importerID sourceHandle candidateID candidateTitle confidence failureReason queuedAt } }";

/** UnmatchedRecords — the pending Media-independent review inbox. */
export async function fetchUnmatchedRecords(): Promise<UnmatchedRecordInfo[]> {
  const data = await graphql<{ unmatchedRecords: UnmatchedRecordInfo[] | null }>(
    UNMATCHED_RECORDS_QUERY,
    {},
  );
  return data.unmatchedRecords ?? [];
}

const CREATE_MEDIA_FROM_UNMATCHED_MUTATION =
  "mutation CreateMediaFromUnmatched($recordID: String!, $libraryID: String!, $title: String!, $year: Int) { createMediaFromUnmatched(recordID: $recordID, libraryID: $libraryID, title: $title, year: $year) { id title summary year favorited } }";

/** CreateMediaFromUnmatched — hand-authored Media with no external ID (METAPARITY-6). */
export async function createMediaFromUnmatched(vars: {
  recordID: string;
  libraryID: string;
  title: string;
  year?: number;
}): Promise<GenericMediaInfo> {
  const data = await graphql<{ createMediaFromUnmatched: GenericMediaInfo }>(
    CREATE_MEDIA_FROM_UNMATCHED_MUTATION,
    vars,
  );
  return data.createMediaFromUnmatched;
}

// --- Scoped suppression (METAPARITY-7, ADR-0068 §A) ------------------------

const UNMATCHED_SUPPRESSIONS_QUERY =
  "query UnmatchedSuppressions($recordID: String) { unmatchedSuppressions(recordID: $recordID) { scope key recordID candidateID sourceHandle actorProfileID createdAt } }";

/** UnmatchedSuppressions — the active suppressions and their provenance. */
export async function fetchUnmatchedSuppressions(
  recordID = "",
): Promise<UnmatchedSuppressionInfo[]> {
  const data = await graphql<{ unmatchedSuppressions: UnmatchedSuppressionInfo[] | null }>(
    UNMATCHED_SUPPRESSIONS_QUERY,
    { recordID },
  );
  return data.unmatchedSuppressions ?? [];
}

/**
 * The scope a suppression applies to. The caller always states it — ADR-0068
 * forbids inferring scope from a generic "Ignore".
 */
export type SuppressionScope = "source-record" | "file" | "candidate";

/** The identity one scoped suppression addresses. Suppress and reset share it. */
export interface SuppressionTarget {
  scope: SuppressionScope;
  recordID?: string;
  fingerprint?: string;
  candidateID?: string;
  sourceHandle?: string;
}

const SUPPRESS_UNMATCHED_MUTATION =
  "mutation SuppressUnmatched($scope: String!, $recordID: String, $fingerprint: String, $candidateID: String, $sourceHandle: String) { suppressUnmatched(scope: $scope, recordID: $recordID, fingerprint: $fingerprint, candidateID: $candidateID, sourceHandle: $sourceHandle) }";

/** SuppressUnmatched — suppress exactly one stated scope. */
export async function suppressUnmatched(target: SuppressionTarget): Promise<boolean> {
  const data = await graphql<{ suppressUnmatched: boolean }>(SUPPRESS_UNMATCHED_MUTATION, {
    ...target,
  });
  return data.suppressUnmatched;
}

const RESET_UNMATCHED_SUPPRESSION_MUTATION =
  "mutation ResetUnmatchedSuppression($scope: String!, $recordID: String, $fingerprint: String, $candidateID: String) { resetUnmatchedSuppression(scope: $scope, recordID: $recordID, fingerprint: $fingerprint, candidateID: $candidateID) }";

/** ResetUnmatchedSuppression — lift ONE scope; both siblings stay suppressed. */
export async function resetUnmatchedSuppression(target: SuppressionTarget): Promise<boolean> {
  const data = await graphql<{ resetUnmatchedSuppression: boolean }>(
    RESET_UNMATCHED_SUPPRESSION_MUTATION,
    { ...target },
  );
  return data.resetUnmatchedSuppression;
}

// --- Watcher status (ADR-0032) --------------------------------------------

const WATCHER_STATUS_QUERY =
  "query WatcherStatus { watcherStatus { libraryID title state reason } }";

/** WatcherStatus — the per-Library fsnotify Watcher state. */
export async function fetchWatcherStatus(): Promise<WatcherStatusInfo[]> {
  const data = await graphql<{ watcherStatus: WatcherStatusInfo[] | null }>(
    WATCHER_STATUS_QUERY,
    {},
  );
  return data.watcherStatus ?? [];
}

// --- Navigation/Layout overrides (WEB-5J / ADR-0061 §Layout editor) -------

// The override blob's selection set, reused by every nav-override operation so
// reads and writes return the same shape.
const NAV_OVERRIDE_FIELDS =
  "hiddenNodeIDs nodeOrder nodeLabelOverrides themeAccentColor layoutSectionOverrides { hiddenSectionIDs sectionOrder }";

const NAV_OVERRIDE_QUERY = `query NavOverride { navOverride { ${NAV_OVERRIDE_FIELDS} } }`;

/** NavOverride — the current server-level Navigation/Layout override blob. */
export async function fetchNavOverride(): Promise<NavOverrideInfo> {
  const data = await graphql<{ navOverride: NavOverrideInfo }>(NAV_OVERRIDE_QUERY, {});
  return data.navOverride;
}

const NAV_OVERRIDE_HISTORY_QUERY = `query NavOverrideHistory($limit: Int) { navOverrideHistory(limit: $limit) { id scope createdAt data { ${NAV_OVERRIDE_FIELDS} } } }`;

/** NavOverrideHistory — recent override snapshots, newest first. */
export async function fetchNavOverrideHistory(limit = 20): Promise<NavOverrideHistoryInfo[]> {
  const data = await graphql<{
    navOverrideHistory: NavOverrideHistoryInfo[] | null;
  }>(NAV_OVERRIDE_HISTORY_QUERY, { limit });
  return data.navOverrideHistory ?? [];
}

const SET_NAV_OVERRIDE_MUTATION = `mutation SetNavOverride($hiddenNodeIDs: [String!], $nodeOrder: [String!], $themeAccentColor: String, $nodeLabelOverrides: NavLabelOverrides, $layoutSectionOverrides: LayoutSectionOverrides) { setNavOverride(hiddenNodeIDs: $hiddenNodeIDs, nodeOrder: $nodeOrder, themeAccentColor: $themeAccentColor, nodeLabelOverrides: $nodeLabelOverrides, layoutSectionOverrides: $layoutSectionOverrides) { ${NAV_OVERRIDE_FIELDS} } }`;

/** One partial nav-override patch; omit a field to leave it unchanged. */
export type NavOverridePatch = Partial<{
  hiddenNodeIDs: string[];
  nodeOrder: string[];
  themeAccentColor: string;
  nodeLabelOverrides: Record<string, Record<string, string>>;
  layoutSectionOverrides: Record<string, SectionOverrideInfo>;
}>;

/**
 * SetNavOverride — apply a partial override patch. The server reads the current
 * blob, applies only the provided fields, and returns the new state.
 */
export async function setNavOverride(patch: NavOverridePatch): Promise<NavOverrideInfo> {
  const data = await graphql<{ setNavOverride: NavOverrideInfo }>(SET_NAV_OVERRIDE_MUTATION, patch);
  return data.setNavOverride;
}

const RESET_NAV_OVERRIDE_MUTATION = `mutation ResetNavOverride { resetNavOverride { ${NAV_OVERRIDE_FIELDS} } }`;

/** ResetNavOverride — clear all overrides back to plugin/library defaults. */
export async function resetNavOverride(): Promise<NavOverrideInfo> {
  const data = await graphql<{ resetNavOverride: NavOverrideInfo }>(
    RESET_NAV_OVERRIDE_MUTATION,
    {},
  );
  return data.resetNavOverride;
}

const RESTORE_NAV_OVERRIDE_MUTATION = `mutation RestoreNavOverride($historyID: Int!) { restoreNavOverride(historyID: $historyID) { ${NAV_OVERRIDE_FIELDS} } }`;

/** RestoreNavOverride — replace the current override with a history snapshot. */
export async function restoreNavOverride(historyID: number): Promise<NavOverrideInfo> {
  const data = await graphql<{ restoreNavOverride: NavOverrideInfo }>(
    RESTORE_NAV_OVERRIDE_MUTATION,
    { historyID },
  );
  return data.restoreNavOverride;
}

// --- First-run setup (ADR-0013) -------------------------------------------

const SETUP_STATE_QUERY =
  "query SetupState { setupState { adminPasswordSet hasLibrary hasOwnerProfile pluginsActivated complete } }";

/** SetupState — the first-run setup readiness checklist. */
export async function fetchSetupState(signal?: AbortSignal): Promise<SetupState> {
  const data = await graphql<{ setupState: SetupState }>(SETUP_STATE_QUERY, {}, signal);
  return data.setupState;
}
