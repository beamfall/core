import type { StringKey } from "@/strings";

// Why the Console is locked (ADR-0085 §30). Every reason here is CLIENT-VERIFIABLE
// from state the browser already holds — the Profile kind, whether an Admin
// Session is active, the unlock-time expiry, and whether the bound Profile
// changed. The server's verify result stays deliberately generic (no oracle on
// password-set / eligibility / lockout); we never infer a server-side reason.
export type LockReason =
  | "not-adult" // a managed/non-adult Profile cannot administer the server
  | "no-session" // no Admin Session yet — re-authenticate with the admin password
  | "expired" // the Admin Session window lapsed (known only within the unlock page lifetime)
  | "profile-switched"; // the Profile changed since the Admin Session opened

export interface LockInput {
  /** The current session's Profile kind ("adult" | "managed" | …). */
  profileKind: string | undefined;
  /** Whether an Admin Session is currently active. */
  adminActive: boolean;
  /** The Profile id bound when the Admin Session opened (empty if none). */
  boundProfileID: string;
  /** The current session's Profile id. */
  currentProfileID: string | undefined;
  /** Unlock-time expiry ISO, when known (mutation result only). */
  expiresAt: string | undefined;
  /** Injectable clock for testability; defaults to Date.now(). */
  now?: number;
}

/**
 * lockReason returns the reason the Console is locked, or null when it is
 * legitimately unlocked. Order matters: a non-adult Profile can never hold
 * admin authority, so that wins; then a switched Profile or a lapsed window
 * invalidates an otherwise-active session; finally an absent session is the
 * plain "re-authenticate" state.
 */
export function lockReason(input: LockInput): LockReason | null {
  if (input.profileKind !== undefined && input.profileKind !== "adult") {
    return "not-adult";
  }
  if (!input.adminActive) return "no-session";
  // Active session: verify it still belongs to this Profile and hasn't lapsed.
  if (
    input.boundProfileID &&
    input.currentProfileID &&
    input.boundProfileID !== input.currentProfileID
  ) {
    return "profile-switched";
  }
  if (input.expiresAt) {
    const expiry = Date.parse(input.expiresAt);
    if (!Number.isNaN(expiry) && expiry <= (input.now ?? Date.now())) {
      return "expired";
    }
  }
  return null;
}

/** The reason headline string for each lock reason. */
export const LOCK_REASON_TITLE: Record<LockReason, StringKey> = {
  "not-adult": "consoleLockedNotAdultTitle",
  "no-session": "consoleLockedNoSessionTitle",
  expired: "consoleLockedExpiredTitle",
  "profile-switched": "consoleLockedSwitchedTitle",
};

/** The reason explanation string for each lock reason. */
export const LOCK_REASON_LEAD: Record<LockReason, StringKey> = {
  "not-adult": "consoleLockedNotAdultLead",
  "no-session": "consoleLockedNoSessionLead",
  expired: "consoleLockedExpiredLead",
  "profile-switched": "consoleLockedSwitchedLead",
};
