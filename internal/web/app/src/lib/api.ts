// The single client boundary to the Beamfall server API surface
// (server.go routes). Faithful to the vanilla app's contract:
//   - identity rides an HttpOnly same-origin cookie (never readable by JS)
//   - CSRF defense is a constant custom header the browser cannot set
//     cross-origin without CORS (internal/web/static/app.js CSRF_HEADER)
//   - all content arrives through these surfaces; the bundle is static bytes

export const CSRF_HEADER = "X-Beamfall-Csrf";
const DEFAULT_RESPONSE_BYTES = 8 * 1024 * 1024;
const MAX_RESPONSE_BYTES = 32 * 1024 * 1024;
const DEFAULT_DEADLINE_MS = 30_000;
const MAX_DEADLINE_MS = 120_000;
let identityEpochSignal: AbortSignal | undefined;
let identityEpochReset: (() => void) | undefined;

export function setIdentityEpochSignal(signal?: AbortSignal): void {
  identityEpochSignal = signal;
}

export function getIdentityEpochSignal(): AbortSignal | undefined {
  return identityEpochSignal;
}

/**
 * Register the session spine's epoch reset (lib/session.tsx). Callers that
 * observe a server response which ENDS the current identity — a Reveal-terminal
 * playback response (ADR-0004/0010), say — cannot fix that locally: the revealed
 * session, manifest, and Profile set are all scoped to an identity the server
 * has already retired. This is the one seam through which any such observer
 * asks the spine to start a new epoch, so the wiring is not per-consumer.
 */
export function setIdentityEpochReset(reset?: () => void): void {
  identityEpochReset = reset;
}

/**
 * Begin a new identity epoch: aborts every in-flight fetch of the current epoch
 * and clears all identity-scoped state before the next identity is read. A
 * no-op when no session spine is mounted (unit harnesses, the enroll shell).
 */
export function resetIdentityEpoch(): void {
  identityEpochReset?.();
}

function requestSignal(signal?: AbortSignal): AbortSignal | undefined {
  if (!signal) return identityEpochSignal;
  if (!identityEpochSignal || signal === identityEpochSignal) return signal;
  return AbortSignal.any([identityEpochSignal, signal]);
}

// The Profile this tab shows (CORE-2923). Every tab shares one session
// cookie, so after another tab switches Profile the server would otherwise
// apply this tab's requests to the new Profile. Each request names the Profile
// it was made for, and the server refuses one that no longer matches.
const PROFILE_HEADER = "X-Beamfall-Profile";
let expectedProfileID: string | undefined;
let profileMismatchHandler: (() => void) | undefined;

/**
 * Name the Profile this tab shows on every request from now on; no argument
 * while an identity is loading or none is signed in, so the request names none.
 */
export function setExpectedProfile(profileID?: string): void {
  expectedProfileID = profileID || undefined;
}

/**
 * Register what a refusal for a Profile this tab still shows does (the session
 * spine reloads its identity). Call with no argument to unregister.
 */
export function setProfileMismatchHandler(handler?: () => void): void {
  profileMismatchHandler = handler;
}

/** The Profile this tab's requests name, if any. */
export function getExpectedProfile(): string | undefined {
  return expectedProfileID;
}

/** The Profile header for a request made outside the helpers below. */
export function profileHeaders(): Record<string, string> {
  return expectedProfileID ? { [PROFILE_HEADER]: expectedProfileID } : {};
}

/**
 * Whether a refusal says the session is bound to another Profile than the
 * request named. A REST refusal is a 409; a GraphQL mutation that found the
 * switch only at its write answers 200 with the reason on its error.
 */
function isProfileMismatch(status: number, payload: unknown): boolean {
  if (typeof payload !== "object" || payload === null) return false;
  const body = payload as { reason?: unknown; errors?: { extensions?: { reason?: unknown } }[] };
  if (status === 409 && body.reason === "PROFILE_MISMATCH") return true;
  if (!Array.isArray(body.errors)) return false;
  return body.errors.some((error) => error?.extensions?.reason === "PROFILE_MISMATCH");
}

/**
 * React to a refused request made for `sentProfile`. Only a refusal for the
 * Profile this tab still shows reloads it: one that arrives after the tab
 * moved on to another identity belongs to the old one.
 */
