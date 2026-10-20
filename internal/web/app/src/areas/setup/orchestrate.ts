import { ApiError, fetchSetupRequired, postJSON } from "@/lib/api";
import {
  createLibrary,
  fetchLibraries,
  fetchSetupState,
  scanLibrary,
  setConsoleConfig,
  setLibraryRoots,
  setPluginActive,
  verifyAdminSession,
} from "@/lib/graphql";
import type { SetupState } from "@/lib/graphql/types";
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

/** The five Library-type tiles, each mapped to a plugin + optional familyScope.
 * Scope value strings are verbatim from filmtv/bundle.go FamilyScopes; Music
 * and Podcast plugins have no familyScope. */
export type LibraryType = "films" | "tvshows" | "mixed" | "music" | "podcasts";

export type UpdateCheckChannel = "stable" | "beta";

export interface LibraryTypeSpec {
  /** The Library-type tile id (also the filmtv familyScope where applicable). */
  type: LibraryType;
  /** The installed plugin this tile binds the Library to. */
  pluginID: string;
  /** The Film & TV familyScope, or null for plugins without scopes. */
  familyScope: string | null;
}

export const LIBRARY_TYPES: readonly LibraryTypeSpec[] = [
  { type: "films", pluginID: "filmtv", familyScope: "films" },
  { type: "tvshows", pluginID: "filmtv", familyScope: "tvshows" },
  { type: "mixed", pluginID: "filmtv", familyScope: "mixed" },
  { type: "music", pluginID: "music", familyScope: null },
  { type: "podcasts", pluginID: "podcasts", familyScope: null },
] as const;

export function libraryTypeSpec(type: LibraryType): LibraryTypeSpec {
  const spec = LIBRARY_TYPES.find((s) => s.type === type);
  if (!spec) throw new Error(`unknown library type: ${type}`);
  return spec;
}

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

  constructor(cause: unknown, progress: FirstLibraryProgress) {
    super(cause instanceof Error ? cause.message : "Something went wrong. Please try again.");
    this.name = "FirstLibraryError";
    this.progress = progress;
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
}): Promise<SetupBootstrapResult> {
  try {
    return await postJSON<SetupBootstrapResult>("/api/setup", {
      token: input.token,
      deviceName: input.deviceName,
      profileName: input.profileName,
      adminPassword: input.adminPassword,
    });
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      throw new SetupTokenError();
    }
    throw err;
  }
}

/**
 * Create the first Library from the chosen tile, then set its single root.
 * Returns the created Library id.
 */
export async function createFirstLibrary(input: {
  title: string;
  type: LibraryType;
  rootPath: string;
  progress?: FirstLibraryProgress;
}): Promise<FirstLibraryResult> {
  const spec = libraryTypeSpec(input.type);
  let progress: FirstLibraryProgress = { ...(input.progress ?? { rootsSet: false }) };
  try {
    const libraryID =
      progress.libraryID ??
      (await createLibrary(input.title, spec.pluginID, spec.familyScope ?? undefined)).id;
    progress = { ...progress, libraryID };
    if (!progress.rootsSet) {
      await setLibraryRoots(libraryID, [input.rootPath]);
      progress = { libraryID, rootsSet: true, rootPath: input.rootPath };
    }
    const scan = await scanLibrary(libraryID, progress.rootPath ?? input.rootPath);
    return { libraryID, scanJobID: scan.id, scanStatus: scan.status };
  } catch (err) {
    throw new FirstLibraryError(err, { ...progress });
  }
}

/** Activate each chosen plugin in turn (setPluginActive, ADR-0007 step 5). */
export async function activatePlugins(pluginIDs: string[]): Promise<void> {
  for (const pluginID of pluginIDs) {
    await setPluginActive(pluginID, true);
  }
}

/**
 * Persist the setup wizard's update-awareness choice. `setConsoleConfig` is an
 * Admin-Session-gated Console operation, so first-run setup verifies that gate
 * with the password the operator just created.
 */
export async function configureUpdateCheck(input: {
  adminPassword: string;
  enabled: boolean;
  channel: UpdateCheckChannel;
}): Promise<void> {
  await verifyAdminSession(input.adminPassword);
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
  /** Server-derived per-step completion, or null when not yet bootstrapped. */
  state: SetupState | null;
  /** Partially-created first Library state that must survive a reload. */
  firstLibraryProgress?: FirstLibraryProgress;
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
  const required = await fetchSetupRequired();
  if (required) return { alreadyBootstrapped: false, state: null };
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
 * server to emit a structured error code first (tracked in
 * docs/agent-memory/optimizations.md); until then the recovery affordance — let
 * the operator edit the path and retry — is what makes the failure actionable,
 * not message precision.
 */
export function classifySetupError(_err: unknown): StringKey {
  return "setupFailed";
}
