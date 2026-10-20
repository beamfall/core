// Console (admin-gated) operator operations: remote-access status, server +
// transcode config, live sessions, access schedules, enforced certification
// filters, qBittorrent ingest, admin API tokens, scoped API keys, and the
// activity log (ADR-0007 v1 Console; ADR-0040 keys/tokens).
//
// Every query/mutation string is copied VERBATIM from internal/web/static/app.js
// (contract-tested server-side).

import { graphql } from "@/lib/api";
import type {
  AccessScheduleInfo,
  AccessWindowInput,
  ActivityEntryInfo,
  APITokenInfo,
  CapabilityStateInfo,
  CertificationCeilingResult,
  ConsoleConfigInfo,
  CreatedAPITokenInfo,
  CreatedScopedAPIKeyInfo,
  DeviceInfo,
  EnforcedFilterPlugin,
  EnforcedFilterProfile,
  JobEventInfo,
  JobInfo,
  LiveSessionInfo,
  OperationsGovernorInfo,
  PlaybackCacheInfo,
  PlaybackCacheOpResult,
  ProfileAdminInfo,
  QBittorrentIngestInfo,
  RemoteAccessInfo,
  RetentionCandidateHoldResult,
  RetentionRuleInfo,
  RevokeAllDevicesResult,
  RevokeDeviceResult,
  ScopedAPIKeyInfo,
  ScraperCredentialInfo,
  TerminateSessionResult,
  TranscodeConfigInfo,
  UpdateStatusInfo,
} from "./types";

// --- Remote access (ADR-0012/0018/0025) -----------------------------------

const REMOTE_ACCESS_QUERY =
  "query RemoteAccess { remoteAccess { directHTTPS { enabled domain certState } relay { enabled endpoint publicHost connectionState lastError } plainHTTPPublicWarning endpoints { kind url } } }";

/** RemoteAccess — the direct-HTTPS/ACME + relay carrier status. */
export async function fetchRemoteAccess(signal?: AbortSignal): Promise<RemoteAccessInfo> {
  const data = await graphql<{ remoteAccess: RemoteAccessInfo }>(REMOTE_ACCESS_QUERY, {}, signal);
  return data.remoteAccess;
}

// --- Console config: workers + plugin activation (ADR-0013) ---------------

const CONSOLE_CONFIG_FIELDS =
  "backgroundWorkers updateCheckEnabled updateCheckChannel remoteEnrollmentEnabled eventsRetentionDays plugins { id displayName active controlPolicy config { name type label required default value configured options { value label } } permissions { kind label description highRisk } status { id label state detail } actions { id label icon risk requiredPermissions inputFields { name type label required default value configured options { value label } } confirmation } scrapers { pluginID scraperID displayName kind endpoint authRef authStyle credentialRequired credentialConfigured credentialStatus credentialError } }";

const CONSOLE_CONFIG_QUERY = `query ConsoleConfig { consoleConfig { ${CONSOLE_CONFIG_FIELDS} } }`;

/** ConsoleConfig — background worker pool size + plugin activation states. */
export async function fetchConsoleConfig(signal?: AbortSignal): Promise<ConsoleConfigInfo> {
  const data = await graphql<{ consoleConfig: ConsoleConfigInfo }>(
    CONSOLE_CONFIG_QUERY,
    {},
    signal,
  );
  return data.consoleConfig;
}

const SET_PLUGIN_ACTIVE_MUTATION = `mutation SetPluginActive($pluginID: String!, $active: Boolean!) { setPluginActive(pluginID: $pluginID, active: $active) { ${CONSOLE_CONFIG_FIELDS} } }`;

/** SetPluginActive — activate/deactivate a plugin (returns the full config). */
export async function setPluginActive(
  pluginID: string,
  active: boolean,

  signal?: AbortSignal,
): Promise<ConsoleConfigInfo> {
  const data = await graphql<{ setPluginActive: ConsoleConfigInfo }>(
    SET_PLUGIN_ACTIVE_MUTATION,
    {
      pluginID,
      active,
    },
    signal,
  );
  return data.setPluginActive;
}

const UNINSTALL_PLUGIN_DATA_MUTATION = `mutation UninstallPluginData($pluginID: String!, $confirm: Boolean!) { uninstallPluginData(pluginID: $pluginID, confirm: $confirm) { ${CONSOLE_CONFIG_FIELDS} } }`;

/**
 * UninstallPluginData — the explicit, Admin-Session-gated purge (ADR-0104).
 * Permanently removes the plugin's stored Entity data across its bound
 * Libraries and deactivates it. Irreversible (not Trash-routed); distinct from
 * Deactivate, which preserves data. Always called with `confirm: true`.
 */
export async function uninstallPluginData(
  pluginID: string,
  signal?: AbortSignal,
): Promise<ConsoleConfigInfo> {
  const data = await graphql<{ uninstallPluginData: ConsoleConfigInfo }>(
    UNINSTALL_PLUGIN_DATA_MUTATION,
    { pluginID, confirm: true },

    signal,
  );
  return data.uninstallPluginData;
}

const SET_PLUGIN_CONFIG_MUTATION = `mutation SetPluginConfig($pluginID: String!, $values: JSON, $clear: [String!]) { setPluginConfig(pluginID: $pluginID, values: $values, clear: $clear) { ${CONSOLE_CONFIG_FIELDS} } }`;