export function noteProfileRefusal(
  sentProfile: string | undefined,
  status: number,
  payload: unknown,
): void {
  if (sentProfile === undefined || sentProfile !== expectedProfileID) return;
  if (!isProfileMismatch(status, payload)) return;
  profileMismatchHandler?.();
}

let requestGuard: ((path: string) => void) | undefined;

/** Install the app-wide guard that can stop normal API use after terminal compatibility. */
export function installAPIRequestGuard(guard: (path: string) => void): void {
  requestGuard = guard;
}

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly payload?: unknown,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

/** A 2xx response whose body is not the JSON document the caller contracted for
 * (empty, truncated, or an HTML error page from an intermediary). Kept as an
 * ApiError so every `instanceof ApiError` branch handles it instead of a bare
 * SyntaxError escaping from JSON.parse. */
export class ApiMalformedResponseError extends ApiError {
  constructor(
    readonly path: string,
    status: number,
  ) {
    super(`${path} returned a non-JSON body`, status);
    this.name = "ApiMalformedResponseError";
  }
}

/** Whether an API failure is an authorization rejection (HTTP 401/403, or a
 * GraphQL authorization error mapped onto the same statuses by `graphql`). */
export function isAuthRejection(error: unknown): error is ApiError {
  if (!(error instanceof ApiError)) return false;
  return error.status === 401 || error.status === 403;
}

// GraphQL resolvers refuse in-band (HTTP 200 + `errors`) with these exact
// messages (internal/server graph_*.go); the client maps each onto the HTTP
// status the REST surfaces use for the same refusal so one auth path serves
// both transports.
const GRAPHQL_AUTH_STATUS: Readonly<Record<string, number>> = {
  "admin session required": 403,
  "manager role required": 403,
  "invalid session": 401,
  "session expired": 401,
  unauthorized: 401,
};

const ADMIN_SESSION_REQUIRED = "admin session required";

/**
 * Asked when a GraphQL operation is refused for want of an Admin Session.
 * Resolves true once the surface has opened one, false if it gave up.
 */
export type AdminUnlockHandler = () => Promise<boolean>;

let adminUnlockHandler: AdminUnlockHandler | undefined;

/**
 * Register the one surface that offers an in-place admin unlock (the Manage
 * area). A refused operation waits on the handler that was registered when it
 * was SENT — so a late refusal belongs to the page that asked, not to whatever
 * is showing by then — and is sent once more after a successful unlock. The
 * caller sees only the final outcome. Call with no argument to unregister.
 */
export function setAdminUnlockHandler(handler?: AdminUnlockHandler): void {
  adminUnlockHandler = handler;
}

function graphqlErrorStatus(messages: readonly string[]): number {
  const matched = messages.map((message) => GRAPHQL_AUTH_STATUS[message.trim().toLowerCase()]);
  return matched.find((status) => status !== undefined) ?? 200;
}

export class ApiDeadlineError extends Error {
  constructor(
    readonly path: string,
    readonly deadlineMs: number,
  ) {
    super(`${path} exceeded its ${deadlineMs}ms deadline`);
    this.name = "ApiDeadlineError";
  }
}

export class ApiResponseTooLargeError extends Error {
  constructor(
    readonly path: string,
    readonly maxResponseBytes: number,
    readonly receivedBytes: number,
  ) {
    super(`${path} response exceeded ${maxResponseBytes} bytes`);
    this.name = "ApiResponseTooLargeError";
  }
}

export class ApiConfigurationError extends Error {
  constructor(
    readonly field: "maxResponseBytes" | "deadlineMs",
    readonly value: number,
    readonly hardMaximum: number,
  ) {
    super(`${field} must be a positive integer no greater than ${hardMaximum}; received ${value}`);
    this.name = "ApiConfigurationError";
  }
}

export interface JSONRequestOptions {
  signal?: AbortSignal;
  maxResponseBytes?: number;
  /** Null lets a long-running operation use only the caller and identity signals. */
  deadlineMs?: number | null;
}

type JSONRequestArgument = AbortSignal | JSONRequestOptions;

interface JSONRequestConfig {
  signal?: AbortSignal;
  maxResponseBytes: number;
  deadlineMs: number | null;
}

