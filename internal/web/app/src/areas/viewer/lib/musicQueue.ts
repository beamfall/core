import { createContext, useSyncExternalStore } from "react";
import { ApiError, type GraphQLResult, getIdentityEpochSignal, graphql } from "@/lib/api";
import { fetchMediaRows } from "@/lib/graphql/browse";
import type { StringKey } from "@/strings";
import type { PlayQueueItem } from "./watchQueue";

export const MusicQueueProfile = createContext<string | undefined>(undefined);

interface QueueRow extends PlayQueueItem {
  locked?: boolean;
}

type QueueWrite =
  | { field: "queueAdd"; mediaID: string; position?: number }
  | { field: "queueMove"; mediaID: string; position: number }
  | { field: "queueRemove"; mediaID: string }
  | { field: "queueClear" };

const PAGE_SIZE = 200;
const QUEUE_QUERY = `query MusicQueue($query: String, $limit: Int, $offset: Int) {
  sourceItems(kind: "queue", query: $query, limit: $limit, offset: $offset) {
    id title mediaClass libraryID locked
  }
}`;

const writeQueries = {
  queueAdd: `mutation MusicQueueAdd($mediaID: String!, $position: Int, $expectedProfileID: String!) {
    queueAdd(mediaID: $mediaID, position: $position, expectedProfileID: $expectedProfileID) { id }
  }`,
  queueMove: `mutation MusicQueueMove($mediaID: String!, $position: Int!, $expectedProfileID: String!) {
    queueMove(mediaID: $mediaID, position: $position, expectedProfileID: $expectedProfileID) { id }
  }`,
  queueRemove: `mutation MusicQueueRemove($mediaID: String!, $expectedProfileID: String!) {
    queueRemove(mediaID: $mediaID, expectedProfileID: $expectedProfileID) { id }
  }`,
  queueClear: `mutation MusicQueueClear($expectedProfileID: String!) {
    queueClear(expectedProfileID: $expectedProfileID) { id }
  }`,
};

async function readQueue(signal?: AbortSignal): Promise<QueueRow[]> {
  const entries = new Map<string, QueueRow>();
  for (let offset = 0; ; offset += PAGE_SIZE) {
    // A nonempty query selects the explicit-read path (no passive-discovery
    // mute filter). Positions must count every visible row, including muted
    // tracks, locked cards and other media classes.
    const { sourceItems } = await graphql<{ sourceItems: QueueRow[] }>(
      QUEUE_QUERY,
      { query: "queue", limit: PAGE_SIZE, offset },
      signal,
    );
    for (const row of sourceItems) entries.set(row.id, row);
    if (sourceItems.length < PAGE_SIZE) return [...entries.values()];
  }
}

function profileMismatch(error: unknown): boolean {
  if (!(error instanceof ApiError) || !Array.isArray(error.payload)) return false;
  const errors: GraphQLResult<unknown>["errors"] = error.payload;
  return errors?.some((item) => item.extensions?.reason === "PROFILE_MISMATCH") ?? false;
}

export function playableMusic(row: QueueRow): boolean {
  return row.mediaClass === "audio" && !row.locked;
}

interface ActiveMusic {
  currentID: string;
  history: readonly PlayQueueItem[];
  catalog: readonly PlayQueueItem[];
  fullCatalog?: () => Promise<readonly PlayQueueItem[] | undefined>;
  /** Switch the current playback start to the durable queue. */
  adopt: () => void;
  discard: (id?: string) => void;
  reprioritize: (id: string, anchor?: string, after?: boolean) => void;
}

let activeMusic: { profileID: string; epoch?: AbortSignal; read: () => ActiveMusic } | undefined;

export function registerActiveMusic(profileID: string, read: () => ActiveMusic): () => void {
  const player = { profileID, epoch: getIdentityEpochSignal(), read };
  activeMusic = player;
  return () => {
    if (activeMusic === player) activeMusic = undefined;
  };
}

interface QueueSnapshot {
  rows: readonly QueueRow[];
  busy: boolean;
  notice?: { key: StringKey };
}

const EMPTY: QueueSnapshot = { rows: [], busy: false };
const listeners = new Set<() => void>();
let snapshot = EMPTY;
let owner: { profileID: string; epoch?: AbortSignal } | undefined;
let pending = Promise.resolve();

function publish(next: QueueSnapshot) {
  snapshot = next;
  for (const listener of listeners) listener();
}