/** SetPluginConfig — update non-secret typed plugin config values. */
export async function setPluginConfig(
  pluginID: string,
  values: Record<string, unknown>,
  clear: string[] = [],

  signal?: AbortSignal,
): Promise<ConsoleConfigInfo> {
  const data = await graphql<{ setPluginConfig: ConsoleConfigInfo }>(
    SET_PLUGIN_CONFIG_MUTATION,
    {
      pluginID,
      values,
      clear,
    },
    signal,
  );
  return data.setPluginConfig;
}

export interface ConsoleConfigPatch {
  backgroundWorkers?: number;
  updateCheckEnabled?: boolean;
  updateCheckChannel?: string;
  remoteEnrollmentEnabled?: boolean;
  eventsRetentionDays?: number;
}

const SET_CONSOLE_CONFIG_MUTATION = `mutation SetConsoleConfig($backgroundWorkers: Int, $updateCheckEnabled: Boolean, $updateCheckChannel: String, $remoteEnrollmentEnabled: Boolean, $eventsRetentionDays: Int) { setConsoleConfig(backgroundWorkers: $backgroundWorkers, updateCheckEnabled: $updateCheckEnabled, updateCheckChannel: $updateCheckChannel, remoteEnrollmentEnabled: $remoteEnrollmentEnabled, eventsRetentionDays: $eventsRetentionDays) { ${CONSOLE_CONFIG_FIELDS} } }`;

/** SetConsoleConfig — partially update Console runtime config. */
export async function setConsoleConfig(
  patch: ConsoleConfigPatch,
  signal?: AbortSignal,
): Promise<ConsoleConfigInfo> {
  const data = await graphql<{ setConsoleConfig: ConsoleConfigInfo }>(
    SET_CONSOLE_CONFIG_MUTATION,
    patch as unknown as Record<string, unknown>,

    signal,
  );
  return data.setConsoleConfig;
}

const UPDATE_STATUS_QUERY =
  "query UpdateStatus { updateStatus { state enabled channel currentVersion latestVersion updateAvailable } }";

/** UpdateStatus — opt-in public release-manifest status. */
export async function fetchUpdateStatus(signal?: AbortSignal): Promise<UpdateStatusInfo> {
  const data = await graphql<{ updateStatus: UpdateStatusInfo }>(UPDATE_STATUS_QUERY, {}, signal);
  return data.updateStatus;
}

const SCRAPER_CREDENTIAL_FIELDS =
  "authRef credentialConfigured credentialStatus credentialError scraperIDs";

const SET_SCRAPER_CREDENTIAL_MUTATION = `mutation SetScraperCredential($authRef: String!, $secret: String!) { setScraperCredential(authRef: $authRef, secret: $secret) { ${SCRAPER_CREDENTIAL_FIELDS} } }`;

/** SetScraperCredential — rotate one AuthRef-scoped scraper credential. */
export async function setScraperCredential(
  authRef: string,
  secret: string,

  signal?: AbortSignal,
): Promise<ScraperCredentialInfo> {
  const data = await graphql<{ setScraperCredential: ScraperCredentialInfo }>(
    SET_SCRAPER_CREDENTIAL_MUTATION,
    { authRef, secret },

    signal,
  );
  return data.setScraperCredential;
}

const VALIDATE_SCRAPER_CREDENTIAL_MUTATION = `mutation ValidateScraperCredential($authRef: String!) { validateScraperCredential(authRef: $authRef) { ${SCRAPER_CREDENTIAL_FIELDS} } }`;

/** ValidateScraperCredential — re-read one AuthRef-scoped credential state. */
export async function validateScraperCredential(
  authRef: string,
  signal?: AbortSignal,
): Promise<ScraperCredentialInfo> {
  const data = await graphql<{
    validateScraperCredential: ScraperCredentialInfo;
  }>(VALIDATE_SCRAPER_CREDENTIAL_MUTATION, { authRef }, signal);
  return data.validateScraperCredential;
}

const DISABLE_SCRAPER_CREDENTIAL_MUTATION = `mutation DisableScraperCredential($authRef: String!) { disableScraperCredential(authRef: $authRef) { ${SCRAPER_CREDENTIAL_FIELDS} } }`;

/** DisableScraperCredential — remove one AuthRef-scoped scraper credential. */
export async function disableScraperCredential(
  authRef: string,
  signal?: AbortSignal,
): Promise<ScraperCredentialInfo> {
  const data = await graphql<{
    disableScraperCredential: ScraperCredentialInfo;
  }>(DISABLE_SCRAPER_CREDENTIAL_MUTATION, { authRef }, signal);
  return data.disableScraperCredential;
}

// --- Operations jobs/governor substrate (ADR-0013 / ADR-0061) -------------

const OPERATIONS_GOVERNOR_QUERY =
  "query OperationsGovernor { operationsGovernor { available generationWorkers backgroundCPUBudget activeTranscodes activeGeneration generationAllowed lowPowerMode transcodePreemptingBackground } }";

