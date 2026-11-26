import {
  createContext,
  createElement,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router";
import { PREF_KEYS } from "@/areas/viewer/lib/preferences";
import { getIdentityEpochSignal } from "@/lib/api";
import { displayString } from "@/lib/display";
import type {
  Layout,
  LibrarySummary,
  NavigationManifest,
  NavigationNode,
  Section,
  SourceItem,
  ViewerPluginInfo,
} from "@/lib/graphql/types";
import { fetchClientPrefs, fetchViewerPlugins, setClientPref } from "@/lib/graphql/viewer";
import { useOptionalDisplayLocale, useT } from "@/lib/i18n";
import { findNodeBySlug } from "@/lib/nodePath";
import { useSession } from "@/lib/session";
import type { StringKey } from "@/strings";

export type ExperienceModeId = string;
export type LaunchPolicy = "remember" | "fixed" | "ask";

export interface ExperienceMode {
  id: ExperienceModeId;
  kind: "all" | "watch" | "listen" | "modality";
  labelKey?: StringKey;
  fallbackLabel: string;
  libraryIDs: string[];
  mediaClasses: string[];
}

export interface LaunchExperiencePref {
  policy: LaunchPolicy;
  fixedMode: ExperienceModeId;
}

export const DEFAULT_LAUNCH_EXPERIENCE: LaunchExperiencePref = {
  policy: "remember",
  fixedMode: "all",
};

const LAUNCH_STORAGE_PREFIX = "beamfall.experienceLaunch.v1";
const LAST_MODE_STORAGE_PREFIX = "beamfall.experienceLastMode.v1";
const PLUGINS_STORAGE_KEY = "beamfall.viewerPlugins.v1";
const ASK_DISMISSED_SESSION_PREFIX = "beamfall.launchLensChosen.v1";

const MODE_LABEL_KEYS: Readonly<Record<string, StringKey>> = {
  all: "lensModeAll",
  watch: "lensModeWatch",
  listen: "lensModeListen",
  image: "lensModePhotos",
};

const KNOWN_MODALITY_HINTS: readonly string[] = ["all", "watch", "listen", "image"];

const LAUNCH_EXEMPT_PREFIXES: readonly string[] = [
  "/profiles",
  "/watch/",
  "/view/",
  "/party/stage/",
  "/settings",
  "/setup",
  "/enroll",
];

// Closed core icon vocabulary hints used ONLY when authoritative plugin metadata
// has not yet loaded or for unbound libraries carrying an explicit media icon.
const ICON_MODALITY_HINTS: Readonly<Record<string, string>> = {
  film: "watch",
  tv: "watch",
  video: "watch",
  mic: "watch",
  note: "listen",
  headphones: "listen",
  broadcast: "listen",
  image: "image",
  camera: "image",
};

function normalizeMediaClasses(classes: readonly string[] | undefined | null): string[] {
  if (!classes?.length) return [];
  const out: string[] = [];
  for (const raw of classes) {
    const clean = raw.trim().toLowerCase();
    if (clean && !out.includes(clean)) out.push(clean);
  }
  return out;
}

function titleCaseSlug(slug: string): string {
  const clean = slug.trim().replace(/[-_]+/g, " ");
  if (!clean) return slug;
  return clean.replace(/\b[a-z]/g, (ch) => ch.toUpperCase());
}

/**
 * Pure plugin-driven classification of a single Library into an experience modality.
 * ZERO hardcoded plugin IDs: inspects declared `mediaClasses`, plugin `presentation`
 * (`selectPlays`, `interactiveSidecars`, `resumeMedia`) and `capabilities`, with
 * layout source and core icon-vocabulary fallbacks only when plugin metadata is absent.
 */
export function classifyLibraryModality(
  library: Pick<LibrarySummary, "id" | "pluginID" | "icon">,
  plugin?: ViewerPluginInfo | null,
  layout?: Layout | null,
): { modality: string | null; mediaClasses: string[]; dynamicLabel?: string } {
  const classes = normalizeMediaClasses(plugin?.mediaClasses);
  const presentation = plugin?.presentation;
  const sidecarCount = presentation?.interactiveSidecars?.length ?? 0;

  if (classes.length > 0) {
    const primary = classes[0];
    const hasVideo = classes.includes("video");
    const isInteractiveStage = hasVideo && (presentation?.selectPlays === true || sidecarCount > 0);

    if (primary === "video" || isInteractiveStage) {
      return {
        modality: "watch",
        mediaClasses: hasVideo ? ["video"] : classes,
      };
    }
    if (primary === "audio") {
      return {
        modality: "listen",
        mediaClasses: ["audio"],
      };
    }
    if (primary === "image") {
      return {
        modality: "image",
        mediaClasses: ["image"],
      };
    }
    return {
      modality: primary,
      mediaClasses: [primary],
      dynamicLabel: titleCaseSlug(primary),
    };
  }

  // Fallback before GraphQL `plugins` settles (or unbound libraries): inspect the
  // library's declared layout sections, then Core's closed icon vocabulary.
  if (layout?.sections?.length) {
    for (const section of layout.sections) {
      const kind = section.source?.kind;
      const value = section.source?.value;
      if (kind === "on-this-day") {
        return { modality: "image", mediaClasses: ["image"] };
      }
      if (
        (kind === "next-up" && value === "audio") ||
        kind === "top-tracks" ||
        kind === "top-albums" ||
        kind === "top-artists"
      ) {
        return { modality: "listen", mediaClasses: ["audio"] };
      }
      if (kind === "next-up" && value === "video") {
        return { modality: "watch", mediaClasses: ["video"] };
      }
    }
  }

  const iconModality = library.icon ? ICON_MODALITY_HINTS[library.icon] : undefined;
  if (iconModality === "watch") return { modality: "watch", mediaClasses: ["video"] };
  if (iconModality === "listen") return { modality: "listen", mediaClasses: ["audio"] };
  if (iconModality === "image") return { modality: "image", mediaClasses: ["image"] };

  return { modality: null, mediaClasses: [] };
}

/**
 * Derive available Experience Modes dynamically from active Libraries + declared plugin
 * metadata (`all`, plus `watch`, `listen`, `image`, and any extra declared classes
 * when libraries of that class exist).
 */
export function deriveExperienceModes(
  libraries: readonly LibrarySummary[],
  plugins: readonly ViewerPluginInfo[],
  layouts: readonly Layout[] = [],
  navigation: readonly NavigationNode[] = [],
  locale = "en",
): ExperienceMode[] {
  const pluginsById = new Map(plugins.map((plugin) => [plugin.id, plugin]));
  const layoutsById = new Map(layouts.map((layout) => [layout.id, layout]));
  const layoutIdByLibraryId = new Map<string, string>();
  for (const node of navigation) {
    if (node.libraryID && node.layoutID) layoutIdByLibraryId.set(node.libraryID, node.layoutID);
  }

  const allLibraryIDs = libraries.map((lib) => lib.id);
  const buckets = new Map<
    string,
    { libraryIDs: string[]; mediaClasses: Set<string>; fallbackLabel: string }
  >();

  for (const library of libraries) {
    const plugin = library.pluginID ? (pluginsById.get(library.pluginID) ?? null) : null;
    const navLayoutId = layoutIdByLibraryId.get(library.id);
    const layout = navLayoutId
      ? (layoutsById.get(navLayoutId) ?? null)
      : library.pluginID
        ? (layoutsById.get(`layout-${library.pluginID}`) ?? null)
        : null;
    const classified = classifyLibraryModality(library, plugin, layout);
    if (!classified.modality) continue;

    let bucket = buckets.get(classified.modality);
    if (!bucket) {
      const pluginTitle = plugin ? displayString(plugin.displayName, locale) : "";
      bucket = {
        libraryIDs: [],
        mediaClasses: new Set<string>(),
        fallbackLabel:
          classified.modality === "watch"
            ? "Watch"
            : classified.modality === "listen"
              ? "Listen"
              : classified.modality === "image"
                ? "Photos"
                : pluginTitle || classified.dynamicLabel || titleCaseSlug(classified.modality),
      };
      buckets.set(classified.modality, bucket);
    }
    bucket.libraryIDs.push(library.id);
    for (const cls of classified.mediaClasses) bucket.mediaClasses.add(cls);
  }

  const modes: ExperienceMode[] = [
    {
      id: "all",
      kind: "all",
      labelKey: "lensModeAll",
      fallbackLabel: "All",
      libraryIDs: allLibraryIDs,
      mediaClasses: [],
    },
  ];

  const pushIfPresent = (id: string, kind: ExperienceMode["kind"]) => {
    const bucket = buckets.get(id);
    if (!bucket || bucket.libraryIDs.length === 0) return;
    modes.push({
      id,
      kind,
      labelKey: MODE_LABEL_KEYS[id],
      fallbackLabel: bucket.fallbackLabel,
      libraryIDs: bucket.libraryIDs,
      mediaClasses: [...bucket.mediaClasses],
    });
    buckets.delete(id);
  };

  pushIfPresent("watch", "watch");
  pushIfPresent("listen", "listen");
  pushIfPresent("image", "modality");

  for (const extraId of [...buckets.keys()].sort()) {
    pushIfPresent(extraId, "modality");
  }

  return modes;
}

export function normalizeExperienceMode(
  requested: string | null | undefined,
  modes: readonly ExperienceMode[],
  allowPendingKnown = false,
): ExperienceModeId {
  const clean = requested?.trim().toLowerCase() ?? "";
  if (!clean) return "all";
  if (modes.some((m) => m.id === clean)) return clean;
  if (allowPendingKnown && KNOWN_MODALITY_HINTS.includes(clean)) return clean;
  return "all";
}

export function sectionMatchesLens(section: Section, mode: ExperienceMode | undefined): boolean {
  if (!mode || mode.id === "all") return true;
  if (section.source?.kind === "next-up") {
    const band = section.source.value?.trim().toLowerCase();
    if (band === "video") return mode.mediaClasses.includes("video");
    if (band === "audio") return mode.mediaClasses.includes("audio");
  }
  return true;
}

export function itemMatchesLens(
  item: Pick<SourceItem, "libraryID" | "mediaClass">,
  mode: ExperienceMode | undefined,
): boolean {
  if (!mode || mode.id === "all") return true;
  if (item.libraryID) return mode.libraryIDs.includes(item.libraryID);
  if (item.mediaClass) return mode.mediaClasses.includes(item.mediaClass.toLowerCase());
  return true;
}

export function parseLaunchExperiencePref(raw: string | null | undefined): LaunchExperiencePref {
  if (!raw) return DEFAULT_LAUNCH_EXPERIENCE;
  try {
    const parsed = JSON.parse(raw) as Partial<LaunchExperiencePref>;
    const policy: LaunchPolicy =
      parsed.policy === "fixed" || parsed.policy === "ask" || parsed.policy === "remember"
        ? parsed.policy
        : "remember";
    const fixedMode =
      typeof parsed.fixedMode === "string" && parsed.fixedMode.trim()
        ? parsed.fixedMode.trim().toLowerCase()
        : "all";
    return { policy, fixedMode };
  } catch {
    return DEFAULT_LAUNCH_EXPERIENCE;
  }
}

export function encodeLaunchExperiencePref(pref: LaunchExperiencePref): string {
  return JSON.stringify({
    policy: pref.policy,
    fixedMode: pref.fixedMode || "all",
  });
}

function safeLocalStorage(): Storage | undefined {
  try {
    return typeof window === "undefined" ? undefined : window.localStorage;
  } catch {
    return undefined;
  }
}

function safeSessionStorage(): Storage | undefined {
  try {
    return typeof window === "undefined" ? undefined : window.sessionStorage;
  } catch {
    return undefined;
  }
}

function profileStorageKey(prefix: string, profileID: string | null | undefined): string {
  return profileID ? `${prefix}.${profileID}` : `${prefix}.default`;
}

export function readStoredLaunchExperience(profileID?: string | null): LaunchExperiencePref {
  const storage = safeLocalStorage();
  try {
    const specific = storage?.getItem(profileStorageKey(LAUNCH_STORAGE_PREFIX, profileID));
    if (specific) return parseLaunchExperiencePref(specific);
    const shared = storage?.getItem(LAUNCH_STORAGE_PREFIX);
    return parseLaunchExperiencePref(shared);
  } catch {
    return DEFAULT_LAUNCH_EXPERIENCE;
  }
}

export function syncLocalLaunchExperience(
  pref: LaunchExperiencePref,
  profileID?: string | null,
): void {
  writeStoredLaunchExperience(pref, profileID);
  emitPrefChange();
}

function writeStoredLaunchExperience(pref: LaunchExperiencePref, profileID?: string | null): void {
  const storage = safeLocalStorage();
  const encoded = encodeLaunchExperiencePref(pref);
  try {
    storage?.setItem(profileStorageKey(LAUNCH_STORAGE_PREFIX, profileID), encoded);
    storage?.setItem(LAUNCH_STORAGE_PREFIX, encoded);
  } catch {
    // Private browsing or quota errors keep in-memory state.
  }
}

export function readStoredLastMode(profileID?: string | null): ExperienceModeId {
  const storage = safeLocalStorage();
  try {
    const specific = storage?.getItem(profileStorageKey(LAST_MODE_STORAGE_PREFIX, profileID));
    if (specific) return specific.trim().toLowerCase() || "all";
    const shared = storage?.getItem(LAST_MODE_STORAGE_PREFIX);
    return shared?.trim().toLowerCase() || "all";
  } catch {
    return "all";
  }
}

function writeStoredLastMode(mode: ExperienceModeId, profileID?: string | null): void {
  const storage = safeLocalStorage();
  try {
    storage?.setItem(profileStorageKey(LAST_MODE_STORAGE_PREFIX, profileID), mode);
    storage?.setItem(LAST_MODE_STORAGE_PREFIX, mode);
  } catch {
    // Ignore storage errors.
  }
}

function readCachedPlugins(): ViewerPluginInfo[] {
  const storage = safeLocalStorage();
  try {
    const raw = storage?.getItem(PLUGINS_STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? (parsed as ViewerPluginInfo[]) : [];
  } catch {
    return [];
  }
}

function writeCachedPlugins(plugins: ViewerPluginInfo[]): void {
  const storage = safeLocalStorage();
  try {
    storage?.setItem(PLUGINS_STORAGE_KEY, JSON.stringify(plugins));
  } catch {
    // Ignore storage errors.
  }
}

function hasAnsweredLaunchLensThisSession(profileID: string | null): boolean {
  const storage = safeSessionStorage();
  try {
    return storage?.getItem(profileStorageKey(ASK_DISMISSED_SESSION_PREFIX, profileID)) === "1";
  } catch {
    return false;
  }
}

function markLaunchLensAnsweredThisSession(profileID: string | null): void {
  const storage = safeSessionStorage();
  try {
    storage?.setItem(profileStorageKey(ASK_DISMISSED_SESSION_PREFIX, profileID), "1");
  } catch {
    // Ignore storage errors.
  }
}

/** Test helper for resetting session-level Launch Lens dismissal state. */
export function resetLaunchLensSession(profileID?: string | null): void {
  const storage = safeSessionStorage();
  try {
    if (profileID === undefined) {
      storage?.removeItem(profileStorageKey(ASK_DISMISSED_SESSION_PREFIX, null));
    } else {
      storage?.removeItem(profileStorageKey(ASK_DISMISSED_SESSION_PREFIX, profileID));
    }
  } catch {
    // Ignore storage errors.
  }
}

export function withExperienceModeParam(path: string, mode: ExperienceModeId): string {
  if (!mode || mode === "all") return path;
  const hashIndex = path.indexOf("#");
  const hash = hashIndex >= 0 ? path.slice(hashIndex) : "";
  const base = hashIndex >= 0 ? path.slice(0, hashIndex) : path;
  const queryIndex = base.indexOf("?");
  const pathname = queryIndex >= 0 ? base.slice(0, queryIndex) : base;
  const params = new URLSearchParams(queryIndex >= 0 ? base.slice(queryIndex + 1) : "");
  params.set("mode", mode);
  const qs = params.toString();
  return `${pathname}${qs ? `?${qs}` : ""}${hash}`;
}

export function experienceModeLabel(
  entry: Pick<ExperienceMode, "labelKey" | "fallbackLabel">,
  t: (key: StringKey) => string,
): string {
  return entry.labelKey ? t(entry.labelKey) : entry.fallbackLabel;
}

export interface ExperienceLensContextValue {
  mode: ExperienceModeId;
  activeLens: ExperienceMode;
  modes: ExperienceMode[];
  showModePill: boolean;
  /** `undefined` in `"all"` mode so callers keep full unfiltered server queries; otherwise matching library IDs. */
  libraryIDFilter: string[] | undefined;
  launchPref: LaunchExperiencePref;
  launchOpen: boolean;
  plugins: ViewerPluginInfo[];
  setMode: (
    next: ExperienceModeId,
    options?: { syncUrl?: boolean; navigateHomeIfOutside?: boolean },
  ) => void;
  openLaunchLens: () => void;
  dismissLaunchLens: () => void;
  saveLaunchPref: (next: LaunchExperiencePref) => Promise<void>;
  /** Filter an explicit NavigationManifest Library set under the active lens. */
  matchesLibrary: (libraryID: string) => boolean;
}

const DEFAULT_LENS: ExperienceMode = {
  id: "all",
  kind: "all",
  labelKey: "lensModeAll",
  fallbackLabel: "All",
  libraryIDs: [],
  mediaClasses: [],
};

const DEFAULT_CONTEXT: ExperienceLensContextValue = {
  mode: "all",
  activeLens: DEFAULT_LENS,
  modes: [DEFAULT_LENS],
  showModePill: false,
  libraryIDFilter: undefined,
  launchPref: DEFAULT_LAUNCH_EXPERIENCE,
  launchOpen: false,
  plugins: [],
  setMode: () => {},
  openLaunchLens: () => {},
  dismissLaunchLens: () => {},
  saveLaunchPref: async () => {},
  matchesLibrary: () => true,
};

const ExperienceLensContext = createContext<ExperienceLensContextValue>(DEFAULT_CONTEXT);

export function useExperienceLens(): ExperienceLensContextValue {
  return useContext(ExperienceLensContext);
}

// Reactive store so components syncing preferences (e.g. ProfileSettingsPage) and
// the active Viewer shell reflect saves immediately without reloading.
const prefListeners = new Set<() => void>();
function emitPrefChange() {
  for (const listener of prefListeners) listener();
}
function subscribeStorage(listener: () => void): () => void {
  prefListeners.add(listener);
  const onStorage = (event: StorageEvent) => {
    if (
      event.key === null ||
      event.key.startsWith(LAUNCH_STORAGE_PREFIX) ||
      event.key.startsWith(LAST_MODE_STORAGE_PREFIX)
    ) {
      listener();
    }
  };
  globalThis.addEventListener?.("storage", onStorage);
  return () => {
    prefListeners.delete(listener);
    globalThis.removeEventListener?.("storage", onStorage);
  };
}

function isLaunchExemptPath(pathname: string): boolean {
  return LAUNCH_EXEMPT_PREFIXES.some((prefix) =>
    prefix.endsWith("/") ? pathname.startsWith(prefix) : pathname === prefix,
  );
}

export function ExperienceLensProvider({
  children,
  initialPlugins,
}: {
  children: ReactNode;
  initialPlugins?: ViewerPluginInfo[];
}) {
  const locale = useOptionalDisplayLocale();
  const { manifest, session } = useSession();
  const location = useLocation();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const profileID = session?.profileID ?? null;

  const [plugins, setPlugins] = useState<ViewerPluginInfo[]>(
    () => initialPlugins ?? readCachedPlugins(),
  );
  const [pluginsSettled, setPluginsSettled] = useState(Boolean(initialPlugins));
  const [prefsLoaded, setPrefsLoaded] = useState(false);

  const launchSnapshot = useSyncExternalStore(
    subscribeStorage,
    () => encodeLaunchExperiencePref(readStoredLaunchExperience(profileID)),
    () => encodeLaunchExperiencePref(DEFAULT_LAUNCH_EXPERIENCE),
  );
  const storedLastMode = useSyncExternalStore(
    subscribeStorage,
    () => readStoredLastMode(profileID),
    () => "all",
  );
  const launchPref = useMemo(() => parseLaunchExperiencePref(launchSnapshot), [launchSnapshot]);

  const urlModeParam = searchParams.get("mode");
  const hasExplicitUrlMode = urlModeParam !== null && urlModeParam.trim() !== "";

  const modes = useMemo(
    () =>
      deriveExperienceModes(
        manifest?.libraries ?? [],
        plugins,
        manifest?.layouts ?? [],
        manifest?.navigation ?? [],
        locale,
      ),
    [manifest, plugins, locale],
  );
  const showModePill = useMemo(
    () => modes.filter((entry) => entry.id !== "all").length >= 2,
    [modes],
  );

  // Track user's explicit selection during this mounted profile session separately
  // so URL param removal doesn't snap back unexpectedly while browsing.
  const [pickedMode, setPickedMode] = useState<ExperienceModeId | null>(null);
  const [launchOpen, setLaunchOpen] = useState(false);
  const prevProfileRef = useRef<string | null>(profileID);
  const pickedInLoadRef = useRef(false);

  useEffect(() => {
    if (prevProfileRef.current !== profileID) {
      prevProfileRef.current = profileID;
      setPickedMode(null);
      setLaunchOpen(false);
      setPrefsLoaded(false);
      pickedInLoadRef.current = false;
    }
  }, [profileID]);

  // Load Viewer plugin declarations (roleSession, works for every viewer role).
  useEffect(() => {
    if (initialPlugins || !profileID || !manifest) return;
    let alive = true;
    const controller = new AbortController();
    const epochSignal = getIdentityEpochSignal();
    const signal = epochSignal
      ? AbortSignal.any([controller.signal, epochSignal])
      : controller.signal;
    fetchViewerPlugins(signal)
      .then((list) => {
        if (!alive || signal.aborted) return;
        setPlugins(list);
        writeCachedPlugins(list);
        setPluginsSettled(true);
      })
      .catch(() => {
        if (!alive || signal.aborted) return;
        setPluginsSettled(true);
      });
    return () => {
      alive = false;
      controller.abort();
    };
  }, [profileID, initialPlugins]);

  // Sync per-profile Launch Experience + last mode from server clientPrefs.
  useEffect(() => {
    if (!profileID || !manifest) return;
    let alive = true;
    fetchClientPrefs()
      .then((prefs) => {
        if (!alive) return;
        if (!pickedInLoadRef.current) {
          const launchEntry = prefs.find((p) => p.key === PREF_KEYS.experienceLaunch);
          if (launchEntry?.value) {
            writeStoredLaunchExperience(parseLaunchExperiencePref(launchEntry.value), profileID);
          }
          const lastEntry = prefs.find((p) => p.key === PREF_KEYS.experienceLastMode);
          if (lastEntry?.value) {
            writeStoredLastMode(lastEntry.value.trim().toLowerCase() || "all", profileID);
          }
          emitPrefChange();
        }
        setPrefsLoaded(true);
      })
      .catch(() => {
        if (alive) setPrefsLoaded(true);
      });
    return () => {
      alive = false;
    };
  }, [profileID]);

  // Resolve active mode in priority order:
  // 1) Explicit ?mode=... URL query parameter
  // 2) Interactive pick in this tab
  // 3) Launch policy: fixed -> fixedMode, remember -> storedLastMode, ask -> storedLastMode / "all"
  const rawMode = hasExplicitUrlMode
    ? urlModeParam
    : pickedMode !== null
      ? pickedMode
      : launchPref.policy === "fixed"
        ? launchPref.fixedMode
        : storedLastMode;

  const mode = normalizeExperienceMode(
    rawMode,
    modes,
    hasExplicitUrlMode || !pluginsSettled || !manifest,
  );

  // Open the 1-tap Launch Lens chooser when policy === "ask" once per tab/profile
  // if multiple modalities exist and the user didn't deep-link via ?mode= or playback.
  useEffect(() => {
    if (!profileID || !manifest || !pluginsSettled || !prefsLoaded) return;
    if (!showModePill) return;
    if (launchPref.policy !== "ask") {
      setLaunchOpen(false);
      return;
    }
    if (hasExplicitUrlMode || isLaunchExemptPath(location.pathname)) return;
    if (hasAnsweredLaunchLensThisSession(profileID)) return;
    setLaunchOpen(true);
  }, [
    profileID,
    manifest,
    pluginsSettled,
    prefsLoaded,
    showModePill,
    launchPref.policy,
    hasExplicitUrlMode,
    location.pathname,
  ]);

  const activeLens = useMemo(
    () => modes.find((entry) => entry.id === mode) ?? modes[0] ?? DEFAULT_LENS,
    [modes, mode],
  );

  const libraryIDSet = useMemo(() => new Set(activeLens.libraryIDs), [activeLens.libraryIDs]);
  const libraryIDFilter = useMemo(
    () => (activeLens.id === "all" ? undefined : activeLens.libraryIDs),
    [activeLens.id, activeLens.libraryIDs],
  );

  const setMode = useCallback(
    (next: ExperienceModeId, options?: { syncUrl?: boolean; navigateHomeIfOutside?: boolean }) => {
      const normalized = normalizeExperienceMode(next, modes, !pluginsSettled || !manifest);
      const nextLens = modes.find((m) => m.id === normalized);
      pickedInLoadRef.current = true;
      setPickedMode(normalized);
      writeStoredLastMode(normalized, profileID);
      markLaunchLensAnsweredThisSession(profileID);
      setLaunchOpen(false);
      emitPrefChange();

      if (profileID) {
        const epoch = getIdentityEpochSignal();
        void setClientPref(PREF_KEYS.experienceLastMode, normalized).catch(() => {
          if (epoch?.aborted) return;
        });
      }

      const syncUrl = options?.syncUrl ?? true;
      const navigateHomeIfOutside = options?.navigateHomeIfOutside ?? true;

      // Check whether the user is currently inside a Library node excluded by the new lens.
      const nodeMatch = location.pathname.match(/^\/n\/([^/]+)/);
      if (navigateHomeIfOutside && nodeMatch && manifest && normalized !== "all" && nextLens) {
        const segment = decodeURIComponent(nodeMatch[1]);
        const node = findNodeBySlug(manifest.navigation, segment);
        if (node?.libraryID && !nextLens.libraryIDs.includes(node.libraryID)) {
          navigate(withExperienceModeParam("/", normalized));
          return;
        }
      }

      if (syncUrl) {
        const nextParams = new URLSearchParams(location.search);
        if (normalized === "all") {
          nextParams.delete("mode");
        } else {
          nextParams.set("mode", normalized);
        }
        setSearchParams(nextParams, { replace: true });
      }
    },
    [
      modes,
      pluginsSettled,
      manifest,
      profileID,
      location.pathname,
      location.search,
      navigate,
      setSearchParams,
    ],
  );

  const dismissLaunchLens = useCallback(() => {
    markLaunchLensAnsweredThisSession(profileID);
    setLaunchOpen(false);
  }, [profileID]);

  const openLaunchLens = useCallback(() => {
    setLaunchOpen(true);
  }, []);

  const saveLaunchPref = useCallback(
    async (next: LaunchExperiencePref) => {
      const normalized: LaunchExperiencePref = {
        policy: next.policy,
        fixedMode: next.fixedMode || "all",
      };
      pickedInLoadRef.current = true;
      writeStoredLaunchExperience(normalized, profileID);
      emitPrefChange();
      if (normalized.policy !== "ask") {
        setLaunchOpen(false);
      }
      if (profileID) {
        await setClientPref(PREF_KEYS.experienceLaunch, encodeLaunchExperiencePref(normalized));
      }
    },
    [profileID],
  );

  const matchesLibrary = useCallback(
    (libraryID: string) => activeLens.id === "all" || libraryIDSet.has(libraryID),
    [activeLens.id, libraryIDSet],
  );

  const value = useMemo<ExperienceLensContextValue>(
    () => ({
      mode: activeLens.id,
      activeLens,
      modes,
      showModePill,
      libraryIDFilter,
      launchPref,
      launchOpen,
      plugins,
      setMode,
      openLaunchLens,
      dismissLaunchLens,
      saveLaunchPref,
      matchesLibrary,
    }),
    [
      activeLens,
      modes,
      showModePill,
      libraryIDFilter,
      launchPref,
      launchOpen,
      plugins,
      setMode,
      openLaunchLens,
      dismissLaunchLens,
      saveLaunchPref,
      matchesLibrary,
    ],
  );

  return createElement(ExperienceLensContext.Provider, { value }, children);
}

/** Filter a manifest under the active Experience Lens (helper for Home & Navigation). */
export function filterManifestByLens(
  manifest: NavigationManifest | null,
  activeLens: ExperienceMode,
): NavigationManifest | null {
  if (!manifest || activeLens.id === "all") return manifest;
  const allowed = new Set(activeLens.libraryIDs);
  return {
    ...manifest,
    libraries: manifest.libraries.filter((library) => allowed.has(library.id)),
  };
}

export function useExperienceModeLabel() {
  const t = useT();
  return useCallback(
    (entry: Pick<ExperienceMode, "labelKey" | "fallbackLabel">) => experienceModeLabel(entry, t),
    [t],
  );
}