function bind(profileID: string) {
  const epoch = getIdentityEpochSignal();
  if (owner?.profileID === profileID && owner.epoch === epoch) return owner;
  const bound = { profileID, epoch };
  owner = bound;
  pending = Promise.resolve();
  epoch?.addEventListener(
    "abort",
    () => {
      if (owner !== bound) return;
      owner = undefined;
      pending = Promise.resolve();
      publish(EMPTY);
    },
    { once: true },
  );
  publish(EMPTY);
  return bound;
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function useMusicQueueSnapshot(profileID?: string): QueueSnapshot {
  return useSyncExternalStore(subscribe, () =>
    profileID && owner?.profileID === profileID && owner.epoch === getIdentityEpochSignal()
      ? snapshot
      : EMPTY,
  );
}

type QueueIntent =
  | { kind: "next" | "append"; mediaID: string; title: string }
  | { kind: "move"; mediaID: string; anchor: string; after: boolean }
  | { kind: "remove"; mediaID: string }
  | { kind: "clear" }
  | { kind: "refresh" };

function playedIDs(active?: ActiveMusic): Set<string> {
  return new Set(active ? [active.currentID, ...active.history.map(({ id }) => id)] : []);
}

export function updateMusicQueue(profileID: string, intent: QueueIntent): Promise<void> {
  const bound = bind(profileID);
  const requestedPlayer =
    activeMusic?.profileID === profileID && activeMusic.epoch === bound.epoch
      ? activeMusic
      : undefined;
  const live = () =>
    owner === bound && !bound.epoch?.aborted && bound.epoch === getIdentityEpochSignal();
  const run = async () => {
    if (!live()) return;
    const write = async (operation: QueueWrite) => {
      if (!live()) throw new DOMException("Identity changed", "AbortError");
      const { field, ...args } = operation;
      await graphql(writeQueries[field], { ...args, expectedProfileID: profileID }, bound.epoch, {
        adminUnlock: false,
      });
    };
    const editing = intent.kind !== "refresh";
    if (editing) publish({ ...snapshot, busy: true, notice: undefined });
    let dirty = false;
    let reconciled = [...snapshot.rows];
    try {
      const rows = await readQueue(bound.epoch);
      if (!live()) return;
      if (intent.kind === "refresh") {
        publish({ ...snapshot, rows });
        return;
      }
      reconciled = [...rows];

      const player = activeMusic === requestedPlayer ? requestedPlayer : undefined;
      const active = player?.read();
      if (!active && intent.kind !== "append" && intent.kind !== "next") return;
      const played = playedIDs(active);
      const upcoming = (row: QueueRow) => playableMusic(row) && !played.has(row.id);
      const order = reconciled;
      async function apply(operation: QueueWrite) {
        dirty = true;
        await write(operation);
      }

      if (intent.kind === "clear") {
        const removable = rows.filter(upcoming);
        if (removable.length && removable.length === rows.length) {
          await apply({ field: "queueClear" });
          reconciled = [];
        } else {
          for (const { id } of removable) {
            await apply({ field: "queueRemove", mediaID: id });
            reconciled = reconciled.filter((row) => row.id !== id);
          }
        }
      } else {
        // Persist the catalog continuation before editing it. Preserve every
        // server row's relative order, including rows absent from this panel.
        const existing = new Set(rows.map(({ id }) => id));
        const catalog = (await active?.fullCatalog?.()) ?? active?.catalog ?? [];
        const additions = catalog.filter(({ id }) => !existing.has(id) && !played.has(id));
        const unknown = additions.filter(({ mediaClass }) => !mediaClass).map(({ id }) => id);
        const classified = unknown.length ? await fetchMediaRows(unknown, bound.epoch) : {};
        const missing = additions
          .filter((track) => (track.mediaClass || classified[track.id]?.mediaClass) === "audio")
          .map((track) => ({ ...track, mediaClass: "audio" }));
        if (active && activeMusic !== player) return;
        const mediaID = intent.mediaID;
        const editable = (id: string) =>
          [...rows, ...missing].some((row) => row.id === id && upcoming(row));
        if (
          ((intent.kind === "move" || intent.kind === "remove") && !editable(mediaID)) ||
          (intent.kind === "move" && !editable(intent.anchor))
        ) {
          throw new Error("Queue edit target is no longer available");
        }
        for (const track of missing) {
          await apply({ field: "queueAdd", mediaID: track.id });
          order.push(track);
        }

        const without = order.filter((row) => row.id !== mediaID);
        const entry = order.find((row) => row.id === mediaID) ?? {
          id: mediaID,
          title: intent.kind === "append" || intent.kind === "next" ? intent.title : "",
          mediaClass: "audio",
        };
        switch (intent.kind) {
          case "append":
            await apply({ field: "queueAdd", mediaID });
            reconciled = [...without, entry];
            break;
          case "next": {
            const next = without.findIndex(upcoming);
            const position = next < 0 ? without.length : next;
            await apply({ field: "queueAdd", mediaID, position });
            without.splice(position, 0, entry);
            reconciled = without;
            break;
          }
          case "move": {
            const anchor = without.findIndex((row) => row.id === intent.anchor && upcoming(row));
            const position = anchor + Number(intent.after);
            await apply({ field: "queueMove", mediaID, position });
            without.splice(position, 0, entry);
            reconciled = without;
            break;
          }
          case "remove":
            await apply({ field: "queueRemove", mediaID });
            reconciled = without;
            break;
        }
      }
      if (!live()) return;
      if (active && activeMusic === player) {
        active.adopt();
        if (intent.kind === "clear") active.discard();
        if (intent.kind === "remove") active.discard(intent.mediaID);
        if (intent.kind === "next") active.reprioritize(intent.mediaID);
        if (intent.kind === "move")
          active.reprioritize(intent.mediaID, intent.anchor, intent.after);
        if (intent.kind === "append") active.reprioritize(intent.mediaID, undefined, true);
      }
      publish({ ...snapshot, rows: reconciled });
      const nextRows = dirty ? await readQueue(bound.epoch) : rows;
      if (!live()) return;
      const notice =
        intent.kind === "next"
          ? { key: "musicQueueNextAdded" as const }
          : intent.kind === "append"
            ? { key: "musicQueueAdded" as const }
            : undefined;
      publish({ rows: nextRows, busy: false, notice });
    } catch (error) {
      if (!live()) return;
      if (profileMismatch(error)) {
        owner = undefined;
        publish(EMPTY);
        return;
      }
      // A request can fail after the server applied it. Re-read instead of
      // restoring an old order or repeating a write.
      const rows = dirty ? await readQueue(bound.epoch).catch(() => reconciled) : snapshot.rows;
      if (live())
        publish({
          rows,
          busy: false,
          notice: { key: editing ? "musicQueueWriteFailed" : "musicQueueLoadFailed" },
        });
    } finally {
      if (live() && editing && snapshot.busy) publish({ ...snapshot, busy: false });
    }
  };
  pending = pending.then(run);
  return pending;
}
