// The Admin-Session epoch (ADR-0007 §4 / ADR-0085), the Console-scoped sibling
// of the identity epoch in lib/api.ts.
//
// Console rows are scoped to ONE Admin Session. When that session ends — the
// window lapses, the operator locks, or the bound Profile switches — every
// Console request issued under it is prior-epoch work: its response may no
// longer reflect authority the browser still holds, and writing it back would
// leave operator data on screen after the gate has relocked. AdminGate owns the
// epoch (it is the component that observes all three transitions) and publishes
// its signal here; the common Console fetch path (areas/console/useAsync) and
// the ad-hoc Console callsites compose it into every request they issue.
//
// A module-level signal rather than context: Console fetchers are plain async
// functions called from event handlers, timers, and poll loops that have no
// hook scope, and lib/api.ts already establishes this exact seam for identity.

let adminEpochSignal: AbortSignal | undefined;

/** Publish the current Admin-Session epoch signal (AdminGate only). */
export function setAdminEpochSignal(signal?: AbortSignal): void {
  adminEpochSignal = signal;
}

/**
 * The current Admin-Session epoch signal, for Console callsites that issue a
 * request outside {@link useAsync} (a manual refresh, a status poll).
 */
export function getAdminEpochSignal(): AbortSignal | undefined {
  return adminEpochSignal;
}

/**
 * Compose a request's own signal with the Admin-Session epoch, so ending the
 * Admin Session aborts the request even though the caller never sees the epoch.
 */
export function withAdminEpoch(signal: AbortSignal): AbortSignal {
  if (!adminEpochSignal || adminEpochSignal === signal) return signal;
  return AbortSignal.any([adminEpochSignal, signal]);
}