/** OperationsGovernor — live resource-governor status for Operations. */
export async function fetchOperationsGovernor(
  signal?: AbortSignal,
): Promise<OperationsGovernorInfo> {
  const data = await graphql<{ operationsGovernor: OperationsGovernorInfo }>(
    OPERATIONS_GOVERNOR_QUERY,
    {},
    signal,
  );
  return data.operationsGovernor;
}

const JOB_EVENTS_QUERY =
  "query JobEvents($id: String!) { jobEvents(id: $id) { id jobID occurredAt type payload } }";

/** JobEvents — one job's durable event/log history. */
export async function fetchJobEvents(id: string, signal?: AbortSignal): Promise<JobEventInfo[]> {
  const data = await graphql<{ jobEvents: JobEventInfo[] | null }>(
    JOB_EVENTS_QUERY,
    { id },
    signal,
  );
  return data.jobEvents ?? [];
}

const JOB_CANCEL_MUTATION =
  "mutation JobCancel($id: String!) { jobCancel(id: $id) { id kind class status attempts createdAt updatedAt startedAt finishedAt error progress { current total unit message } } }";

/** JobCancel — request cancellation for a queued/running/yielded job. */
export async function cancelJob(id: string, signal?: AbortSignal): Promise<JobInfo> {
  const data = await graphql<{ jobCancel: JobInfo }>(JOB_CANCEL_MUTATION, { id }, signal);
  return data.jobCancel;
}

const JOB_RETRY_MUTATION =
  "mutation JobRetry($id: String!) { jobRetry(id: $id) { id kind class status attempts createdAt updatedAt startedAt finishedAt error progress { current total unit message } } }";

/** JobRetry — requeue a terminal job using its stored payload/checkpoint. */
export async function retryJob(id: string, signal?: AbortSignal): Promise<JobInfo> {
  const data = await graphql<{ jobRetry: JobInfo }>(JOB_RETRY_MUTATION, { id }, signal);
  return data.jobRetry;
}

const RUN_EVENTS_COMPACTION_MUTATION =
  "mutation RunEventsCompaction { runEventsCompaction { id kind class status attempts createdAt updatedAt startedAt finishedAt error progress { current total unit message } } }";

/** RunEventsCompaction — enqueue an on-demand events.db compaction job. */
export async function runEventsCompaction(signal?: AbortSignal): Promise<JobInfo> {
  const data = await graphql<{ runEventsCompaction: JobInfo }>(
    RUN_EVENTS_COMPACTION_MUTATION,
    {},
    signal,
  );
  return data.runEventsCompaction;
}

// --- Transcode config -----------------------------------------------------

const TRANSCODE_CONFIG_QUERY =
  "query TranscodeConfig { transcodeConfig { hardwareEnabled codec quality device configuredPreference hardwareAcceleration { accelerator status } } }";

/** TranscodeConfig — the transcode policy. */
export async function fetchTranscodeConfig(signal?: AbortSignal): Promise<TranscodeConfigInfo> {
  const data = await graphql<{ transcodeConfig: TranscodeConfigInfo }>(
    TRANSCODE_CONFIG_QUERY,
    {},
    signal,
  );
  return data.transcodeConfig;
}

const SET_TRANSCODE_CONFIG_MUTATION =
  "mutation SetTranscodeConfig($hardwareEnabled: Boolean!, $codec: String!, $quality: String!, $device: String!) { setTranscodeConfig(hardwareEnabled: $hardwareEnabled, codec: $codec, quality: $quality, device: $device) { hardwareEnabled codec quality device configuredPreference hardwareAcceleration { accelerator status } } }";

/** SetTranscodeConfig — set the transcode policy. */
export async function setTranscodeConfig(
  config: TranscodeConfigInfo,

  signal?: AbortSignal,
): Promise<TranscodeConfigInfo> {
  const data = await graphql<{ setTranscodeConfig: TranscodeConfigInfo }>(
    SET_TRANSCODE_CONFIG_MUTATION,
    config as unknown as Record<string, unknown>,

    signal,
  );
  return data.setTranscodeConfig;
}

// --- Live sessions (ADR-0007) ---------------------------------------------

const LIVE_SESSIONS_QUERY =
  "query LiveSessions { liveSessions { sessionID deviceID deviceName profileID profileName mediaID title transcoding positionSeconds durationSeconds } }";

/** LiveSessions — the active playback sessions across all devices. */
export async function fetchLiveSessions(signal?: AbortSignal): Promise<LiveSessionInfo[]> {
  const data = await graphql<{ liveSessions: LiveSessionInfo[] | null }>(
    LIVE_SESSIONS_QUERY,
    {},
    signal,
  );
  return data.liveSessions ?? [];
}

const TERMINATE_SESSION_MUTATION =
  "mutation TerminateSession($sessionID: String!) { terminateSession(sessionID: $sessionID) { sessionID } }";

/** TerminateSession — terminate one live session. */
export async function terminateSession(
  sessionID: string,
  signal?: AbortSignal,
): Promise<TerminateSessionResult> {
  const data = await graphql<{ terminateSession: TerminateSessionResult }>(
    TERMINATE_SESSION_MUTATION,
    { sessionID },

    signal,
  );
  return data.terminateSession;
}

// --- Device administration (ADR-0081) -------------------------------------

const DEVICES_QUERY = "query Devices { devices { deviceID name role revoked current } }";

