import type { ActivityRecord } from "@/lib/graphql/activity";

// How the Console Activity page reads the activity log: one sentence per
// action in the server's closed vocabulary (internal/store/activity.go), a
// category for filtering, and locale-aware times. activityDisplay.test.ts
// reads the Go vocabulary and fails when an action has no entry here.
//
// The text is English only and lives here, not in the localized string
// registry, until that registry can take it.

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

export const ACTIVITY_CATEGORY_LABELS: Record<ActivityCategory, string> = {
  security: "Security",
  access: "Profiles & access",
  library: "Library & media",
  plugins: "Plugins & features",
  settings: "Settings",
  playback: "Playback",
  system: "System",
  other: "Other",
};

export const ACTIVITY_TEXT = {
  lead: "What people, devices and plugins changed on this server, newest first.",
  searchLabel: "Search activity",
  categoryLabel: "Category",
  allCategories: "All",
  when: "When",
  what: "What happened",
  showDetails: "Show details",
  hideDetails: "Hide details",
  rawAction: "Event key",
  target: "Target",
  profile: "Profile",
  device: "Device",
  library: "Library",
  apiToken: "API token",
  apiKey: "API key",
  exactTime: "Exact time",
  showing: "Showing {shown} of the latest {total} events",
} as const;

/** What an entry's TargetID names, when the action fixes it. */
type TargetKind = "profile" | "device" | "library" | "plugin" | "capability" | "configDomain";

interface ActivityLabel {
  category: ActivityCategory;
  /** `{actor}` and `{target}` are filled in; the result is sentence-cased. */
  sentence: string;
  /** Stands in for `{target}` when its name cannot be resolved. */
  noun?: string;
  /** Overrides the entry's own targetType when the action fixes the kind. */
  target?: TargetKind;
}

function label(
  category: ActivityCategory,
  sentence: string,
  noun?: string,
  target?: TargetKind,
): ActivityLabel {
  return { category, sentence, noun, target };
}

