import type { ActivityRecord } from "@/lib/graphql/activity";
import type { StringKey } from "@/strings";

// How the Console Activity page reads the activity log: one sentence per
// action in the server's closed vocabulary (internal/store/activity.go), a
// category for filtering, and locale-aware times. activityDisplay.test.ts
// reads the Go vocabulary and fails when an action has no entry here. The
// sentences, categories and page text live in the localized string catalog
// (src/strings/activity-*.ts, CORE-1208).

/** The `t` of useT(), or any function with its shape. */
export type Translate = (key: StringKey, values?: Record<string, string | number>) => string;

export type ActivityCategory =
  | "security"
  | "access"
  | "library"
  | "plugins"
  | "settings"
  | "playback"
  | "system"
  | "other";

export const ACTIVITY_CATEGORIES: readonly ActivityCategory[] = [
  "security",
  "access",
  "library",
  "plugins",
  "settings",
  "playback",
  "system",
  "other",
];

export const ACTIVITY_CATEGORY_LABELS: Record<ActivityCategory, StringKey> = {
  security: "activityCategorySecurity",
  access: "activityCategoryAccess",
  library: "activityCategoryLibrary",
  plugins: "activityCategoryPlugins",
  settings: "activityCategorySettings",
  playback: "activityCategoryPlayback",
  system: "activityCategorySystem",
  other: "activityCategoryOther",
};

/** What an entry's TargetID names, when the action fixes it. */
type TargetKind =
  | "profile"
  | "device"
  | "library"
  | "plugin"
  | "capability"
  | "configDomain"
  | "media";

interface ActivityLabel {
  category: ActivityCategory;
  /** `{actor}` and `{target}` are filled in; the result is sentence-cased. */
  sentence: StringKey;
  /** Stands in for `{target}` when its name cannot be resolved. */
  noun?: StringKey;
  /** Overrides the entry's own targetType when the action fixes the kind. */
  target?: TargetKind;
}

function label(
  category: ActivityCategory,
  sentence: StringKey,
  noun?: StringKey,
  target?: TargetKind,
): ActivityLabel {
  return { category, sentence, noun, target };
}