/** Devices — every enrolled device for the Console administration list. */
export async function fetchDevices(signal?: AbortSignal): Promise<DeviceInfo[]> {
  const data = await graphql<{ devices: DeviceInfo[] | null }>(DEVICES_QUERY, {}, signal);
  return data.devices ?? [];
}

const REVOKE_DEVICE_MUTATION =
  "mutation RevokeDevice($deviceID: String!) { revokeDevice(deviceID: $deviceID) { deviceID transcodesAborted } }";

/** RevokeDevice — revoke one device credential (invalidates its sessions and
 * stream tokens, aborts its in-flight transcodes). */
export async function revokeDevice(
  deviceID: string,
  signal?: AbortSignal,
): Promise<RevokeDeviceResult> {
  const data = await graphql<{ revokeDevice: RevokeDeviceResult }>(
    REVOKE_DEVICE_MUTATION,
    {
      deviceID,
    },
    signal,
  );
  return data.revokeDevice;
}

const REVOKE_ALL_DEVICES_MUTATION =
  "mutation RevokeAllDevices { revokeAllDevices { revokedCount transcodesAborted } }";

/** RevokeAllDevices — revoke every device EXCEPT the current browser. */
export async function revokeAllDevices(signal?: AbortSignal): Promise<RevokeAllDevicesResult> {
  const data = await graphql<{ revokeAllDevices: RevokeAllDevicesResult }>(
    REVOKE_ALL_DEVICES_MUTATION,
    {},

    signal,
  );
  return data.revokeAllDevices;
}

const SET_DEVICE_ROLE_MUTATION =
  "mutation SetDeviceRole($deviceID: String!, $role: String!) { setDeviceRole(deviceID: $deviceID, role: $role) { deviceID name role revoked current } }";

/** SetDeviceRole — promote or demote an enrolled non-current device. */
export async function setDeviceRole(
  deviceID: string,
  role: string,
  signal?: AbortSignal,
): Promise<DeviceInfo> {
  const data = await graphql<{ setDeviceRole: DeviceInfo }>(
    SET_DEVICE_ROLE_MUTATION,
    {
      deviceID,
      role,
    },
    signal,
  );
  return data.setDeviceRole;
}

// --- Playback rendition cache (ADR-0057, PLAY-7) --------------------------

const PLAYBACK_CACHE_QUERY =
  "query PlaybackRenditionCache { playbackRenditionCache { configured ready generating failed evicted readyBytes budgetBytes } }";

/** PlaybackRenditionCache — the operator cache-health snapshot. */
export async function fetchPlaybackCache(signal?: AbortSignal): Promise<PlaybackCacheInfo> {
  const data = await graphql<{ playbackRenditionCache: PlaybackCacheInfo }>(
    PLAYBACK_CACHE_QUERY,
    {},
    signal,
  );
  return data.playbackRenditionCache;
}

const PRECOMPUTE_RENDITIONS_MUTATION =
  "mutation PrecomputePlaybackRenditions($mediaID: ID!) { precomputePlaybackRenditions(mediaID: $mediaID) { affected } }";

/** PrecomputePlaybackRenditions — explicit generation for one Media's ladder. */
export async function precomputePlaybackRenditions(
  mediaID: string,

  signal?: AbortSignal,
): Promise<PlaybackCacheOpResult> {
  const data = await graphql<{
    precomputePlaybackRenditions: PlaybackCacheOpResult;
  }>(PRECOMPUTE_RENDITIONS_MUTATION, { mediaID }, signal);
  return data.precomputePlaybackRenditions;
}

const EVICT_PLAYBACK_CACHE_MUTATION =
  "mutation EvictPlaybackRenditionCache { evictPlaybackRenditionCache { affected } }";

/** EvictPlaybackRenditionCache — an immediate over-budget eviction pass. */
export async function evictPlaybackRenditionCache(
  signal?: AbortSignal,
): Promise<PlaybackCacheOpResult> {
  const data = await graphql<{
    evictPlaybackRenditionCache: PlaybackCacheOpResult;
  }>(EVICT_PLAYBACK_CACHE_MUTATION, {}, signal);
  return data.evictPlaybackRenditionCache;
}

// --- Capability runtime gating (ADR-0124 §5) ------------------------------

const CAPABILITY_STATES_QUERY =
  "query CapabilityStates { capabilityStates { id kind enabled flippable reason } }";

/** CapabilityStates — the runtime state of every compiled-in capability with the
 * operator's Workflow toggles applied. Probe-derived capabilities report
 * `enabled:false, reason:probe-absent` and `flippable:false` (auto-detected,
 * never operator-flipped). Suppressed entirely on App Store binaries. */
export async function fetchCapabilityStates(signal?: AbortSignal): Promise<CapabilityStateInfo[]> {
  const data = await graphql<{ capabilityStates: CapabilityStateInfo[] }>(
    CAPABILITY_STATES_QUERY,
    {},
    signal,
  );
  return data.capabilityStates;
}

const SET_CAPABILITY_ENABLED_MUTATION =
  "mutation SetCapabilityEnabled($capability: String!, $enabled: Boolean!) { setCapabilityEnabled(capability: $capability, enabled: $enabled) { id kind enabled flippable reason } }";

/** SetCapabilityEnabled — flip one Workflow capability's runtime enabled state.
 * An audited, attributed, Admin-Session-gated mutation; only flippable Workflow
 * capabilities are valid targets. Returns the refreshed capability states. */
