// Auth / enrollment operations: the Admin Session GraphQL verbs and the
// non-GraphQL REST enrollment surfaces (device enroll, browser→MANAGER
// promotion, logout, manager-gated pairing-code minting). The REST calls ride
// the same CSRF + same-origin-cookie boundary as the GraphQL client
// (internal/web/static/app.js, internal/web/server.go routes).
//
// GraphQL operation strings are copied VERBATIM from app.js (contract-tested
// server-side). The REST helpers build on the foundation client's postJSON,
// which throws ApiError (carrying .status) on a non-2xx response — callers
// inspect that status the way the vanilla app inspects response.status.

import { graphql, postJSON } from "@/lib/api";
import { rebindCompatibility } from "@/lib/compat";
import type { AdminSessionInfo } from "./types";

/**
 * Outcome of the compatibility rebind that follows a successful credential
 * step. The credential (pairing code, admin password) is CONSUMED server-side
 * the moment the server accepts it, so a handshake failure afterwards must
 * never read as a credential failure: callers commit the success (redirect /
 * unlock) and surface "rebind-failed" as its own recoverable notice. The next
 * full load re-runs the handshake through the session spine regardless.
 */
export type RebindOutcome = "bound" | "rebind-failed";

async function settleRebind(): Promise<RebindOutcome> {
  try {
    await rebindCompatibility();
    return "bound";
  } catch {
    return "rebind-failed";
  }
}

// --- Admin Session (GraphQL) ----------------------------------------------

const ADMIN_SESSION_QUERY = "query AdminSession { adminSession { active expiresAt } }";

/** AdminSession — whether an Admin Session is currently active. */
export async function fetchAdminSession(signal?: AbortSignal): Promise<AdminSessionInfo> {
  const data = await graphql<{ adminSession: AdminSessionInfo }>(ADMIN_SESSION_QUERY, {}, signal);
  return data.adminSession;
}

const VERIFY_ADMIN_SESSION_MUTATION =
  "mutation VerifyAdminSession($password: String!) { verifyAdminSession(password: $password) { active expiresAt } }";

/** An accepted admin credential plus the outcome of the handshake after it. */
export interface VerifiedAdminSession extends AdminSessionInfo {
  /** Absent only in fixtures that predate the split; callers read it as bound. */
  rebind?: RebindOutcome;
}

/**
 * VerifyAdminSession — open an Admin Session with the admin password. Rejects
 * only when the credential step itself fails; `rebind` reports the handshake
 * that follows, which the caller surfaces separately (the session is live).
 */
export async function verifyAdminSession(
  password: string,
  signal?: AbortSignal,
): Promise<VerifiedAdminSession> {
  const data = await graphql<{ verifyAdminSession: AdminSessionInfo }>(
    VERIFY_ADMIN_SESSION_MUTATION,
    { password },
    signal,
  );
  const info = data.verifyAdminSession;
  const rebind = info.active ? await settleRebind() : "bound";
  return { ...info, rebind };
}

const END_ADMIN_SESSION_MUTATION = "mutation EndAdminSession { endAdminSession { active } }";

/** EndAdminSession — close the active Admin Session. */
export async function endAdminSession(signal?: AbortSignal): Promise<AdminSessionInfo> {
  const data = await graphql<{ endAdminSession: AdminSessionInfo }>(
    END_ADMIN_SESSION_MUTATION,
    {},
    signal,
  );
  await rebindCompatibility();
  return data.endAdminSession;
}

// --- Enrollment + pairing (REST: internal/web/server.go) ------------------

/**
 * Enroll this browser as a device with a pairing code (POST /api/web/enroll).
 * Resolves with the rebind outcome once the server accepts the code (the code
 * is consumed and the cookies are set at that point); rejects with an ApiError
 * (`.status` 401 = invalid code) only when the enrollment itself fails — the
 * same status the vanilla app branches on.
 */
export async function enroll(code: string, deviceName: string): Promise<RebindOutcome> {
  await postJSON("/api/web/enroll", { code, deviceName });
  return settleRebind();
}

/**
 * Browser→MANAGER promotion: redeem a manager pairing code for this
 * already-enrolled device (POST /api/web/promote). Cookies are untouched — the
 * role changes server-side. Rejects with an ApiError (`.status` 401 = invalid
 * code, 403 = not a manager code) on failure.
 */
export async function promote(code: string, signal?: AbortSignal): Promise<void> {
  await postJSON("/api/web/promote", { code }, signal);
  await rebindCompatibility();
}

/** Sign out: drop the session cookie (POST /api/web/logout). */
export async function logout(): Promise<void> {
  await postJSON("/api/web/logout", {});
}

/** The minted pairing-code payload (POST /api/pairing-codes response). */
export interface PairingCode {
  code: string;
  expiresAt: string;
}

/**
 * Mint a pairing code (POST /api/pairing-codes). The same REST endpoint the
 * Manager apps use; minting is manager-gated server-side. `deviceRole` is the
 * role the code enrolls ("viewer" or "manager").
 */
export async function mintPairingCode(deviceRole: string): Promise<PairingCode> {
  return postJSON<PairingCode>("/api/pairing-codes", { deviceRole });
}