function jsonRequestConfig(argument?: JSONRequestArgument): JSONRequestConfig {
  const options = argument && "aborted" in argument ? { signal: argument } : argument;
  const maxResponseBytes = options?.maxResponseBytes ?? DEFAULT_RESPONSE_BYTES;
  const { deadlineMs = DEFAULT_DEADLINE_MS } = options ?? {};
  validateRequestBound("maxResponseBytes", maxResponseBytes, MAX_RESPONSE_BYTES);
  if (deadlineMs !== null) validateRequestBound("deadlineMs", deadlineMs, MAX_DEADLINE_MS);
  return { signal: options?.signal, maxResponseBytes, deadlineMs };
}

function validateRequestBound(
  field: "maxResponseBytes" | "deadlineMs",
  value: number,
  hardMaximum: number,
): void {
  if (!Number.isSafeInteger(value) || value <= 0 || value > hardMaximum) {
    throw new ApiConfigurationError(field, value, hardMaximum);
  }
}

async function requestJSON<T>(
  method: "GET" | "POST",
  path: string,
  init: RequestInit,
  argument?: JSONRequestArgument,
): Promise<T> {
  requestGuard?.(path);
  const config = jsonRequestConfig(argument);
  const { deadlineMs } = config;
  let transportSignal = requestSignal(config.signal);
  let deadlineTimer: ReturnType<typeof setTimeout> | undefined;
  let deadline: Promise<never> | undefined;
  if (deadlineMs !== null) {
    const deadlineError = new ApiDeadlineError(path, deadlineMs);
    const controller = new AbortController();
    transportSignal = transportSignal
      ? AbortSignal.any([transportSignal, controller.signal])
      : controller.signal;
    deadline = new Promise<never>((_resolve, reject) => {
      deadlineTimer = setTimeout(() => {
        controller.abort(deadlineError);
        reject(deadlineError);
      }, deadlineMs);
    });
  }
  const sentProfile = expectedProfileID;
  const requestInit: RequestInit = { ...init, signal: transportSignal };
  if (sentProfile !== undefined) {
    requestInit.headers = {
      ...(init.headers as Record<string, string> | undefined),
      ...profileHeaders(),
    };
  }
  const transport = Promise.resolve(fetch(path, requestInit)).then(async (response) => {
    const bytes = await readResponseBytes(response, path, config.maxResponseBytes);
    if (!response.ok) {
      const payload = parseErrorPayload(bytes);
      noteProfileRefusal(sentProfile, response.status, payload);
      throw new ApiError(`${method} ${path} failed`, response.status, payload);
    }
    const body = parseJSONBody<T>(bytes, path, response.status);
    noteProfileRefusal(sentProfile, response.status, body);
    return body;
  });
  try {
    return await (deadline ? Promise.race([transport, deadline]) : transport);
  } finally {
    clearTimeout(deadlineTimer);
  }
}

async function readResponseBytes(
  response: Response,
  path: string,
  maxResponseBytes: number,
): Promise<Uint8Array> {
  if (!response.headers) {
    const payload = await response.json();
    return new TextEncoder().encode(JSON.stringify(payload) ?? "");
  }
  const contentLength = response.headers.get("Content-Length");
  if (contentLength && /^\d+$/.test(contentLength)) {
    const declaredBytes = Number(contentLength);
    if (declaredBytes > maxResponseBytes) {
      await response.body?.cancel().catch(() => {});
      throw new ApiResponseTooLargeError(path, maxResponseBytes, declaredBytes);
    }
  }
  if (!response.body) return new Uint8Array();

  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let receivedBytes = 0;
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    receivedBytes += value.byteLength;
    if (receivedBytes > maxResponseBytes) {
      const error = new ApiResponseTooLargeError(path, maxResponseBytes, receivedBytes);
      await reader.cancel(error).catch(() => {});
      throw error;
    }
    chunks.push(value);
  }

  const bytes = new Uint8Array(receivedBytes);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return bytes;
}

function parseJSONBody<T>(bytes: Uint8Array, path: string, status: number): T {
  try {
    return JSON.parse(new TextDecoder().decode(bytes)) as T;
  } catch {
    throw new ApiMalformedResponseError(path, status);
  }
}

function parseErrorPayload(bytes: Uint8Array): unknown {
  try {
    return JSON.parse(new TextDecoder().decode(bytes));
  } catch {
    return undefined;
  }
}

