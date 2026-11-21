// Guest HTTP client for the party session (CORE-1662 design "Guest HTTP", CORE-2948).
//
// A guest has no device session: it joins with the party code and a name and gets a scoped,
// expiring capability token, which every later call presents in the X-Beamfall-Party-Guest
// header. Plain fetch, no cookies. The token is a CREDENTIAL: it is kept in sessionStorage keyed
// by the join code (so a reload stays joined, a closed tab forgets it) and never logged.

export const PARTY_GUEST_HEADER = "X-Beamfall-Party-Guest";

export type PartyEntryState =
  | "pending"
  | "waiting"
  | "current"
  | "done"
  | "removed"
  | "declined"
  | "skipped";

/** The guest view of a PartyEntry: the server sends only these fields to guests. */
export interface PartyGuestEntry {
  id: string;
  title: string;
  subtitle: string;
  participantName: string;
  state: PartyEntryState;
  /** 1-based among waiting entries, 0 otherwise. */
  position: number;
  etaSeconds: number;
}

export interface PartyGuestState {
  sessionID: string;
  joinCode: string;
  autoAdd: boolean;
  current: PartyGuestEntry | null;
  /** Waiting entries in rotation order. */
  entries: PartyGuestEntry[];
  /** This guest's pending, waiting and declined entries. */
  mine: PartyGuestEntry[];
  maxWaiting: number;
  expiresAt: string;
}

export interface PartyGuestJoin {
  token: string;
  sessionID: string;
  participantID: string;
  name: string;
  expiresAt: string;
}

export interface PartySearchItem {
  mediaID: string;
  title: string;
  subtitle: string;
  durationSeconds: number;
  artworkURL: string;
  /** When this Media was last current earlier in the session, else "". */
  lastPlayedAt: string;
}

export type PartyGuestErrorKind =
  | "unknown-code" // 404: any unusable code; the server does not say why
  | "full" // 409 on join
  | "limit" // 409 on request: at the waiting limit
  | "rate-limited" // 429
  | "ended" // 401: token refused or expired
  | "failed"; // anything else, including offline

export class PartyGuestError extends Error {
  constructor(
    readonly kind: PartyGuestErrorKind,
    readonly status: number,
  ) {
    super(`party guest request failed: ${kind} (${status})`);
    this.name = "PartyGuestError";
  }
}

function errorKind(status: number, conflict: "full" | "limit"): PartyGuestErrorKind {
  switch (status) {
    case 404:
      return "unknown-code";
    case 409:
      return conflict;
    case 429:
      return "rate-limited";
    case 401:
      return "ended";
    default:
      return "failed";
  }
}

async function call<T>(
  method: "GET" | "POST",
  path: string,
  token: string | null,
  body?: unknown,
  conflict: "full" | "limit" = "limit",
  signal?: AbortSignal,
): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (token) headers[PARTY_GUEST_HEADER] = token;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  let response: Response;
  try {
    response = await fetch(path, {
      method,
      headers,
      credentials: "omit",
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    });
  } catch (err) {
    if (signal?.aborted) throw err;
    throw new PartyGuestError("failed", 0);
  }
  if (!response.ok)
    throw new PartyGuestError(errorKind(response.status, conflict), response.status);
  try {
    return (await response.json()) as T;
  } catch {
    throw new PartyGuestError("failed", response.status);
  }
}

export function joinParty(code: string, name: string): Promise<PartyGuestJoin> {
  return call("POST", "/api/party/join", null, { code, name }, "full");
}

export async function fetchGuestState(
  token: string,
  signal?: AbortSignal,
): Promise<PartyGuestState> {
  const state = await call<PartyGuestState>(
    "GET",
    "/api/party/guest",
    token,
    undefined,
    "limit",
    signal,
  );
  return {
    ...state,
    current: state.current ?? null,
    entries: state.entries ?? [],
    mine: state.mine ?? [],
  };
}

export async function searchPartyMedia(
  token: string,
  q: string,
  signal?: AbortSignal,
): Promise<PartySearchItem[]> {
  const { items } = await call<{ items?: PartySearchItem[] }>(
    "GET",
    `/api/party/guest/search?q=${encodeURIComponent(q)}`,
    token,
    undefined,
    "limit",
    signal,
  );
  return items ?? [];
}

export async function requestPartyMedia(token: string, mediaID: string): Promise<PartyGuestEntry> {
  const { entry } = await call<{ entry: PartyGuestEntry }>(
    "POST",
    "/api/party/guest/request",
    token,
    { mediaID },
  );
  return entry;
}

export async function cancelPartyRequest(token: string, entryID: string): Promise<void> {
  await call("POST", "/api/party/guest/cancel", token, { entryID });
}

// Session storage of the guest token, keyed by the normalized join code. Storage can be missing
// or throw (private windows, blocked site data); the page then simply asks to join again.

export interface StoredGuest {
  token: string;
  name: string;
}

const storageKey = (code: string) => `beamfall.partyGuest.${normalizePartyCode(code)}`;

export function normalizePartyCode(code: string): string {
  return code.replace(/\s+/g, "").toUpperCase();
}

export function loadGuest(code: string): StoredGuest | null {
  try {
    const raw = window.sessionStorage.getItem(storageKey(code));
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<StoredGuest>;
    return typeof parsed.token === "string" && parsed.token !== ""
      ? { token: parsed.token, name: typeof parsed.name === "string" ? parsed.name : "" }
      : null;
  } catch {
    return null;
  }
}

export function saveGuest(code: string, guest: StoredGuest): void {
  try {
    window.sessionStorage.setItem(storageKey(code), JSON.stringify(guest));
  } catch {
    // Not persisted: a reload asks to join again.
  }
}

export function forgetGuest(code: string): void {
  try {
    window.sessionStorage.removeItem(storageKey(code));
  } catch {
    // Nothing stored to forget.
  }
}
