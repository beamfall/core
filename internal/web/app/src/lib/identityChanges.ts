// The session cookie is shared by tabs. Send only an invalidation signal;
// readers must verify the live session with the server.
export function openIdentityChanges(): BroadcastChannel | undefined {
  return typeof BroadcastChannel === "undefined"
    ? undefined
    : new BroadcastChannel("beamfall-identity-changes");
}

export function notifyIdentityChange() {
  const channel = openIdentityChanges();
  channel?.postMessage(null);
  channel?.close();
}
