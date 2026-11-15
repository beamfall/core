import { ApiError, fetchSetupStatus, postJSON, type SignInSecurityMode } from "@/lib/api";
import {
  createLibrary,
  fetchConsoleConfig,
  fetchLibraries,
  fetchSetupState,
  scanLibrary,
  setConsoleConfig,
  setLibraryRoots,
  setPluginActive,
  verifyAdminSession,
} from "@/lib/graphql";
import type { PluginSummary, SetupState } from "@/lib/graphql/types";
import { groupProviderKeys, type ProviderKey } from "@/lib/providerKeys";
import type { StringKey } from "@/strings";

// First-run setup orchestration (WEB-1, ADR-0007 §2). The wizard's visual step
// order differs from the server's API shape, so the orchestration lives here,
// decoupled from rendering, and is exercised directly by the request-recording
// tests.
//
// The actual data flow:
//   1. POST /api/setup (pairing.SetupRequest) atomically creates the first
//      MANAGER device+session, the owner Profile, and sets the admin password,
//      all gated by the one-time setup token. A wrong/consumed token → 401.
//   2. The browser now holds a MANAGER session, so the remaining steps use
//      authenticated GraphQL: createLibrary + setLibraryRoots, then
//      setPluginActive for each chosen plugin.
//
// The setup token and admin password are CREDENTIALS — they ride the request
// body and are never logged or echoed.

export type UpdateCheckChannel = "stable" | "beta";

/**
 * The fewest characters a new admin password may have. Mirrors
 * store.MinAdminPasswordLength, which the server enforces on every path that
 * sets a password (CORE-1601); the browser checks first so the rule shows
 * before a round trip.
 */
export const MIN_ADMIN_PASSWORD_LENGTH = 8;

/** What `POST /api/setup` returns on success (graph bootstrap result). */
export interface SetupBootstrapResult {
  deviceID: string;
  sessionExpiresAt: string;
  profileID: string;
  deviceRole: string;
}

export interface FirstLibraryResult {
  libraryID: string;
  scanJobID: string;
  scanStatus: string;
}

export interface FirstLibraryProgress {
  libraryID?: string;
  rootsSet: boolean;
  rootPath?: string;
}

export class FirstLibraryError extends Error {
  readonly progress: FirstLibraryProgress;
  /** True when the Library and its folder are saved and only the first scan failed. */
  readonly scanFailed: boolean;

  constructor(cause: unknown, progress: FirstLibraryProgress, scanFailed = false) {
    super(cause instanceof Error ? cause.message : "Something went wrong. Please try again.", {
      cause,
    });
    this.name = "FirstLibraryError";
    this.progress = progress;
    this.scanFailed = scanFailed;
  }
}

/** A typed marker for a 401 from /api/setup — the token was invalid/consumed. */
export class SetupTokenError extends Error {
  constructor() {
    super("setup token invalid or already used");
    this.name = "SetupTokenError";
  }
}

/**
 * Bootstrap the server: POST /api/setup with the token, device name, owner
 * Profile name, and admin password. A 401 surfaces as a SetupTokenError so the
 * caller can show the invalid-token message and NOT advance; any other failure
 * propagates. On success the browser holds the MANAGER session cookies.
 */
export async function bootstrapSetup(input: {
  token: string;
  deviceName: string;
  profileName: string;
  adminPassword: string;
  securityMode: SignInSecurityMode;
}): Promise<SetupBootstrapResult> {
  try {
    return await postJSON<SetupBootstrapResult>("/api/setup", {
      token: input.token,
      deviceName: input.deviceName,
      profileName: input.profileName,
      adminPassword: input.adminPassword,
      securityMode: input.securityMode,
    });
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      throw new SetupTokenError();
    }
    throw err;
  }
}

/**
 * Create the first Library from the chosen media type (a plugin and, for a
 * plugin with family scopes, one scope; see lib/libraryTypes) with its single
 * root, in one call, then start its first scan. The server checks the folder
 * before it creates anything (CORE-1598), so a bad path leaves no Library
 * behind. When a Library already exists (an earlier attempt whose scan failed,
 * or a rootless one found on resume), a new or corrected path updates its root
 * instead of creating a second Library.
 */
