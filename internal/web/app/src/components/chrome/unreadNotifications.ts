import { useEffect, useSyncExternalStore } from "react";
import { fetchNotifications } from "@/lib/graphql";

export const NOTIFICATION_BADGE_LIMIT = 100;
// How long a fetched count is trusted. Inside this window a remounting shell
// (every route change mounts a new one) and a window refocus reuse it; an
// explicit "notifications changed" signal always refetches.
export const UNREAD_NOTIFICATIONS_STALE_MS = 60_000;

// The unread count behind the header bell, held outside React so it survives
// AppShell remounts (CORE-1164). One entry, for one scope: the inbox is
// per-Profile and reveal-filtered, so a different scope never reads it.
interface Entry {
  scope: string;
  count: number;
  fetchedAt: number;
  inFlight: Promise<void> | null;
}

let entry: Entry | null = null;
const listeners = new Set<() => void>();

function emit() {
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function revalidate(scope: string, force: boolean): void {
  if (entry?.scope !== scope) {
    entry = { scope, count: 0, fetchedAt: 0, inFlight: null };
    emit();
  }
  const current = entry;
  if (current.inFlight) return;
  if (
    !force &&
    current.fetchedAt > 0 &&
    Date.now() - current.fetchedAt < UNREAD_NOTIFICATIONS_STALE_MS
  ) {
    return;
  }
  current.inFlight = fetchNotifications({ unreadOnly: true, limit: NOTIFICATION_BADGE_LIMIT })
    .then(
      (rows) => {
        current.count = rows.length;
        current.fetchedAt = Date.now();
      },
      () => {
        // Keep the last known count: a failed refresh is not "nothing unread".
        // fetchedAt stays put, so the next mount or refocus tries again.
      },
    )
    .finally(() => {
      current.inFlight = null;
      if (entry === current) emit();
    });
}

/**
 * The unread notification count for `scope` (null when there is no inbox to
 * read: signed out, enrolling, or outside the Viewer). Fetches once per scope
 * and shares the result across remounts; revalidates when stale on mount and
 * on window refocus, and at once on `beamfall:notifications-changed`.
 */
export function useUnreadNotifications(scope: string | null): number {
  const count = useSyncExternalStore(subscribe, () =>
    scope !== null && entry?.scope === scope ? entry.count : 0,
  );

  useEffect(() => {
    if (scope === null) return;
    revalidate(scope, false);
    function onForeground() {
      if (document.visibilityState === "hidden") return;
      revalidate(scope as string, false);
    }
    function onChanged() {
      revalidate(scope as string, true);
    }
    window.addEventListener("focus", onForeground);
    document.addEventListener("visibilitychange", onForeground);
    window.addEventListener("beamfall:notifications-changed", onChanged);
    return () => {
      window.removeEventListener("focus", onForeground);
      document.removeEventListener("visibilitychange", onForeground);
      window.removeEventListener("beamfall:notifications-changed", onChanged);
    };
  }, [scope]);

  return count;
}

export function resetUnreadNotificationsForTest() {
  entry = null;
}
