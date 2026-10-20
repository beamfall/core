// The single client boundary to the Beamfall server API surface
// (server.go routes). Faithful to the vanilla app's contract:
//   - identity rides an HttpOnly same-origin cookie (never readable by JS)
//   - CSRF defense is a constant custom header the browser cannot set
//     cross-origin without CORS (internal/web/static/app.js CSRF_HEADER)
//   - all content arrives through these surfaces; the bundle is static bytes

const CSRF_HEADER = "X-Beamfall-Csrf";
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
  deadlineMs?: number;
}

type JSONRequestArgument = AbortSignal | JSONRequestOptions;

interface JSONRequestConfig {
  signal?: AbortSignal;
  maxResponseBytes: number;
  deadlineMs: number;
}

function jsonRequestConfig(argument?: JSONRequestArgument): JSONRequestConfig {
  const options = argument && "aborted" in argument ? { signal: argument } : argument;
  const maxResponseBytes = options?.maxResponseBytes ?? DEFAULT_RESPONSE_BYTES;
  const deadlineMs = options?.deadlineMs ?? DEFAULT_DEADLINE_MS;
  validateRequestBound("maxResponseBytes", maxResponseBytes, MAX_RESPONSE_BYTES);
  validateRequestBound("deadlineMs", deadlineMs, MAX_DEADLINE_MS);
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
  const deadlineError = new ApiDeadlineError(path, config.deadlineMs);
  const deadlineController = new AbortController();
  let deadlineTimer: ReturnType<typeof setTimeout> | undefined;
  const deadline = new Promise<never>((_resolve, reject) => {
    deadlineTimer = setTimeout(() => {
      deadlineController.abort(deadlineError);
      reject(deadlineError);
    }, config.deadlineMs);
  });
  const callerSignal = requestSignal(config.signal);
  const transportSignal = callerSignal
    ? AbortSignal.any([callerSignal, deadlineController.signal])
    : deadlineController.signal;
  const requestInit = { ...init, signal: transportSignal };
  const transport = Promise.resolve(fetch(path, requestInit)).then(async (response) => {
    const bytes = await readResponseBytes(response, path, config.maxResponseBytes);
    if (!response.ok) {
      throw new ApiError(`${method} ${path} failed`, response.status, parseErrorPayload(bytes));
    }
    return parseJSONBody<T>(bytes, path, response.status);
  });
  try {
    return await Promise.race([transport, deadline]);
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

/** GET JSON with same-origin credentials. Honors an optional AbortSignal. */
export async function getJSON<T = unknown>(
  path: string,
  options?: JSONRequestArgument,
): Promise<T> {
  return requestJSON<T>("GET", path, { credentials: "same-origin" }, options);
}

export interface GraphQLResult<T> {
  data?: T;
  errors?: { message: string }[];
}

/** Issue a GraphQL operation against /api/graphql. Honors an optional
 * AbortSignal, threaded through to the underlying transport request. */
export async function graphql<T = unknown>(
  query: string,
  variables: Record<string, unknown> = {},
  signal?: AbortSignal,
): Promise<T> {
  const result = await postJSON<GraphQLResult<T>>(
    "/api/graphql",
    {
      query,
      variables,
    },
    signal,
  );
  if (result.errors?.length) {
    const messages = result.errors.map((e) => e.message);
    throw new ApiError(messages.join("; "), graphqlErrorStatus(messages));
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

/** Fetch the Navigation Manifest (the Viewer's nav + layout document). */
export function getManifest<T = unknown>(signal?: AbortSignal): Promise<T> {
  return getJSON<T>("/api/manifest", signal);
}

/** The live state of an SSE subscription, surfaced to a connection indicator. */
export type EventStreamState = "live" | "reconnecting";

/**
 * Subscribe to the Reveal-filtered SSE event stream (/api/events, ADR-0040).
 * Returns an unsubscribe function. The wire format, route, and event payload
 * are unchanged; `onState` is an additive, optional observer of the browser's
 * own EventSource lifecycle: "live" on open, "reconnecting" on a transport
 * error (EventSource auto-retries with backoff until the returned unsubscribe
 * closes it). When both `onError` and `onState` are supplied both run on error
 * — they are composed, never overwritten.
 */
export function subscribeEvents(
  onEvent: (event: MessageEvent) => void,
  onError?: (err: Event) => void,
  onState?: (state: EventStreamState) => void,
): () => void {
  const source = new EventSource("/api/events", { withCredentials: true });
  source.onmessage = onEvent;
  if (onState) source.onopen = () => onState("live");
  if (onError || onState) {
    source.onerror = (err) => {
      onError?.(err);
      onState?.("reconnecting");
    };
  }
  return () => source.close();
}