export const ACTIVITY_LABELS: Record<string, ActivityLabel> = {
  // Request lifecycle.
  "mutation.attempt": label("system", "{actor} started a change"),
  "mutation.finished": label("system", "A change by {actor} finished"),

  // Security: unlocks, sign-in, credentials, devices.
  "crypto.lifecycle.prepared": label("security", "{actor} started an encryption key change"),
  reveal: label("security", "{actor} unlocked restricted libraries"),
  "reveal.denied": label("security", "An attempt by {actor} to unlock restricted libraries failed"),
  relock: label("security", "{actor} locked restricted libraries again"),
  "admin.login": label("security", "{actor} unlocked the Console"),
  "admin.login.denied": label("security", "An attempt by {actor} to unlock the Console failed"),
  "admin.password.set": label("security", "{actor} set the admin password"),
  "signin_security.mode": label("security", "{actor} changed the sign-in security mode"),
  "token.create": label("security", "{actor} created an API token"),
  "token.revoke": label("security", "{actor} revoked an API token"),
  "key.create": label("security", "{actor} created an API key"),
  "key.revoke": label("security", "{actor} revoked an API key"),
  "pairing.code": label("security", "{actor} created a device pairing code"),
  "device.enroll": label("security", "{target} was paired with this server", "a device", "device"),
  "enroll.denied": label("security", "A device pairing attempt was refused"),
  "device.promote": label("security", "{target} became a manager device", "a device", "device"),
  "promote.denied": label("security", "An attempt to make a device a manager was refused"),
  "device.revoke": label("security", "{actor} revoked {target}", "a device", "device"),
  "device.role": label("security", "{actor} changed the role of {target}", "a device", "device"),

  // Profiles and access.
  "profile.switch": label("access", "{actor} switched profiles"),
  "profile.library": label(
    "access",
    "{actor} changed which libraries {target} can see",
    "a profile",
    "profile",
  ),
  "profile.access_schedule": label(
    "access",
    "{actor} changed the access schedule for {target}",
    "a profile",
    "profile",
  ),
  "profile.owner_transfer": label(
    "access",
    "{actor} made {target} the owner",
    "another profile",
    "profile",
  ),
  "library.restriction": label(
    "access",
    "{actor} changed the restriction on {target}",
    "a library",
    "library",
  ),
  "mydata.delete": label("access", "{actor} deleted their own watch data"),
  "mydata.operator_delete": label(
    "access",
    "{actor} deleted the watch data of {target}",
    "a profile",
    "profile",
  ),
  "access.request": label("access", "{actor} asked for access to a locked item"),
  "access.resolve": label("access", "{actor} answered an access request"),
  "extension.request": label("access", "{actor} asked for more time"),
  "extension.response": label("access", "{actor} answered a request for more time"),
  "extension.preauth": label(
    "access",
    "{actor} changed automatic extra time for {target}",
    "a profile",
    "profile",
  ),

  // Library and media.
  "library.delete": label("library", "{actor} deleted {target}", "a library", "library"),
  "library.roots": label(
    "library",
    "{actor} changed the folders of {target}",
    "a library",
    "library",
  ),
  "library.icon": label("library", "{actor} changed the icon of {target}", "a library", "library"),
  "library.preview_clips": label(
    "library",
    "{actor} changed preview clips for {target}",
    "a library",
    "library",
  ),
  "media.trash": label("library", "{actor} moved {target} to Trash", "an item"),
  "media.restore": label("library", "{actor} restored {target} from Trash", "an item"),
  "trash.purge": label("library", "{actor} permanently emptied Trash"),
  "retention.trash": label("library", "A retention rule moved an item to Trash"),
  "retention_rule.delete": label("library", "{actor} deleted a retention rule"),
  "collection.trash": label("library", "{actor} moved a collection to Trash"),
  "marker.delete": label("library", "{actor} deleted a marker"),
  "channel.delete": label("library", "{actor} deleted a channel"),
  "saved_filter.delete": label("library", "{actor} deleted a saved filter"),
  "smart_collection.delete": label("library", "{actor} deleted a smart collection"),
  "saved_station.delete": label("library", "{actor} deleted a saved station"),
  "scrobble.unlink": label("library", "{actor} unlinked a scrobbling account"),
  "offline_pack_tokens.delete": label("library", "{actor} removed offline download access"),
  "media.ai_tag_accept": label("library", "{actor} approved a suggested tag"),
  "artwork.pin": label("library", "{actor} pinned artwork"),
  "subscription.retention": label("library", "{actor} changed how long a subscription keeps items"),
  "subscription.entry_association": label(
    "library",
    "{actor} matched a subscription entry to an item",
  ),
  "media.bulk_edit": label("library", "{actor} edited several items at once"),
  "media.organize": label("library", "{actor} reorganized files"),
  "media.merge": label("library", "{actor} merged items"),
  "media.field_lock": label("library", "{actor} locked or unlocked a field on an item"),
  "media.relation": label("library", "{actor} changed how an item is related to others"),
  "entity.merge": label("library", "{actor} merged two entries"),
  "entity.relation": label("library", "{actor} changed how an entry is related to others"),
  "batch.undo": label("library", "{actor} undid a batch change"),
  "managed_file.create": label("library", "{actor} added a managed file"),
  "managed_file.move": label("library", "{actor} moved a managed file"),
  "managed_file.delete": label("library", "{actor} deleted a managed file"),
  "managed_file.restore": label("library", "{actor} restored a managed file"),
  "tag.create": label("library", "{actor} created a tag"),
  "tag.update": label("library", "{actor} changed a tag"),
  "tag.delete": label("library", "{actor} deleted a tag"),
  "tag.restore": label("library", "{actor} restored a tag"),
  "tag.assign": label("library", "{actor} tagged an item"),
  "tag.bulk_assign": label("library", "{actor} tagged several items at once"),

  // Plugins and features.
  "plugin.activation": label("plugins", "{actor} turned {target} on or off", "a plugin", "plugin"),
  "plugin.uninstall": label(
    "plugins",
    "{actor} uninstalled {target} and deleted its data",
    "a plugin",
    "plugin",
  ),
  "plugin.grant": label(
    "plugins",
    "{actor} changed the permissions of {target}",
    "a plugin",
    "plugin",
  ),
  "plugin.update": label("plugins", "{actor} updated {target}", "a plugin", "plugin"),
  "plugin.catalog_read": label("plugins", "{target} read the catalog", "a plugin", "plugin"),
  "plugin.ingest_request": label("plugins", "{target} asked to add files", "a plugin", "plugin"),
  "plugin.file_mutation_request": label(
    "plugins",
    "{target} asked to change files",
    "a plugin",
    "plugin",
  ),
  "plugin.profile_link": label(
    "plugins",
    "{target} linked an account for {actor}",
    "a plugin",
    "plugin",
  ),
  "plugin.outbound_request": label(
    "plugins",
    "{target} contacted an outside service",
    "a plugin",
    "plugin",
  ),
  "plugin.dead_letter_retry": label(
    "plugins",
    "{actor} retried a failed delivery for {target}",
    "a plugin",
    "plugin",
  ),
  "capability.toggle": label(
    "plugins",
    "{actor} turned {target} on or off",
    "a feature",
    "capability",
  ),

  // Settings.
  "config.update": label("settings", "{actor} changed {target}", "server settings", "configDomain"),
  "schedule.update": label("settings", "{actor} changed a task schedule"),
  "path_picker.root_added": label("settings", "{actor} allowed a new folder location"),

  // Playback.
  "session.terminate": label("playback", "{actor} ended a playback session"),
  "session.control": label("playback", "Playback for {actor} was controlled from another device"),
  "group.control": label("playback", "{actor} changed a synced playback group"),
};

