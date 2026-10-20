// Recovery policy for fatal hls.js errors (ADR-0010, ADR-0084). A fatal
// NETWORK_ERROR is retried in place with capped exponential backoff; a fatal
// MEDIA_ERROR gets one plain recoverMediaError() and then one
// swapAudioCodec()+recoverMediaError(); anything past the budget (or any other
// fatal type) destroys the instance. The budget resets once a fragment buffers
// again, so "consecutive" means without intervening progress.

// String values of Hls.ErrorTypes, inlined so this module stays free of the
// hls.js runtime (hlsRecovery.test.ts pins them to the real enum).
export const HLS_NETWORK_ERROR = "networkError";
export const HLS_MEDIA_ERROR = "mediaError";

export const NETWORK_RETRY_DELAYS_MS = [1000, 2000, 4000] as const;
export const MEDIA_RECOVERY_ATTEMPTS = 2;

export interface RecoveryBudget {
  network: number;
  media: number;
}

export type RecoveryStep =
  | { kind: "network"; delayMs: number }
  | { kind: "media"; swapAudioCodec: boolean }
  | { kind: "destroy" };

export function freshRecoveryBudget(): RecoveryBudget {
  return { network: 0, media: 0 };
}

export function resetRecoveryBudget(budget: RecoveryBudget): void {
  budget.network = 0;
  budget.media = 0;
}

// Consumes one attempt from the budget and names the step to take for it.
export function nextRecoveryStep(budget: RecoveryBudget, errorType: unknown): RecoveryStep {
  if (errorType === HLS_NETWORK_ERROR) return nextNetworkStep(budget);
  if (errorType === HLS_MEDIA_ERROR) return nextMediaStep(budget);
  return { kind: "destroy" };
}

function nextNetworkStep(budget: RecoveryBudget): RecoveryStep {
  const delayMs = NETWORK_RETRY_DELAYS_MS[budget.network];
  if (delayMs === undefined) return { kind: "destroy" };
  budget.network += 1;
  return { kind: "network", delayMs };
}

function nextMediaStep(budget: RecoveryBudget): RecoveryStep {
  if (budget.media >= MEDIA_RECOVERY_ATTEMPTS) return { kind: "destroy" };
  const swapAudioCodec = budget.media === 1;
  budget.media += 1;
  return { kind: "media", swapAudioCodec };
}