export const ACTIVITY_LABELS: Record<string, ActivityLabel> = {
  // Request lifecycle.
  "mutation.attempt": label("system", "activityEventMutationAttempt"),
  "mutation.finished": label("system", "activityEventMutationFinished"),

  // Security: unlocks, sign-in, credentials, devices.
  "crypto.lifecycle.prepared": label("security", "activityEventCryptoLifecyclePrepared"),
  reveal: label("security", "activityEventReveal"),
  "reveal.denied": label("security", "activityEventRevealDenied"),
  relock: label("security", "activityEventRelock"),
  "admin.login": label("security", "activityEventAdminLogin"),
  "admin.login.denied": label("security", "activityEventAdminLoginDenied"),
  "admin.password.set": label("security", "activityEventAdminPasswordSet"),
  "signin_security.mode": label("security", "activityEventSigninSecurityMode"),
  "token.create": label("security", "activityEventTokenCreate"),
  "token.revoke": label("security", "activityEventTokenRevoke"),
  "key.create": label("security", "activityEventKeyCreate"),
  "key.revoke": label("security", "activityEventKeyRevoke"),
  "pairing.code": label("security", "activityEventPairingCode"),
  "device.enroll": label("security", "activityEventDeviceEnroll", "activityNounDevice", "device"),
  "enroll.denied": label("security", "activityEventEnrollDenied"),
  "device.promote": label("security", "activityEventDevicePromote", "activityNounDevice", "device"),
  "promote.denied": label("security", "activityEventPromoteDenied"),
  "device.revoke": label("security", "activityEventDeviceRevoke", "activityNounDevice", "device"),
  "device.role": label("security", "activityEventDeviceRole", "activityNounDevice", "device"),

  // Profiles and access.
  "profile.switch": label("access", "activityEventProfileSwitch"),
  "profile.order": label("access", "activityEventProfileOrder"),
  "profile.avatar": label("access", "activityEventProfileAvatar", "activityNounProfile", "profile"),
  "profile.picture": label(
    "access",
    "activityEventProfilePicture",
    "activityNounProfile",
    "profile",
  ),
  "profile.library": label(
    "access",
    "activityEventProfileLibrary",
    "activityNounProfile",
    "profile",
  ),
  "profile.access_schedule": label(
    "access",
    "activityEventProfileAccessSchedule",
    "activityNounProfile",
    "profile",
  ),
  "profile.owner_transfer": label(
    "access",
    "activityEventProfileOwnerTransfer",
    "activityNounOtherProfile",
    "profile",
  ),
  "library.restriction": label(
    "access",
    "activityEventLibraryRestriction",
    "activityNounLibrary",
    "library",
  ),
  "mydata.delete": label("access", "activityEventMydataDelete"),
  "mydata.operator_delete": label(
    "access",
    "activityEventMydataOperatorDelete",
    "activityNounProfile",
    "profile",
  ),
  "access.request": label("access", "activityEventAccessRequest"),
  "access.resolve": label("access", "activityEventAccessResolve"),
  "extension.request": label("access", "activityEventExtensionRequest"),
  "extension.response": label("access", "activityEventExtensionResponse"),
  "extension.preauth": label(
    "access",
    "activityEventExtensionPreauth",
    "activityNounProfile",
    "profile",
  ),

  // Library and media.
  "library.delete": label(
    "library",
    "activityEventLibraryDelete",
    "activityNounLibrary",
    "library",
  ),
  "library.restore": label(
    "library",
    "activityEventLibraryRestore",
    "activityNounLibrary",
    "library",
  ),
  "library.purge": label("library", "activityEventLibraryPurge", "activityNounLibrary", "library"),
  "library.roots": label("library", "activityEventLibraryRoots", "activityNounLibrary", "library"),
  "scan.preview": label("library", "activityEventScanPreview", "activityNounLibrary", "library"),
  "scan.preview_commit": label(
    "library",
    "activityEventScanPreviewCommit",
    "activityNounLibrary",
    "library",
  ),
  "library.icon": label("library", "activityEventLibraryIcon", "activityNounLibrary", "library"),
  "library.preview_clips": label(
    "library",
    "activityEventLibraryPreviewClips",
    "activityNounLibrary",
    "library",
  ),
  "library.show_on_home": label(
    "library",
    "activityEventLibraryShowOnHome",
    "activityNounLibrary",
    "library",
  ),
  "media.trash": label("library", "activityEventMediaTrash", "activityNounItem", "media"),
  "media.restore": label("library", "activityEventMediaRestore", "activityNounItem", "media"),
  "trash.purge": label("library", "activityEventTrashPurge"),
  "retention.trash": label("library", "activityEventRetentionTrash"),
  "retention_rule.delete": label("library", "activityEventRetentionRuleDelete"),
  "collection.trash": label("library", "activityEventCollectionTrash"),
  "marker.delete": label("library", "activityEventMarkerDelete"),
  "channel.delete": label("library", "activityEventChannelDelete"),
  "saved_filter.delete": label("library", "activityEventSavedFilterDelete"),
  "smart_collection.delete": label("library", "activityEventSmartCollectionDelete"),
  "saved_station.delete": label("library", "activityEventSavedStationDelete"),
  "scrobble.unlink": label("library", "activityEventScrobbleUnlink"),
  "offline_pack_tokens.delete": label("library", "activityEventOfflinePackTokensDelete"),
  "media.ai_tag_accept": label("library", "activityEventMediaAiTagAccept"),
  "artwork.pin": label("library", "activityEventArtworkPin", "activityNounItem", "media"),
  "subscription.retention": label("library", "activityEventSubscriptionRetention"),
  "subscription.entry_association": label("library", "activityEventSubscriptionEntryAssociation"),
  "media.bulk_edit": label("library", "activityEventMediaBulkEdit"),
  "media.organize": label("library", "activityEventMediaOrganize"),
  "media.merge": label("library", "activityEventMediaMerge"),
  "media.field_lock": label("library", "activityEventMediaFieldLock", "activityNounItem", "media"),
  "media.metadata_edit": label(
    "library",
    "activityEventMediaMetadataEdit",
    "activityNounItem",
    "media",
  ),
  "media.metadata_refresh": label(
    "library",
    "activityEventMediaMetadataRefresh",
    "activityNounItem",
    "media",
  ),
  "media.relation": label("library", "activityEventMediaRelation", "activityNounItem", "media"),
  "entity.merge": label("library", "activityEventEntityMerge"),
  "entity.relation": label("library", "activityEventEntityRelation"),
  "batch.undo": label("library", "activityEventBatchUndo"),
  "managed_file.create": label("library", "activityEventManagedFileCreate"),
  "managed_file.move": label("library", "activityEventManagedFileMove"),
  "managed_file.delete": label("library", "activityEventManagedFileDelete"),
  "managed_file.restore": label("library", "activityEventManagedFileRestore"),
  "tag.create": label("library", "activityEventTagCreate"),
  "tag.update": label("library", "activityEventTagUpdate"),
  "tag.delete": label("library", "activityEventTagDelete"),
  "tag.restore": label("library", "activityEventTagRestore"),
  "tag.assign": label("library", "activityEventTagAssign"),
  "tag.bulk_assign": label("library", "activityEventTagBulkAssign"),

  // Plugins and features.
  "plugin.activation": label(
    "plugins",
    "activityEventPluginActivation",
    "activityNounPlugin",
    "plugin",
  ),
  "plugin.uninstall": label(
    "plugins",
    "activityEventPluginUninstall",
    "activityNounPlugin",
    "plugin",
  ),
  "plugin.grant": label("plugins", "activityEventPluginGrant", "activityNounPlugin", "plugin"),
  "plugin.update": label("plugins", "activityEventPluginUpdate", "activityNounPlugin", "plugin"),
  "plugin.catalog_read": label(
    "plugins",
    "activityEventPluginCatalogRead",
    "activityNounPlugin",
    "plugin",
  ),
  "plugin.ingest_request": label(
    "plugins",
    "activityEventPluginIngestRequest",
    "activityNounPlugin",
    "plugin",
  ),
  "plugin.file_mutation_request": label(
    "plugins",
    "activityEventPluginFileMutationRequest",
    "activityNounPlugin",
    "plugin",
  ),
  "plugin.profile_link": label(
    "plugins",
    "activityEventPluginProfileLink",
    "activityNounPlugin",
    "plugin",
  ),
  "plugin.outbound_request": label(
    "plugins",
    "activityEventPluginOutboundRequest",
    "activityNounPlugin",
    "plugin",
  ),
  "plugin.metadata_write": label(
    "plugins",
    "activityEventPluginMetadataWrite",
    "activityNounPlugin",
    "plugin",
  ),
  "plugin.collection_write": label(
    "plugins",
    "activityEventPluginCollectionWrite",
    "activityNounPlugin",
    "plugin",
  ),
  "plugin.artwork_write": label(
    "plugins",
    "activityEventPluginArtworkWrite",
    "activityNounPlugin",
    "plugin",
  ),
  "plugin.watch_state_write": label(
    "plugins",
    "activityEventPluginWatchStateWrite",
    "activityNounPlugin",
    "plugin",
  ),
  "plugin.playback_state_read": label(
    "plugins",
    "activityEventPluginPlaybackStateRead",
    "activityNounPlugin",
    "plugin",
  ),
  "plugin.dead_letter_retry": label(
    "plugins",
    "activityEventPluginDeadLetterRetry",
    "activityNounPlugin",
    "plugin",
  ),
  "capability.toggle": label(
    "plugins",
    "activityEventCapabilityToggle",
    "activityNounFeature",
    "capability",
  ),

  // Settings.
  "config.update": label(
    "settings",
    "activityEventConfigUpdate",
    "activityNounServerSettings",
    "configDomain",
  ),
  "schedule.update": label("settings", "activityEventScheduleUpdate"),
  "path_picker.root_added": label("settings", "activityEventPathPickerRootAdded"),

  // Playback.
  "session.terminate": label("playback", "activityEventSessionTerminate"),
  "session.control": label("playback", "activityEventSessionControl"),
  "group.control": label("playback", "activityEventGroupControl"),
};