/** Names the page has resolved for the identifiers an entry carries. */
export interface ActivityNames {
  profiles: ReadonlyMap<string, string>;
  devices: ReadonlyMap<string, string>;
  libraries: ReadonlyMap<string, string>;
}

export const NO_ACTIVITY_NAMES: ActivityNames = {
  profiles: new Map(),
  devices: new Map(),
  libraries: new Map(),
};

export interface ActivityRow {
  sentence: string;
  category: ActivityCategory;
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

function actorName(entry: ActivityRecord, names: ActivityNames): string {
  const profile = entry.profileID ? names.profiles.get(entry.profileID) : undefined;
  if (profile) return profile;
  if (entry.tokenID) return "an API token";
  if (entry.apiKeyID) return "an API key";
  return entry.profileID ? "a profile that no longer exists" : "someone";
}

function targetName(
  entry: ActivityRecord,
  kind: string | undefined,
  names: ActivityNames,
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
    case "plugin":
      return id ? `the ${id} plugin` : undefined;
    case "capability":
      return id ? `the ${humanizeActivityKey(id).toLowerCase()} feature` : undefined;
    case "configDomain":
      return entry.targetType
        ? `${humanizeActivityKey(entry.targetType).toLowerCase()} settings`
        : undefined;
    default:
      return undefined;
  }
}

/** Turns one entry into the sentence, category and names its row shows. */
export function describeActivity(
  entry: ActivityRecord,
  names: ActivityNames = NO_ACTIVITY_NAMES,
): ActivityRow {
  const known = ACTIVITY_LABELS[entry.action];
  const row = {
    profileName: entry.profileID ? names.profiles.get(entry.profileID) : undefined,
    deviceName: entry.deviceID ? names.devices.get(entry.deviceID) : undefined,
    libraryName: entry.libraryID ? names.libraries.get(entry.libraryID) : undefined,
  };
  if (!known) {
    return { ...row, sentence: humanizeActivityKey(entry.action), category: "other", known: false };
  }
  const target =
    targetName(entry, known.target ?? entry.targetType, names) ?? known.noun ?? "something";
  const sentence = known.sentence
    .replace("{actor}", actorName(entry, names))
    .replace("{target}", target);
  return { ...row, sentence: sentenceCase(sentence), category: known.category, known: true };
}

/** Whether a row matches the search text (sentence, raw key, names, ids). */
export function activityMatches(entry: ActivityRecord, row: ActivityRow, query: string): boolean {
  const needle = query.trim().toLowerCase();
  if (!needle) return true;
  return [
    row.sentence,
    ACTIVITY_CATEGORY_LABELS[row.category],
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