export async function createFirstLibrary(input: {
  title: string;
  pluginID: string;
  familyScope: string | null;
  rootPath: string;
  progress?: FirstLibraryProgress;
}): Promise<FirstLibraryResult> {
  let progress: FirstLibraryProgress = { ...(input.progress ?? { rootsSet: false }) };
  let scanning = false;
  try {
    let libraryID = progress.libraryID;
    if (!libraryID) {
      libraryID = (
        await createLibrary(input.title, input.pluginID, input.familyScope ?? undefined, [
          input.rootPath,
        ])
      ).id;
      progress = { libraryID, rootsSet: true, rootPath: input.rootPath };
    } else if (!progress.rootsSet || progress.rootPath !== input.rootPath) {
      await setLibraryRoots(libraryID, [input.rootPath]);
      progress = { libraryID, rootsSet: true, rootPath: input.rootPath };
    }
    scanning = true;
    const scan = await scanLibrary(libraryID, input.rootPath);
    return { libraryID, scanJobID: scan.id, scanStatus: scan.status };
  } catch (err) {
    throw new FirstLibraryError(err, { ...progress }, scanning);
  }
}

/**
 * Save the plugin choices (setPluginActive, ADR-0007 step 5): each chosen
 * plugin on, and each one the owner switched off saved as off. Installed
 * plugins are on by default, so an unsaved "off" would come back after a
 * reload (CORE-1604).
 */
export async function activatePlugins(
  pluginIDs: string[],
  inactivePluginIDs: string[] = [],
): Promise<void> {
  for (const pluginID of pluginIDs) {
    await setPluginActive(pluginID, true);
  }
  for (const pluginID of inactivePluginIDs) {
    await setPluginActive(pluginID, false);
  }
}

/** The plugin choices and Console settings saved on the server, read on resume. */
export interface SavedPluginChoices {
  plugins: PluginSummary[];
  active: Set<string>;
  updateCheckEnabled: boolean;
  updateCheckChannel: UpdateCheckChannel;
  providerKeys: ProviderKey[];
}

/**
 * Read the saved plugin choices so a resumed wizard shows the server's state,
 * not a guess (CORE-1604). Console config needs the Admin Session the first
 * run opened; once that has lapsed this throws and the caller falls back to
 * the installed plugin list.
 */
export async function loadSavedPluginChoices(): Promise<SavedPluginChoices> {
  const config = await fetchConsoleConfig();
  return {
    plugins: config.plugins.map((p) => ({ id: p.id, displayName: p.displayName })),
    active: new Set(config.plugins.filter((p) => p.active).map((p) => p.id)),
    updateCheckEnabled: config.updateCheckEnabled,
    updateCheckChannel: config.updateCheckChannel === "beta" ? "beta" : "stable",
    providerKeys: groupProviderKeys(config.plugins.flatMap((p) => p.scrapers ?? [])),
  };
}

/**
 * Reopen the Admin Session a resumed wizard needs to read the saved choices
 * once the first run's window has lapsed (CORE-1731). Resolves true when the
 * server accepted the password. The password is not kept.
 */
export async function reopenAdminSession(adminPassword: string): Promise<boolean> {
  const info = await verifyAdminSession(adminPassword);
  return info.active;
}

/**
 * List the provider keys the installed plugins' scrapers declare (CORE-1115).
 * Console config is Admin-Session-gated, so first-run setup verifies that gate
 * with the password the operator just created, as `configureUpdateCheck` does.
 */
export async function loadProviderKeys(adminPassword: string): Promise<ProviderKey[]> {
  await verifyAdminSession(adminPassword);
  const config = await fetchConsoleConfig();
  return groupProviderKeys(config.plugins.flatMap((p) => p.scrapers ?? []));
}

/**
 * Persist the setup wizard's update-awareness choice. `setConsoleConfig` is an
 * Admin-Session-gated Console operation, so first-run setup verifies that gate
 * with the password the operator just created. A resumed run has no password
 * (it is never kept across a reload) and relies on the window the first run
 * opened.
 */