export async function setCapabilityEnabled(
  capability: string,
  enabled: boolean,

  signal?: AbortSignal,
): Promise<CapabilityStateInfo[]> {
  const data = await graphql<{
    setCapabilityEnabled: CapabilityStateInfo[];
  }>(SET_CAPABILITY_ENABLED_MUTATION, { capability, enabled }, signal);
  return data.setCapabilityEnabled;
}

// --- Access schedules (ADR-0007 / ADR-0004) -------------------------------

const ACCESS_SCHEDULE_QUERY =
  "query AccessSchedule($profileID: String!) { accessSchedule(profileID: $profileID) { profileID windows { weekday startMinute endMinute } } }";

/** AccessSchedule — a Profile's allowed access windows. */
export async function fetchAccessSchedule(
  profileID: string,
  signal?: AbortSignal,
): Promise<AccessScheduleInfo> {
  const data = await graphql<{ accessSchedule: AccessScheduleInfo | null }>(
    ACCESS_SCHEDULE_QUERY,
    { profileID },
    signal,
  );
  return data.accessSchedule ?? { profileID, windows: [] };
}

const SET_ACCESS_SCHEDULE_MUTATION =
  "mutation SetAccessSchedule($profileID: String!, $windows: [AccessWindowInput!]!) { setAccessSchedule(profileID: $profileID, windows: $windows) { profileID windows { weekday startMinute endMinute } } }";

/** SetAccessSchedule — replace a Profile's allowed access windows. */
export async function setAccessSchedule(
  profileID: string,
  windows: AccessWindowInput[],

  signal?: AbortSignal,
): Promise<AccessScheduleInfo> {
  const data = await graphql<{ setAccessSchedule: AccessScheduleInfo }>(
    SET_ACCESS_SCHEDULE_MUTATION,
    { profileID, windows },

    signal,
  );
  return data.setAccessSchedule;
}

// --- Enforced certification filters (ADR-0028 §5) -------------------------

const ENFORCED_FILTERS_QUERY =
  "query EnforcedFilters { profiles { id name kind libraryIDs certificationCeilings { pluginID ceiling allowUnrated } } plugins { id displayName certificationScale } }";

/** The enforced-filters Console screen payload (profiles + plugins-with-scale). */
export interface EnforcedFiltersData {
  profiles: EnforcedFilterProfile[];
  plugins: EnforcedFilterPlugin[];
}

/** EnforcedFilters — Profiles' ceilings + plugins' certification scales. */
export async function fetchEnforcedFilters(signal?: AbortSignal): Promise<EnforcedFiltersData> {
  const data = await graphql<{
    profiles: EnforcedFilterProfile[] | null;
    plugins: EnforcedFilterPlugin[] | null;
  }>(ENFORCED_FILTERS_QUERY, {}, signal);
  return { profiles: data.profiles ?? [], plugins: data.plugins ?? [] };
}

const SET_CERTIFICATION_CEILING_MUTATION =
  "mutation SetCertificationCeiling($profileID: String!, $pluginID: String!, $ceiling: String!) { setCertificationCeiling(profileID: $profileID, pluginID: $pluginID, ceiling: $ceiling) { id certificationCeilings { pluginID ceiling allowUnrated } } }";

/** SetCertificationCeiling — set a Profile's per-plugin certification ceiling. */
export async function setCertificationCeiling(
  profileID: string,
  pluginID: string,
  ceiling: string,

  signal?: AbortSignal,
): Promise<CertificationCeilingResult> {
  const data = await graphql<{
    setCertificationCeiling: CertificationCeilingResult;
  }>(SET_CERTIFICATION_CEILING_MUTATION, { profileID, pluginID, ceiling }, signal);
  return data.setCertificationCeiling;
}

// --- qBittorrent ingest (ADR-0020) ----------------------------------------

const QBITTORRENT_INGEST_QUERY =
  "query QBittorrentIngest { qbittorrentIngest { config { enabled routes { category libraryID } } status { releaseName category outcome libraryID detail } } }";

/** QBittorrentIngest — the ingest config + recent ingest status (read-only). */
export async function fetchQBittorrentIngest(signal?: AbortSignal): Promise<QBittorrentIngestInfo> {
  const data = await graphql<{ qbittorrentIngest: QBittorrentIngestInfo }>(
    QBITTORRENT_INGEST_QUERY,
    {},
    signal,
  );
  return data.qbittorrentIngest;
}

// --- Admin API tokens (ADR-0013) ------------------------------------------

const API_TOKENS_QUERY = "query APITokens { apiTokens { id name createdAt lastUsedAt revoked } }";

/** APITokens — the admin API tokens. */
export async function fetchAPITokens(signal?: AbortSignal): Promise<APITokenInfo[]> {
  const data = await graphql<{ apiTokens: APITokenInfo[] | null }>(API_TOKENS_QUERY, {}, signal);
  return data.apiTokens ?? [];
}

const REVOKE_API_TOKEN_MUTATION =
  "mutation RevokeAPIToken($id: String!) { revokeApiToken(id: $id) { id name createdAt lastUsedAt revoked } }";

