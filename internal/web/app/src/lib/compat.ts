import { ApiError, installAPIRequestGuard, postJSON } from "./api";

export const CLIENT_SURFACE = "core-web-ui" as const;
export const CORE_CONTRACT_REV_MIN = 1;
export const CORE_CONTRACT_REV_MAX = 1;
export const RENDERING_REV_MIN = 1;
export const RENDERING_REV_MAX = 1;
export const RENDERER_STAMPS = ["core-web-ui-v1"] as const;

export type CompatDecision =
  | "bootstrap"
  | "compatible"
  | "degraded"
  | "client-too-old"
  | "server-too-old"
  | "server-installation-inconsistent";

export type CompatMessageClass =
  | "compat-bootstrap"
  | "compat-compatible"
  | "compat-degraded"
  | "compat-client-too-old"
  | "compat-server-too-old"
  | "compat-server-installation-inconsistent";

export interface ClientContract {
  surface: typeof CLIENT_SURFACE;
  appVersion: string;
  buildID: string;
  coreContractRevRange: readonly [number, number];
  renderingContractRevRange: readonly [number, number];
  renderingStamps: readonly string[];
  fixedVocabularies: readonly string[];
  requestedCapabilities: readonly string[];
  developerAPIMajor: number;
}

export interface CompatHandshakeResponse {
  decision: CompatDecision;
  decisionID?: string;
  reasonCode?: string;
  disabledCapabilities?: string[];
  messageClass: CompatMessageClass;
}

export const CLIENT_CONTRACT: ClientContract = {
  surface: CLIENT_SURFACE,
  appVersion: "embedded",
  buildID: "embedded-core-web-ui-v1",
  coreContractRevRange: [CORE_CONTRACT_REV_MIN, CORE_CONTRACT_REV_MAX],
  renderingContractRevRange: [RENDERING_REV_MIN, RENDERING_REV_MAX],
  renderingStamps: RENDERER_STAMPS,
  fixedVocabularies: [],
  requestedCapabilities: [],
  developerAPIMajor: 0,
};

const terminalDecisions = new Set<CompatDecision>([
  "client-too-old",
  "server-too-old",
  "server-installation-inconsistent",
]);

let terminalResult: CompatHandshakeResponse | undefined;
const terminalListeners = new Set<(result: CompatHandshakeResponse) => void>();
let identityEpoch = 0;
let latchedIdentityEpoch: number | undefined;
let latchedRequest: Promise<CompatHandshakeResponse> | undefined;

installAPIRequestGuard((path) => {
  if (!terminalResult || path === "/api/web/logout") return;
  throw new Error(terminalResult.messageClass);
});

/**
 * Negotiate the embedded UI contract once per live identity epoch.
 * Bootstrap and transport failures remain retryable; terminal incompatibility
 * remains blocked even if later session activity advances the epoch.
 */
export function performHandshake(): Promise<CompatHandshakeResponse> {
  if (terminalResult) return Promise.resolve(terminalResult);
  if (latchedIdentityEpoch === identityEpoch && latchedRequest) return latchedRequest;

  const requestEpoch = identityEpoch;
  const request = postJSON<CompatHandshakeResponse>("/api/compat/handshake", CLIENT_CONTRACT)
    .then((result) => {
      if (requestEpoch !== identityEpoch) {
        return latchedIdentityEpoch === identityEpoch && latchedRequest ? latchedRequest : result;
      }
      if (terminalDecisions.has(result.decision) && !terminalResult) {
        terminalResult = result;
        for (const listener of terminalListeners) listener(result);
      }
      if (result.decision === "bootstrap" && latchedRequest === request) {
        latchedIdentityEpoch = undefined;
        latchedRequest = undefined;
      }
      return result;
    })
    .catch((error: unknown) => {
      if (latchedRequest === request) {
        latchedIdentityEpoch = undefined;
        latchedRequest = undefined;
      }
      throw error;
    });

  latchedIdentityEpoch = identityEpoch;
  latchedRequest = request;
  return request;
}

/** End the current identity epoch without starting work for its replacement. */
export function advanceCompatibilityEpoch(): void {
  identityEpoch++;
  latchedIdentityEpoch = undefined;
  latchedRequest = undefined;
}

/** Re-negotiate after a server-side auth fingerprint change. */
export async function rebindCompatibility(): Promise<CompatHandshakeResponse> {
  advanceCompatibilityEpoch();
  const result = await performHandshake();
  if (isBlocked()) throw new Error(result.messageClass);
  return result;
}

/** Whether an API refusal came from the compatibility enforcement boundary. */
export function isCompatibilityRefusal(error: unknown): error is ApiError {
  if (!(error instanceof ApiError) || error.status !== 403) return false;
  if (typeof error.payload !== "object" || error.payload === null) return false;
  const messageClass = Reflect.get(error.payload, "messageClass");
  return typeof messageClass === "string" && messageClass.startsWith("compat-");
}

/** Observe a terminal decision so cached privileged client state can be cleared immediately. */
export function subscribeCompatibilityBlock(
  listener: (result: CompatHandshakeResponse) => void,
): () => void {
  terminalListeners.add(listener);
  if (terminalResult) listener(terminalResult);
  return () => terminalListeners.delete(listener);
}

/** Whether a terminal compatibility decision has blocked normal API use. */
export function isBlocked(): boolean {
  return terminalResult !== undefined;
}