/** POST JSON with the CSRF header and same-origin credentials. Honors an
 * optional AbortSignal so a caller (e.g. an unmounted Console screen) can
 * cancel the underlying transport request, not just ignore its result. */
export async function postJSON<T = unknown>(
  path: string,
  body: unknown,
  options?: JSONRequestArgument,
): Promise<T> {
  return requestJSON<T>(
    "POST",
    path,
    {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", [CSRF_HEADER]: "1" },
      body: JSON.stringify(body),
    },
    options,
  );
}

/** Upload one artwork image for a Media slot (multipart, CSRF header,
 * same-origin credentials). The server applies it as a manual pick and locks
 * the slot (internal/server/artwork_upload.go, CORE-2904). */
export async function uploadMediaArtwork(
  mediaID: string,
  slot: string,
  file: File,
  options?: JSONRequestArgument,
): Promise<void> {
  const form = new FormData();
  form.append("mediaID", mediaID);
  form.append("slot", slot);
  form.append("file", file);
  await requestJSON<unknown>(
    "POST",
    "/api/media/artwork/upload",
    { method: "POST", credentials: "same-origin", headers: { [CSRF_HEADER]: "1" }, body: form },
    options,
  );
}

/**
 * POST JSON with keepalive, so the request outlives the page that sent it: a
 * pagehide handler uses it to tell the server the viewer left. Fire and
 * forget, except that a Profile refusal still reloads a tab that is open.
 * `profile` names the Profile the request is made for: an unmount that runs
 * after this tab moved on passes the Profile it opened with, so it can never
 * act on the Profile the session moved to (CORE-2923); null names none.
 */
export function postKeepalive(
  path: string,
  body: unknown,
  profile: string | null | undefined = expectedProfileID,
): void {
  try {
    void fetch(path, {
      method: "POST",
      credentials: "same-origin",
      keepalive: true,
      headers: {
        "Content-Type": "application/json",
        [CSRF_HEADER]: "1",
        ...(profile ? { [PROFILE_HEADER]: profile } : {}),
      },
      body: JSON.stringify(body),
    })
      .then(async (response) => {
        noteProfileRefusal(profile ?? undefined, response.status, await response.json());
      })
      .catch(() => {});
  } catch {
    // A page being torn down may refuse new requests; the server's expiry covers it.
  }
}

/** GET JSON with same-origin credentials. Honors an optional AbortSignal. */
export async function getJSON<T = unknown>(
  path: string,
  options?: JSONRequestArgument,
): Promise<T> {
  return requestJSON<T>("GET", path, { credentials: "same-origin" }, options);
}

export interface GraphQLResult<T> {
  data?: T;
  errors?: GraphQLErrorEntry[];
}

/** One entry of a GraphQL response's errors array. `extensions.code` is the
 * server's closed error vocabulary (NOT_FOUND, FORBIDDEN, THROTTLED, ...). */
export interface GraphQLErrorEntry {
  message: string;
  extensions?: { code?: string; reason?: string };
}

/** The extensions.code of the first GraphQL error behind `err`, when `err` is
 * an ApiError raised by graphql(). Callers branch on the code, never on the
 * message. */
export function graphqlErrorCode(err: unknown): string | undefined {
  if (!(err instanceof ApiError)) return undefined;
  const payload = err.payload as { errors?: GraphQLErrorEntry[] } | undefined;
  if (!payload || !Array.isArray(payload.errors)) return undefined;
  return payload.errors.find((e) => typeof e.extensions?.code === "string")?.extensions?.code;
}

/** Issue a GraphQL operation against /api/graphql. Honors an optional
 * AbortSignal, threaded through to the underlying transport request. */
function needsAdminSession(result: GraphQLResult<unknown>): boolean {
  return (
    result.errors?.some((e) => e.message.trim().toLowerCase() === ADMIN_SESSION_REQUIRED) ?? false
  );
}

export interface GraphQLOptions {
  /**
   * False for a background read the page did not ask for (a header poll, say):
   * a refusal for want of an Admin Session then fails the read quietly instead
   * of putting the page's in-place unlock in front of the user.
   */
  adminUnlock?: boolean;
}

