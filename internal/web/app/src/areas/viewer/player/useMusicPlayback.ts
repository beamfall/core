import { useEffect, useRef, useState } from "react";
import { getIdentityEpochSignal } from "@/lib/api";
import { fetchMediaRows } from "@/lib/graphql/browse";
import {
  classifyMusic,
  playableMusic,
  registerActiveMusic,
  updateMusicQueue,
  useMusicQueueSnapshot,
} from "../lib/musicQueue";
import {
  type MusicQueueCursor,
  newStartID,
  type PlayQueueItem,
  type RepeatMode,
} from "../lib/watchQueue";

function unique(items: readonly PlayQueueItem[]): PlayQueueItem[] {
  const seen = new Set<string>();
  return items.filter(({ id }) => {
    if (seen.has(id)) return false;
    seen.add(id);
    return true;
  });
}

function shuffle(items: readonly PlayQueueItem[]): string[] {
  const order = items.map(({ id }) => id);
  for (let i = order.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [order[i], order[j]] = [order[j], order[i]];
  }
  return order;
}

interface Start {
  id: string;
  identity: string | null;
  epoch?: AbortSignal;
  startID?: string;
  run: string;
  catalogAnchor: string;
  adopted: boolean;
  history: PlayQueueItem[];
  requeued: string[];
  repeat: RepeatMode;
  shuffledIDs?: string[];
  advanced: boolean;
  cycle: PlayQueueItem[];
  catalog: readonly PlayQueueItem[];
  fullPlayAll?: boolean;
}

function sameShufflePosition(a: Start, b: Start): boolean {
  return (
    a.run === b.run &&
    a.id === b.id &&
    a.startID === b.startID &&
    a.epoch === b.epoch &&
    a.adopted === b.adopted &&
    a.cycle === b.cycle &&
    a.catalog === b.catalog
  );
}

function cursorFor(start: Start): MusicQueueCursor {
  return {
    currentID: start.id,
    playedIDs: start.history.map(({ id }) => id),
    requeuedIDs: start.requeued,
    ...(start.repeat !== "off" ? { repeat: start.repeat } : {}),
    ...(start.shuffledIDs ? { shuffledIDs: start.shuffledIDs } : {}),
    ...(!start.adopted && {
      catalogAnchor: start.catalogAnchor,
      catalogCycleIDs: start.cycle.map(({ id }) => id),
    }),
  };
}

interface MusicPlaybackOptions {
  current: PlayQueueItem;
  audio: boolean;
  identity: string | null;
  profileID?: string;
  startID?: string;
  continuing?: string;
  adopted: boolean;
  cursor?: MusicQueueCursor;
  catalog: readonly PlayQueueItem[];
  catalogIncomplete?: boolean;
  fullCatalog?: () => Promise<readonly PlayQueueItem[] | undefined>;
  onCursorChange: (run: string, cursor: MusicQueueCursor, adopted: boolean) => void;
}

