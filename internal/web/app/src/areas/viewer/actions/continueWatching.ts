import { useSyncExternalStore } from "react";
import { getIdentityEpochSignal } from "@/lib/api";
import { setContinueWatchingHidden } from "@/lib/graphql";
import { notifyEngagementChanged } from "./engagementSync";
import { announceUndo } from "./hiddenItems";

// Remove from Continue Watching (CORE-2017, server side CORE-2039): a viewer
// takes one Media off their Profile's Continue Watching row. The server keeps
// the resume position, and playing the Media again brings it back. The card
// leaves the row at once. Each removal records the write sequence its write
// settled at, and a row keeps the card out until it holds a read that started
// after that write: a revalidation still in flight, or a row served from the
// viewer cache, never shows it again, and a later read that lists it (it was
// played again) does.

/** Media removed here, by the write sequence their removal settled at. */
export type ContinueWatchingRemovals = ReadonlyMap<string, number>;

const PENDING = Number.POSITIVE_INFINITY;
const NONE: ContinueWatchingRemovals = new Map();
let removed: ContinueWatchingRemovals = NONE;
let writeSeq = 0;
const listeners = new Set<() => void>();

function publish(next: ContinueWatchingRemovals): void {
  removed = next.size ? next : NONE;
  for (const listener of listeners) listener();
}

function setRemoval(id: string, seq: number): void {
  publish(new Map([...removed, [id, seq]]));
}

function dropRemoval(id: string): void {
  if (!removed.has(id)) return;
  const next = new Map(removed);
  next.delete(id);
  publish(next);
}

/** The write sequence now: capture it as a Continue Watching read starts. */
export function continueWatchingReadSeq(): number {
  return writeSeq;
}

/**
 * Whether a row read that started at `readSeq` must still leave the Media out:
 * its removal is in flight, or settled after the read began.
 */
export function continueWatchingKeepsOut(
  removals: ContinueWatchingRemovals,
  id: string,
  readSeq: number,
): boolean {
  const seq = removals.get(id);
  return seq !== undefined && readSeq < seq;
}

/**
 * Take a Media off Continue Watching and offer an Undo. The card is gone by
 * then, so a failed write puts it back and says so in the same notice. A write
 * that settles after the Profile changed says nothing: the notice, and its
 * Undo, belong to the Profile that made the change.
 */
export async function removeFromContinueWatching(mediaID: string, title: string): Promise<void> {
  const target = { mediaID };
  const epoch = getIdentityEpochSignal();
  setRemoval(mediaID, PENDING);
  try {
    await setContinueWatchingHidden(mediaID, true);
  } catch {
    if (epoch !== getIdentityEpochSignal()) return;
    dropRemoval(mediaID);
    announceUndo({ kind: "continue", target, title, failed: true, epoch });
    return;
  }
  if (epoch !== getIdentityEpochSignal()) return;
  writeSeq += 1;
  if (removed.has(mediaID)) setRemoval(mediaID, writeSeq);
  notifyEngagementChanged();
  announceUndo({ kind: "continue", target, title, epoch });
}

/**
 * Put a removed Media back on Continue Watching, in its old place. An Undo
 * from another identity epoch writes nothing.
 */
export async function restoreToContinueWatching(
  mediaID: string,
  epoch = getIdentityEpochSignal(),
): Promise<void> {
  if (epoch !== getIdentityEpochSignal()) return;
  await setContinueWatchingHidden(mediaID, false);
  if (epoch !== getIdentityEpochSignal()) return;
  dropRemoval(mediaID);
  notifyEngagementChanged();
}

/** Forget pending removals: the Profile they belonged to is no longer bound. */
export function clearContinueWatchingRemoved(): void {
  if (removed.size) publish(NONE);
}

/** Media removed here, by the write sequence their removal settled at. */
export function useContinueWatchingRemoved(): ContinueWatchingRemovals {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    () => removed,
    () => removed,
  );
}