/** Names the page has resolved for the identifiers an entry carries. */
export interface ActivityNames {
  profiles: ReadonlyMap<string, string>;
  devices: ReadonlyMap<string, string>;
  libraries: ReadonlyMap<string, string>;
  /** Titles of the Media items the viewer may see; hidden items are absent. */
  media: ReadonlyMap<string, string>;
}

export const NO_ACTIVITY_NAMES: ActivityNames = {
  profiles: new Map(),
  devices: new Map(),
  libraries: new Map(),
  media: new Map(),
};

export interface ActivityRow {
  sentence: string;
  category: ActivityCategory;
  categoryLabel: string;
  /** False when the action has no sentence and the readable key is shown. */
  known: boolean;
  profileName?: string;
  deviceName?: string;
  libraryName?: string;
}

/** "saved_filter.delete" -> "Saved filter delete". */
export function humanizeActivityKey(key: string): string {
  const words = key
    .trim()
    .replace(/[._-]+/g, " ")
    .replace(/\s+/g, " ")
    .toLowerCase();
  return sentenceCase(words);
}

function sentenceCase(text: string): string {
  return text.charAt(0).toUpperCase() + text.slice(1);
}

function actorName(entry: ActivityRecord, names: ActivityNames, t: Translate): string {
  const profile = entry.profileID ? names.profiles.get(entry.profileID) : undefined;
  if (profile) return profile;
  if (entry.tokenID) return t("activityActorApiToken");
  if (entry.apiKeyID) return t("activityActorApiKey");
  return t(entry.profileID ? "activityActorGoneProfile" : "activityActorSomeone");
}