export function useMusicPlayback({
  current,
  audio,
  identity,
  profileID,
  startID,
  continuing,
  adopted,
  cursor,
  catalog,
  catalogIncomplete,
  fullCatalog,
  onCursorChange,
}: MusicPlaybackOptions) {
  const { rows, busy, loaded } = useMusicQueueSnapshot(profileID);
  const readyToSave = loaded && !busy;
  const [shuffleLoad, setShuffleLoad] = useState<{ start: Start; failed: boolean }>();
  const transition = useRef<Pick<
    Start,
    "id" | "history" | "requeued" | "cycle" | "catalog" | "adopted"
  > | null>(null);
  const epoch = getIdentityEpochSignal();
  const restored = cursor?.currentID === current.id ? cursor : undefined;
  const currentIndex = catalog.findIndex(({ id }) => id === current.id);
  const begin = (): Start => ({
    id: current.id,
    identity,
    epoch,
    startID,
    run: continuing || newStartID(),
    catalogAnchor: restored?.catalogAnchor ?? current.id,
    adopted,
    history:
      restored?.playedIDs.map((id) => ({ id, title: "" })) ??
      catalog.slice(0, Math.max(0, currentIndex)),
    requeued: restored?.requeuedIDs ?? [],
    repeat: restored?.repeat ?? "off",
    shuffledIDs: restored?.shuffledIDs,
    cycle: (restored?.catalogCycleIDs ?? []).flatMap((id) => {
      const item = catalog.find((entry) => entry.id === id);
      return item ? [item] : [];
    }),
    catalog: [],
    advanced: !!restored,
  });
  const [start, setStart] = useState(begin);
  const currentShuffleLoad =
    shuffleLoad && sameShufflePosition(shuffleLoad.start, start) ? shuffleLoad : undefined;
  const shuffleLoading = !!currentShuffleLoad && !currentShuffleLoad.failed;

  const sameIdentity = start.identity === identity && start.epoch === epoch;
  if (start.id !== current.id || start.startID !== startID || !sameIdentity) {
    const advance = transition.current;
    if (sameIdentity && continuing === start.run && advance?.id === current.id) {
      setStart({ ...start, ...advance, startID, advanced: true });
    } else {
      const next = begin();
      setStart(sameIdentity && !restored ? { ...next, repeat: start.repeat } : next);
    }
  }

  const usingServer = adopted || start.adopted;
  const { repeat, shuffledIDs } = start;
  const source = usingServer ? [] : catalog.length && !start.fullPlayAll ? catalog : start.catalog;
  const incomplete = !usingServer && !start.fullPlayAll && catalogIncomplete;
  const musicRows = rows.filter(playableMusic);
  const serverIndex = musicRows.findIndex(({ id }) => id === current.id);
  // Older adopted history entries have no cursor. The server prefix is a
  // fallback until the player records the next cursor.
  const history =
    start.advanced || (usingServer && start.history.length)
      ? start.history
      : usingServer
        ? musicRows.slice(0, Math.max(0, serverIndex))
        : catalog.slice(0, Math.max(0, currentIndex));
  const played = new Set([...history.map(({ id }) => id), current.id]);
  const requeued = new Set(start.requeued);
  const continuation = (items: readonly PlayQueueItem[]) =>
    items.slice(items.findIndex(({ id }) => id === start.catalogAnchor) + 1);
  const catalogTail = usingServer ? [] : continuation(source);
  const ranks = new Map(shuffledIDs?.map((id, index) => [id, index]));
  const pendingOrder = (tail: readonly PlayQueueItem[]) => {
    const pending = unique([
      ...musicRows.filter(({ id }) => !played.has(id) || requeued.has(id)),
      ...start.cycle.filter(({ id }) => !played.has(id)),
      ...tail.filter(({ id }) => !played.has(id)),
    ]);
    return shuffledIDs
      ? pending.sort((a, b) => (ranks.get(a.id) ?? ranks.size) - (ranks.get(b.id) ?? ranks.size))
      : pending;
  };

  const upcoming = pendingOrder(catalogTail);
  const known = new Map(
    [...musicRows, ...start.catalog.filter(({ title }) => title)].map((item) => [item.id, item]),
  );
  const unresolved = JSON.stringify(
    audio
      ? [...history, ...upcoming]
          .filter(({ id, title }) => !title && !known.has(id))
          .map(({ id }) => id)
      : [],
  );
  const [resolved, setResolved] = useState<{
    epoch?: AbortSignal;
    identity: string | null;
    rows: Record<string, PlayQueueItem>;
  }>();
  useEffect(() => {
    const ids: string[] = JSON.parse(unresolved);
    if (!ids.length) return;
    const controller = new AbortController();
    void fetchMediaRows(ids, controller.signal).then(
      (rows) => {
        if (!controller.signal.aborted) {
          setResolved({ identity, epoch, rows });
        }
      },
      () => {},
    );
    return () => controller.abort();
  }, [epoch, identity, unresolved]);
  const resolve = (item: PlayQueueItem) => {
    const read =
      resolved?.epoch === epoch && resolved?.identity === identity
        ? resolved.rows[item.id]
        : undefined;
    return item.title ? item : (known.get(item.id) ?? read ?? item);
  };
  const visibleHistory = history
    .map((item, index) => ({ item: resolve(item), index }))
    .filter(({ item }) => !!item.title);
  const currentPosition = visibleHistory.length;
  const order = [...visibleHistory.map(({ item }) => item), current, ...upcoming.map(resolve)];
  const loadCatalog = async () => {
    const full = incomplete && fullCatalog ? await fullCatalog() : source;
    if (!full) throw new Error("The Play all queue is no longer available");
    return full;
  };
  const loadContinuation = async () => {
    if (usingServer) return start.cycle;
    const full = await loadCatalog();
    return unique([...start.cycle, ...continuation(full)]);
  };

  const latest = useRef({
    start,
    current,
    history,
    loadContinuation,
    onCursorChange,
    rows,
    readyToSave,
  });
  latest.current = { start, current, history, loadContinuation, onCursorChange, rows, readyToSave };
  const isSameRun = (captured: Start) =>
    latest.current.start.run === captured.run &&
    latest.current.start.identity === captured.identity &&
    latest.current.start.epoch === captured.epoch;
  const isCurrent = (captured: Start) =>
    isSameRun(captured) && latest.current.current.id === captured.id;
  const persist = (value: Start) => {
    latest.current.start = value;
    setStart(value);
    if (usingServer || (loaded && musicRows.length > 0)) {
      onCursorChange(value.run, cursorFor(value), value.adopted);
    }
  };

  useEffect(() => {
    if (!audio || !profileID || !identity) return;
    const unregister = registerActiveMusic(profileID, () => {
      const captured = latest.current;
      return {
        currentID: captured.current.id,
        history: captured.history,
        requeued: captured.start.requeued,
        loadContinuation: captured.loadContinuation,
        isCurrent: () => isCurrent(captured.start),
        isSameRun: () => isSameRun(captured.start),
        commitEdit: (intent) => {
          const value = latest.current.start;
          const requeued = new Set(value.requeued);
          if (intent.kind === "clear") requeued.clear();
          else if (intent.kind === "remove") requeued.delete(intent.mediaID);
          else if (intent.kind === "next" || intent.kind === "append") {
            if (
              captured.current.id === intent.mediaID ||
              captured.history.some(({ id }) => id === intent.mediaID)
            ) {
              requeued.add(intent.mediaID);
            }
          }
          let shuffledIDs = value.shuffledIDs;
          if (shuffledIDs) {
            const next =
              intent.kind === "clear" ? [] : shuffledIDs.filter((id) => id !== intent.mediaID);
            if (intent.kind !== "clear" && intent.kind !== "remove") {
              const at = intent.kind === "move" ? next.indexOf(intent.anchor) : -1;
              const after = intent.kind === "append" || (intent.kind === "move" && intent.after);
              next.splice(
                at < 0 ? (after ? next.length : 0) : at + Number(after),
                0,
                intent.mediaID,
              );
            }
            shuffledIDs = next;
          }
          const edited = {
            ...value,
            history: latest.current.history,
            adopted: true,
            advanced: true,
            cycle: [],
            requeued: [...requeued],
            shuffledIDs,
          };
          latest.current.start = edited;
          latest.current.loadContinuation = async () => [];
          setStart(edited);
          latest.current.onCursorChange(edited.run, cursorFor(edited), true);
        },
      };
    });
    void updateMusicQueue(profileID, { kind: "refresh" });
    return unregister;
  }, [audio, epoch, identity, profileID]);

  const next =
    order[currentPosition + 1] ??
    (repeat === "all" && !incomplete && (!usingServer || loaded) ? order[0] : undefined);

  return {
    busy,
    readyToSave,
    repeat,
    shuffled: shuffledIDs !== undefined,
    shuffleLoading,
    shuffleFailed: currentShuffleLoad?.failed ?? false,
    current,
    currentIndex: currentPosition,
    order,
    upcoming: order.slice(currentPosition + 1),
    managesOrder: usingServer || source.length > 0 || serverIndex >= 0 || start.advanced,
    run: start.run,
    next,
    previous: visibleHistory.at(-1)?.item,
    cycleRepeat() {
      const value = latest.current.start;
      const mode = value.repeat;
      persist({
        ...value,
        history,
        repeat: mode === "off" ? "all" : mode === "all" ? "one" : "off",
      });
    },
    toggleShuffle() {
      if (shuffleLoading) return;
      if (shuffledIDs || !incomplete) {
        setShuffleLoad(undefined);
        persist({ ...start, history, shuffledIDs: shuffledIDs ? undefined : shuffle(upcoming) });
        return;
      }
      setShuffleLoad({ start, failed: false });
      const stillLoading = () =>
        !epoch?.aborted && sameShufflePosition(start, latest.current.start);
      void loadCatalog().then(
        (full) => {
          if (!stillLoading()) return;
          persist({
            ...latest.current.start,
            catalog: full,
            fullPlayAll: true,
            history,
            shuffledIDs: shuffle(pendingOrder(continuation(full))),
          });
          setShuffleLoad(undefined);
        },
        () => {
          if (stillLoading()) {
            setShuffleLoad({ start, failed: true });
          }
        },
      );
    },
    async completeOrder() {
      if (!readyToSave) throw new Error("The full queue is unavailable");
      const tail = !usingServer && fullCatalog ? await loadContinuation() : catalogTail;
      const tracks = await classifyMusic(
        [...history.map(resolve), current, ...pendingOrder(tail).map(resolve)],
        epoch,
      );
      if (
        latest.current.start !== start ||
        latest.current.rows !== rows ||
        !latest.current.readyToSave ||
        epoch?.aborted
      ) {
        throw new Error("The playback queue changed");
      }
      return tracks;
    },
    prepareAdvance(id: string, wrap: boolean, previous = false) {
      const index = wrap ? 0 : currentPosition + (previous ? -1 : 1);
      if (order[index]?.id !== id) return;
      const serverAdvance =
        usingServer || musicRows.some((item) => item.id === id || item.id === current.id);
      const serverIDs = new Set(rows.map(({ id }) => id));
      const cycle =
        wrap || previous
          ? unique([...source, ...start.cycle, ...order]).filter(({ id }) => !serverIDs.has(id))
          : start.cycle;
      // Preserve a separate catalog continuation when the display mixes the
      // server queue with catalog-only tracks.
      const adopted =
        usingServer ||
        (serverAdvance && !incomplete && catalogTail.length === 0 && cycle.length === 0);
      const advance = {
        ...start,
        id,
        adopted,
        history: wrap
          ? []
          : previous
            ? history.slice(0, visibleHistory.at(-1)?.index)
            : [...history, current],
        requeued: previous ? start.requeued : start.requeued.filter((entry) => entry !== id),
        cycle,
        catalog: source,
      };
      if (id === current.id) {
        if (!wrap || serverAdvance) {
          const advanced = { ...start, ...advance, advanced: true };
          setStart(advanced);
          if (serverAdvance) onCursorChange(advanced.run, cursorFor(advanced), adopted);
        }
      } else {
        transition.current = advance;
      }
      return {
        adopted,
        cursor: serverAdvance ? cursorFor(advance) : undefined,
        nextID: (order[index + 1] ?? (repeat === "all" ? order[0] : undefined))?.id,
      };
    },
  };
}

export type MusicPlayback = ReturnType<typeof useMusicPlayback>;
