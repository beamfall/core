import { useSyncExternalStore } from "react";
import { setContinueWatchingHidden } from "@/lib/graphql";
import type { SourceItem } from "@/lib/graphql/types";
import { notifyEngagementChanged } from "./engagementSync";
import { announceUndo } from "./hiddenItems";

// Remove from Continue Watching (CORE-2017, server side CORE-2039): a viewer
// takes one Media off their Profile's Continue Watching row. The server keeps
// the resume position, and playing the Media again brings it back. The card
// leaves the row at once: its id waits in `removed` until a fresh read of the
// row comes back without it, so a revalidation still in flight, or a row
// served from the viewer cache, never shows it again in between.

const NO_IDS: ReadonlySet<string> = new Set();
let removed: ReadonlySet<string> = NO_IDS;
const listeners = new Set<() => void>();

function publish(next: ReadonlySet<string>): void {
  removed = next;
  for (const listener of listeners) listener();
}

function withID(id: string): void {
  if (!removed.has(id)) publish(new Set([...removed, id]));
}

function withoutID(id: string): void {
  if (!removed.has(id)) return;
  const next = new Set(removed);
  next.delete(id);
  publish(next.size ? next : NO_IDS);
}

/**
 * Take a Media off Continue Watching and offer an Undo. The card is gone by
 * then, so a failed write puts it back and says so in the same notice.
 */
export async function removeFromContinueWatching(mediaID: string, title: string): Promise<void> {
  const target = { mediaID };
  withID(mediaID);
  try {
    await setContinueWatchingHidden(mediaID, true);
  } catch {
    withoutID(mediaID);
    announceUndo({ kind: "continue", target, title, failed: true });
    return;
  }
  notifyEngagementChanged();
  announceUndo({ kind: "continue", target, title });
}

/** Put a removed Media back on Continue Watching, in its old place. */
export async function restoreToContinueWatching(mediaID: string): Promise<void> {
  await setContinueWatchingHidden(mediaID, false);
  withoutID(mediaID);
  notifyEngagementChanged();
}

/** Forget pending removals: the Profile they belonged to is no longer bound. */
export function clearContinueWatchingRemoved(): void {
  if (removed.size) publish(NO_IDS);
}

/**
 * Record a fresh read of a Continue Watching row: a removed Media it no longer
 * lists is settled on the server, so a later play can bring it back.
 */
export function settleContinueWatching(items: readonly SourceItem[]): void {
  if (removed.size === 0) return;
  const listed = new Set(items.map((item) => item.id));
  const next = new Set([...removed].filter((id) => listed.has(id)));
  if (next.size !== removed.size) publish(next.size ? next : NO_IDS);
}

/** Media removed here whose removal a fresh read has not confirmed yet. */
export function useContinueWatchingRemoved(): ReadonlySet<string> {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    () => removed,
    () => removed,
  );
}