/** RevokeAPIToken — revoke one admin API token. */
export async function revokeAPIToken(id: string, signal?: AbortSignal): Promise<APITokenInfo> {
  const data = await graphql<{ revokeApiToken: APITokenInfo }>(
    REVOKE_API_TOKEN_MUTATION,
    { id },
    signal,
  );
  return data.revokeApiToken;
}

const CREATE_API_TOKEN_MUTATION =
  "mutation CreateAPIToken($name: String!) { createApiToken(name: $name) { token info { id name createdAt lastUsedAt revoked } } }";

/** CreateAPIToken — mint an admin API token (token shown once). */
export async function createAPIToken(
  name: string,
  signal?: AbortSignal,
): Promise<CreatedAPITokenInfo> {
  const data = await graphql<{ createApiToken: CreatedAPITokenInfo }>(
    CREATE_API_TOKEN_MUTATION,
    {
      name,
    },
    signal,
  );
  return data.createApiToken;
}

// --- Scoped API keys (ADR-0040) -------------------------------------------

const API_KEYS_QUERY =
  "query APIKeys { apiKeys { id name scopes profileID createdAt lastUsedAt revoked } }";

/** APIKeys — the scoped developer API keys. */
export async function fetchAPIKeys(signal?: AbortSignal): Promise<ScopedAPIKeyInfo[]> {
  const data = await graphql<{ apiKeys: ScopedAPIKeyInfo[] | null }>(API_KEYS_QUERY, {}, signal);
  return data.apiKeys ?? [];
}

const REVOKE_API_KEY_MUTATION =
  "mutation RevokeAPIKey($id: String!) { revokeApiKey(id: $id) { id name scopes profileID createdAt lastUsedAt revoked } }";

/** RevokeAPIKey — revoke one scoped API key. */
export async function revokeAPIKey(id: string, signal?: AbortSignal): Promise<ScopedAPIKeyInfo> {
  const data = await graphql<{ revokeApiKey: ScopedAPIKeyInfo }>(
    REVOKE_API_KEY_MUTATION,
    { id },
    signal,
  );
  return data.revokeApiKey;
}

const CREATE_API_KEY_MUTATION =
  "mutation CreateAPIKey($name: String!, $scopes: [String!]!) { createApiKey(name: $name, scopes: $scopes) { key info { id name scopes profileID createdAt lastUsedAt revoked } } }";

/** CreateAPIKey — mint a scoped API key (key shown once). */
export async function createAPIKey(
  name: string,
  scopes: string[],

  signal?: AbortSignal,
): Promise<CreatedScopedAPIKeyInfo> {
  const data = await graphql<{ createApiKey: CreatedScopedAPIKeyInfo }>(
    CREATE_API_KEY_MUTATION,
    {
      name,
      scopes,
    },
    signal,
  );
  return data.createApiKey;
}

// --- Activity log (ADR-0012) ----------------------------------------------

const ACTIVITY_LOG_QUERY =
  "query ActivityLog { activityLog(limit: 50) { id occurredAt action reversible targetType targetID } }";

/** ActivityLog — the append-only, reveal-filtered activity log (latest 50). */
export async function fetchActivityLog(signal?: AbortSignal): Promise<ActivityEntryInfo[]> {
  const data = await graphql<{ activityLog: ActivityEntryInfo[] | null }>(
    ACTIVITY_LOG_QUERY,
    {},
    signal,
  );
  return data.activityLog ?? [];
}

// --- Profile administration (ADR-0086 — Admin-Session-gated) --------------
// The vanilla app.js has no Profiles admin surface, so these strings are copied
// from the server's authoritative contract tests in internal/graph/graph_test.go
// (CreateProfile / UpdateProfile cases) rather than app.js — same VERBATIM
// discipline, against the server-tested shape.

const PROFILES_ADMIN_QUERY = "query Profiles { profiles { id name kind libraryIDs } }";

/**
 * fetchProfilesAdmin — the household Profiles for the Console Profiles lane.
 * The server `profiles` query excludes Disabled Profiles (graph.go), so a
 * disabled/offboarded Profile drops off the list (there is no re-enable query
 * yet — server follow-up).
 */
export async function fetchProfilesAdmin(signal?: AbortSignal): Promise<ProfileAdminInfo[]> {
  const data = await graphql<{
    profiles: { id: string; name: string; kind: string; libraryIDs: string[] }[] | null;
  }>(PROFILES_ADMIN_QUERY, {}, signal);
  return (data.profiles ?? []).map((p) => ({ ...p, disabled: false }));
}

const CREATE_PROFILE_MUTATION =
  "mutation CreateProfile($name: String!, $kind: String!) { createProfile(name: $name, kind: $kind) { id name kind libraryIDs } }";

/** CreateProfile — create a household Profile (adult/managed). Admin-gated. */
export async function createProfile(
  name: string,
  kind: string,
  signal?: AbortSignal,
): Promise<ProfileAdminInfo> {
  const data = await graphql<{
    createProfile: { id: string; name: string; kind: string; libraryIDs: string[] };
  }>(CREATE_PROFILE_MUTATION, { name, kind }, signal);
  return { ...data.createProfile, disabled: false };
}

const DISABLE_PROFILE_MUTATION =
  "mutation UpdateProfile($profileID: String!, $disabled: Boolean) { updateProfile(profileID: $profileID, disabled: $disabled) { id disabled } }";