export async function graphql<T = unknown>(
  query: string,
  variables: Record<string, unknown> = {},
  signal?: AbortSignal,
  options: GraphQLOptions = {},
): Promise<T> {
  const send = () => postJSON<GraphQLResult<T>>("/api/graphql", { query, variables }, signal);
  const unlock = options.adminUnlock === false ? undefined : adminUnlockHandler;
  let result = await send();
  // The refused operation did not run, so sending it again after the unlock
  // cannot repeat it.
  if (unlock && needsAdminSession(result) && (await unlock())) result = await send();
  if (result.errors?.length) {
    const messages = result.errors.map((e) => e.message);
    throw new ApiError(messages.join("; "), graphqlErrorStatus(messages), {
      errors: result.errors,
    });
  }
  if (result.data === undefined) {
    throw new ApiError("GraphQL response had no data", 200);
  }
  return result.data;
}

/**
 * Probe the unauthenticated first-run setup state (ADR-0007 §2).
 * `GET /api/setup` returns `{"required": true|false}` for an unenrolled
 * browser; `true` means a fresh server with setup still pending. Any error
 * (including a non-200) resolves to `false` so a normal enrolled server never
 * gets stuck routed at the wizard.
 */
export async function fetchSetupRequired(): Promise<boolean> {
  try {
    const data = await getJSON<{ required?: boolean }>("/api/setup");
    return data.required === true;
  } catch {
    return false;
  }
}

/** The owner's sign-in security choice (CORE-1110): Home keeps admin-password sign-in, Strict turns it off. */
export type SignInSecurityMode = "home" | "strict";

/**
 * GET /api/setup in full: whether setup is pending, whether this browser
 * would need the one-time setup token (false on the local network while setup
 * is pending, CORE-1105) and the sign-in security mode (CORE-1110). Errors
 * resolve to `{required:false, tokenRequired:true, securityMode:null}`: a failed
 * read leaves the mode unknown, never Home.
 */
export async function fetchSetupStatus(): Promise<{
  required: boolean;
  tokenRequired: boolean;
  securityMode: SignInSecurityMode | null;
  /** Server addresses on this browser's network that need no setup token (CORE-1600). */
  lanUrls?: string[];
}> {
  try {
    const data = await getJSON<{
      required?: boolean;
      tokenRequired?: boolean;
      securityMode?: string;
      lanUrls?: unknown;
    }>("/api/setup");
    const lanUrls = httpURLs(data.lanUrls);
    return {
      required: data.required === true,
      tokenRequired: data.tokenRequired !== false,
      securityMode: data.securityMode === "strict" ? "strict" : "home",
      ...(lanUrls.length > 0 && { lanUrls }),
    };
  } catch {
    return { required: false, tokenRequired: true, securityMode: null };
  }
}

// Keeps only plain http(s) URLs, so a server-offered address is safe as a link.
function httpURLs(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => {
    if (typeof item !== "string") return [];
    try {
      const url = new URL(item);
      return url.protocol === "http:" || url.protocol === "https:" ? [url.href] : [];
    } catch {
      return [];
    }
  });
}

/** Fetch the Navigation Manifest (the Viewer's nav + layout document). */
export function getManifest<T = unknown>(signal?: AbortSignal): Promise<T> {
  return getJSON<T>("/api/manifest", signal);
}

import { type EventStreamState, openEventStream } from "./eventStream";

export type { EventStreamState };

/**
 * Subscribe to the Reveal-filtered SSE event stream (/api/events, ADR-0040).
 * Returns an unsubscribe function. Every event is delivered, whatever its kind:
 * core frames each one with an `event: <kind>` line, which the browser's own
 * EventSource never hands to `onmessage`, so the stream is read directly. The
 * kind is the delivered event's `type`. `onState` is an optional observer of
 * the connection: "live" once the stream opens, "reconnecting" after a
 * transport error (the stream retries with backoff, resuming from the last
 * event id, until the returned unsubscribe closes it). `onError` and `onState`
 * both run on an error.
 */
export function subscribeEvents(
  onEvent: (event: MessageEvent) => void,
  onError?: (err: Event) => void,
  onState?: (state: EventStreamState) => void,
): () => void {
  return openEventStream("/api/events", {
    onEvent,
    onError,
    onState,
    // Each connection names the Profile this tab shows, and the server ends a
    // stream whose session moved to another one; the refused reconnect reloads
    // the tab (CORE-2923).
    headers: profileHeaders,
    onRefused: (sent, status, payload) => noteProfileRefusal(sent[PROFILE_HEADER], status, payload),
  });
}