function targetName(
  entry: ActivityRecord,
  kind: string | undefined,
  names: ActivityNames,
  t: Translate,
): string | undefined {
  const id = entry.targetID;
  switch (kind) {
    case "profile":
      return id ? names.profiles.get(id) : undefined;
    case "device":
      // Enrollment and promotion act on the calling device itself.
      return names.devices.get(id ?? "") ?? names.devices.get(entry.deviceID ?? "");
    case "library":
      return names.libraries.get(id ?? "") ?? names.libraries.get(entry.libraryID ?? "");
    case "media":
      // Only a Media target carries a Media id; the server reveal-filters the title lookup.
      return entry.targetType === "media" && id ? names.media.get(id) : undefined;
    case "plugin":
      return id ? t("activityTargetPlugin", { plugin: id }) : undefined;
    case "capability":
      return id
        ? t("activityTargetFeature", { feature: humanizeActivityKey(id).toLowerCase() })
        : undefined;
    case "configDomain":
      return entry.targetType
        ? t("activityTargetSettings", { area: humanizeActivityKey(entry.targetType).toLowerCase() })
        : undefined;
    default:
      return undefined;
  }
}

/** Turns one entry into the sentence, category and names its row shows. */
export function describeActivity(
  entry: ActivityRecord,
  t: Translate,
  names: ActivityNames = NO_ACTIVITY_NAMES,
): ActivityRow {
  const known = ACTIVITY_LABELS[entry.action];
  const row = {
    profileName: entry.profileID ? names.profiles.get(entry.profileID) : undefined,
    deviceName: entry.deviceID ? names.devices.get(entry.deviceID) : undefined,
    libraryName: entry.libraryID ? names.libraries.get(entry.libraryID) : undefined,
  };
  if (!known) {
    return {
      ...row,
      sentence: humanizeActivityKey(entry.action),
      category: "other",
      categoryLabel: t(ACTIVITY_CATEGORY_LABELS.other),
      known: false,
    };
  }
  const target =
    targetName(entry, known.target ?? entry.targetType, names, t) ??
    t(known.noun ?? "activityNounSomething");
  const sentence = t(known.sentence, { actor: actorName(entry, names, t), target });
  return {
    ...row,
    sentence: sentenceCase(sentence),
    category: known.category,
    categoryLabel: t(ACTIVITY_CATEGORY_LABELS[known.category]),
    known: true,
  };
}

/** Whether a row matches the search text (sentence, category, raw key, names, ids). */
export function activityMatches(entry: ActivityRecord, row: ActivityRow, query: string): boolean {
  const needle = query.trim().toLowerCase();
  if (!needle) return true;
  return [
    row.sentence,
    row.categoryLabel,
    row.profileName,
    row.deviceName,
    row.libraryName,
    entry.action,
    entry.targetType,
    entry.targetID,
  ].some((field) => field?.toLowerCase().includes(needle));
}

// --- Times ------------------------------------------------------------------

const RELATIVE_STEPS: [limit: number, seconds: number, unit: Intl.RelativeTimeFormatUnit][] = [
  [3_600, 60, "minute"],
  [86_400, 3_600, "hour"],
  [30 * 86_400, 86_400, "day"],
  [365 * 86_400, 30 * 86_400, "month"],
  [Number.POSITIVE_INFINITY, 365 * 86_400, "year"],
];

/** "5 minutes ago", in the viewer's locale. Under a minute reads as "now". */
export function formatRelativeTime(at: Date, now: Date, locale: string): string {
  const elapsed = Math.round((at.getTime() - now.getTime()) / 1000);
  const format = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  const magnitude = Math.abs(elapsed);
  if (magnitude < 60) return format.format(0, "second");
  for (const [limit, seconds, unit] of RELATIVE_STEPS) {
    if (magnitude < limit) return format.format(Math.trunc(elapsed / seconds), unit);
  }
  return format.format(0, "second");
}

/** The date and time in the viewer's locale and time zone. */
export function formatLocalTime(at: Date, locale: string, timeZone?: string): string {
  return new Intl.DateTimeFormat(locale, {
    dateStyle: "medium",
    timeStyle: "short",
    timeZone,
  }).format(at);
}

/** "2026-11-12 03:23:23 UTC" — the exact, zone-free form for hover and details. */
export function formatUtcTime(at: Date): string {
  return `${at.toISOString().slice(0, 19).replace("T", " ")} UTC`;
}

/** Parses the wire timestamp; null when it is not a date. */
export function parseActivityTime(occurredAt: string): Date | null {
  const at = new Date(occurredAt);
  return Number.isNaN(at.getTime()) ? null : at;
}