/**
 * disableProfile — offboard a Profile (ADR-0086 §30): hides it from pickers and
 * refuses new sessions while engagement history is preserved. A thin
 * updateProfile(disabled:true). Admin-gated. One-way in the web Console today
 * (no re-enable query) — the UI confirms + explains the effects before acting.
 */
export async function disableProfile(
  profileID: string,

  signal?: AbortSignal,
): Promise<{ id: string; disabled: boolean }> {
  const data = await graphql<{
    updateProfile: { id: string; disabled: boolean };
  }>(DISABLE_PROFILE_MUTATION, { profileID, disabled: true }, signal);
  return data.updateProfile;
}

const UPDATE_PROFILE_MUTATION =
  "mutation UpdateProfile($profileID: String!, $name: String!, $kind: String!) { updateProfile(profileID: $profileID, name: $name, kind: $kind) { id name kind libraryIDs disabled } }";

/** UpdateProfile — edit a Profile's display name and kind. Admin-gated. */
export async function updateProfile(
  profileID: string,
  name: string,
  kind: string,

  signal?: AbortSignal,
): Promise<ProfileAdminInfo> {
  const data = await graphql<{ updateProfile: ProfileAdminInfo }>(
    UPDATE_PROFILE_MUTATION,
    {
      profileID,
      name,
      kind,
    },
    signal,
  );
  return data.updateProfile;
}

const SET_PROFILE_LIBRARIES_MUTATION =
  "mutation SetProfileLibraries($profileID: String!, $libraryIDs: [String!]!) { setProfileLibraries(profileID: $profileID, libraryIDs: $libraryIDs) { id libraryIDs } }";

/** SetProfileLibraries — replace the Libraries granted to a Profile. */
export async function setProfileLibraries(
  profileID: string,
  libraryIDs: string[],

  signal?: AbortSignal,
): Promise<{ id: string; libraryIDs: string[] }> {
  const data = await graphql<{
    setProfileLibraries: { id: string; libraryIDs: string[] };
  }>(SET_PROFILE_LIBRARIES_MUTATION, { profileID, libraryIDs }, signal);
  return data.setProfileLibraries;
}

// --- Managed-Profile policy (ADR-0087 — Admin-Session-gated) --------------

const SET_PROFILE_STREAM_CAP_MUTATION =
  "mutation UpdateProfile($profileID: String!, $maxConcurrentStreams: Int) { updateProfile(profileID: $profileID, maxConcurrentStreams: $maxConcurrentStreams) { id maxConcurrentStreams } }";

/**
 * setProfileStreamCap — set or CLEAR a Profile's max concurrent streams
 * (ADR-0087 §30). The store distinguishes an absent variable (no-op) from `0`
 * (clears the cap), so `max <= 0` (a cleared/empty input) sends `0` explicitly
 * to clear it — never omit the variable. The `maxConcurrentStreams` value rides
 * the variables JSON alongside `profileID` (the server reads it from raw
 * variables, matching the graph_test.go contract case). Admin-gated.
 */
export async function setProfileStreamCap(
  profileID: string,
  max: number,

  signal?: AbortSignal,
): Promise<{ id: string; maxConcurrentStreams: number | null }> {
  const data = await graphql<{
    updateProfile: { id: string; maxConcurrentStreams: number | null };
  }>(
    SET_PROFILE_STREAM_CAP_MUTATION,
    {
      profileID,
      maxConcurrentStreams: max > 0 ? Math.floor(max) : 0,
    },
    signal,
  );
  return data.updateProfile;
}

const ACCESS_GRANTS_QUERY =
  "query AccessGrantsForProfile($profileID: String!) { accessgrantsforprofile(profileID: $profileID) { profileID mediaID requestID actorProfileID createdAt updatedAt } }";

/**
 * accessGrantsForProfile — list active per-title access grants for a managed
 * Profile (ACCESS-3, ADR-0113 §2.3). An adult may query any profile; a managed
 * Profile may query its own.
 */
export async function accessGrantsForProfile(
  profileID: string,
  signal?: AbortSignal,
): Promise<import("./types").AccessGrantInfo[]> {
  const data = await graphql<{
    accessgrantsforprofile: import("./types").AccessGrantInfo[];
  }>(ACCESS_GRANTS_QUERY, { profileID }, signal);
  return data.accessgrantsforprofile ?? [];
}

const REVOKE_ACCESS_GRANT_MUTATION =
  "mutation RevokeAccessGrant($profileID: String!, $mediaID: String!) { revokeaccessgrant(profileID: $profileID, mediaID: $mediaID) { profileID mediaID } }";

/**
 * revokeAccessGrant — revoke the active AccessGrant for a (profileID, mediaID)
 * pair (ACCESS-3, ADR-0113 §2.3). Adult-only. Idempotent.
 */
export async function revokeAccessGrant(
  profileID: string,
  mediaID: string,

  signal?: AbortSignal,
): Promise<{ profileID: string; mediaID: string }> {
  const data = await graphql<{
    revokeaccessgrant: { profileID: string; mediaID: string };
  }>(REVOKE_ACCESS_GRANT_MUTATION, { profileID, mediaID }, signal);
  return data.revokeaccessgrant;
}