export async function configureUpdateCheck(input: {
  adminPassword: string;
  enabled: boolean;
  channel: UpdateCheckChannel;
}): Promise<void> {
  if (input.adminPassword) await verifyAdminSession(input.adminPassword);
  await setConsoleConfig({
    updateCheckEnabled: input.enabled,
    updateCheckChannel: input.channel,
  });
}

/**
 * The result of probing whether setup is mid-flow and resumable (ADR-0080:
 * "resumable but not replayable"). The one-time setup token is in-memory and
 * single-use, so once `POST /api/setup` has been consumed the browser cannot
 * re-bootstrap — but a reload or server restart may leave provisioning (the
 * Library / plugin steps) incomplete. This lets the wizard skip the
 * already-done token/admin/owner steps and resume at the first incomplete one.
 */
export interface SetupResume {
  /** True once the one-time token has been consumed (a session exists). */
  alreadyBootstrapped: boolean;
  /** False when this browser may finish setup without the token (LAN, CORE-1105). */
  tokenRequired?: boolean;
  /** Server-derived per-step completion, or null when not yet bootstrapped. */
  state: SetupState | null;
  /** Partially-created first Library state that must survive a reload. */
  firstLibraryProgress?: FirstLibraryProgress;
  /** Addresses on the server's network that need no setup token (CORE-1600). */
  lanUrls?: string[];
}

/**
 * Probe the server to decide whether the wizard is a fresh run or a resume.
 *
 *   - `GET /api/setup` `required:true`  → fresh server, not yet bootstrapped.
 *   - `required:false` → the token is consumed (setup ran). We then read
 *     `setupState` to learn which provisioning steps still remain.
 *
 * `setupState` requires a session; if the browser's session is stale/expired
 * the call throws. Setup is unrecoverable without a valid session, so we treat
 * that as `alreadyBootstrapped: true, state: null` and let the wizard bounce to
 * the app root (the session spine re-routes an unenrolled browser to /enroll)
 * rather than surfacing an unhandled error (Gate-A LOW).
 */
export async function bootstrapResume(): Promise<SetupResume> {
  const { required, tokenRequired, lanUrls } = await fetchSetupStatus();
  if (required) {
    return { alreadyBootstrapped: false, state: null, tokenRequired, ...(lanUrls && { lanUrls }) };
  }
  try {
    const state = await fetchSetupState();
    if (state.hasLibrary) {
      const libraries = await fetchLibraries();
      const configuredLibrary = libraries.find((library) => (library.roots?.length ?? 0) > 0);
      const rootlessLibrary = libraries.find((library) => (library.roots?.length ?? 0) === 0);
      state.hasLibrary = configuredLibrary != null;
      state.complete =
        state.adminPasswordSet &&
        state.hasLibrary &&
        state.hasOwnerProfile &&
        state.pluginsActivated;
      if (!configuredLibrary && rootlessLibrary) {
        return {
          alreadyBootstrapped: true,
          state,
          firstLibraryProgress: { libraryID: rootlessLibrary.id, rootsSet: false },
        };
      }
    }
    return { alreadyBootstrapped: true, state };
  } catch {
    return { alreadyBootstrapped: true, state: null };
  }
}

/**
 * Map a provisioning failure to a localized, actionable message key WITHOUT
 * leaking server internals (ADR-0080: "local, actionable errors without leaking
 * secrets or filesystem internals").
 *
 * The server's GraphQL errors carry only free-text messages today (no
 * structured `extensions.code`), so 6A deliberately does NOT substring-match
 * those messages — that would be brittle across server versions and locales and
 * risk a misleading wrong message. Every error maps to the generic
 * `setupFailed` key. Distinguishing bad-path vs permission-denied requires the
 * server to emit a structured error code first (tracked as an optimization
 * follow-up); until then the recovery affordance — let
 * the operator edit the path and retry — is what makes the failure actionable,
 * not message precision.
 */
export function classifySetupError(_err: unknown): StringKey {
  return "setupFailed";
}