const SET_EXTENSION_PREAUTH_MUTATION =
  "mutation SetExtensionPreauthorization($profileID: String!, $enabled: Boolean) { setExtensionPreauthorization(profileID: $profileID, enabled: $enabled) { profileID enabled } }";

/**
 * setExtensionPreauthorization — toggle a managed Profile's extension-request
 * pre-authorization (ADR-0039 §2.4 / ADR-0087 §31). Managed-Profile only — the
 * server rejects an adult Profile. Admin-gated.
 */
export async function setExtensionPreauthorization(
  profileID: string,
  enabled: boolean,

  signal?: AbortSignal,
): Promise<{ profileID: string; enabled: boolean }> {
  const data = await graphql<{
    setExtensionPreauthorization: { profileID: string; enabled: boolean };
  }>(SET_EXTENSION_PREAUTH_MUTATION, { profileID, enabled }, signal);
  return data.setExtensionPreauthorization;
}

// --- Retention rules (ADR-0051, RET-2) ---

const RETENTION_RULES_QUERY =
  "query { retentionRules { id name smartCollectionID sourceExpr targetLibraryID graceDays revealedAtAuthoring enabled createdAt updatedAt } }";

const CREATE_RETENTION_RULE_MUTATION =
  "mutation CreateRetentionRule($name: String!, $sourceExpr: String!, $targetLibraryID: String!, $graceDays: Int, $revealedAtAuthoring: Boolean, $enabled: Boolean) { createRetentionRule(name: $name, sourceExpr: $sourceExpr, targetLibraryID: $targetLibraryID, graceDays: $graceDays, revealedAtAuthoring: $revealedAtAuthoring, enabled: $enabled) { id name smartCollectionID sourceExpr targetLibraryID graceDays revealedAtAuthoring enabled createdAt } }";

const UPDATE_RETENTION_RULE_MUTATION =
  "mutation UpdateRetentionRule($id: String!, $name: String, $sourceExpr: String, $targetLibraryID: String, $graceDays: Int, $revealedAtAuthoring: Boolean, $enabled: Boolean) { updateRetentionRule(id: $id, name: $name, sourceExpr: $sourceExpr, targetLibraryID: $targetLibraryID, graceDays: $graceDays, revealedAtAuthoring: $revealedAtAuthoring, enabled: $enabled) { id name smartCollectionID sourceExpr targetLibraryID graceDays revealedAtAuthoring enabled updatedAt } }";

const DELETE_RETENTION_RULE_MUTATION =
  "mutation DeleteRetentionRule($id: String!) { deleteRetentionRule(id: $id) { id name } }";

const SET_RETENTION_CANDIDATE_HOLD_MUTATION =
  "mutation SetRetentionCandidateHold($ruleID: String!, $mediaID: String!, $hold: Boolean!) { setRetentionCandidateHold(ruleID: $ruleID, mediaID: $mediaID, hold: $hold) { ruleID mediaID hold } }";

export async function fetchRetentionRules(signal?: AbortSignal): Promise<RetentionRuleInfo[]> {
  const data = await graphql<{ retentionRules: RetentionRuleInfo[] }>(
    RETENTION_RULES_QUERY,
    {},
    signal,
  );
  return data.retentionRules;
}

export interface RetentionRuleDraft {
  name: string;
  sourceExpr: string;
  targetLibraryID: string;
  graceDays?: number;
  revealedAtAuthoring?: boolean;
  enabled?: boolean;
}

export async function createRetentionRule(
  draft: RetentionRuleDraft,
  signal?: AbortSignal,
): Promise<RetentionRuleInfo> {
  const data = await graphql<{ createRetentionRule: RetentionRuleInfo }>(
    CREATE_RETENTION_RULE_MUTATION,
    draft as unknown as Record<string, unknown>,

    signal,
  );
  return data.createRetentionRule;
}

export interface RetentionRulePatch {
  id: string;
  name?: string;
  sourceExpr?: string;
  targetLibraryID?: string;
  graceDays?: number;
  revealedAtAuthoring?: boolean;
  enabled?: boolean;
}

export async function updateRetentionRule(
  patch: RetentionRulePatch,
  signal?: AbortSignal,
): Promise<RetentionRuleInfo> {
  const data = await graphql<{ updateRetentionRule: RetentionRuleInfo }>(
    UPDATE_RETENTION_RULE_MUTATION,
    patch as unknown as Record<string, unknown>,

    signal,
  );
  return data.updateRetentionRule;
}

export async function deleteRetentionRule(
  id: string,
  signal?: AbortSignal,
): Promise<RetentionRuleInfo> {
  const data = await graphql<{ deleteRetentionRule: RetentionRuleInfo }>(
    DELETE_RETENTION_RULE_MUTATION,
    { id },

    signal,
  );
  return data.deleteRetentionRule;
}

export async function setRetentionCandidateHold(
  ruleID: string,
  mediaID: string,
  hold: boolean,

  signal?: AbortSignal,
): Promise<RetentionCandidateHoldResult> {
  const data = await graphql<{
    setRetentionCandidateHold: RetentionCandidateHoldResult;
  }>(SET_RETENTION_CANDIDATE_HOLD_MUTATION, { ruleID, mediaID, hold }, signal);
  return data.setRetentionCandidateHold;
}
