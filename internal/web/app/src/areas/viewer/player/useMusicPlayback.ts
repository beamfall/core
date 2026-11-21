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
import { type MusicQueueCursor, newStartID, type PlayQueueItem } from "../lib/watchQueue";

export type RepeatMode = "off" | "all" | "one";

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
  advanced: boolean;
  cycle: PlayQueueItem[];
  catalog: readonly PlayQueueItem[];
  fullPlayAll?: boolean;
}

function cursorFor(start: Pick<Start, "id" | "history" | "requeued">): MusicQueueCursor {
  return {
    currentID: start.id,
    playedIDs: start.history.map(({ id }) => id),
    requeuedIDs: start.requeued,
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
  onQueueEdit: (run: string, cursor: MusicQueueCursor) => void;
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
  onQueueEdit,
}: MusicPlaybackOptions) {
  const { rows, busy, loaded } = useMusicQueueSnapshot(profileID);
  const [repeat, setRepeat] = useState<RepeatMode>("off");
  const [shuffled, setShuffled] = useState<string[] | null>(null);
  const [shuffleLoad, setShuffleLoad] = useState<{ start: Start; failed: boolean }>();
  const transition = useRef<Pick<
    Start,
    "id" | "history" | "requeued" | "cycle" | "catalog"
  > | null>(null);
  const epoch = getIdentityEpochSignal();
  const restored = adopted && cursor?.currentID === current.id ? cursor : undefined;
  const currentIndex = catalog.findIndex(({ id }) => id === current.id);
  const begin = (): Start => ({
    id: current.id,
    identity,
    epoch,
    startID,
    run: continuing || newStartID(),
    catalogAnchor: current.id,
    adopted,
    history:
      restored?.playedIDs.map((id) => ({ id, title: "" })) ??
      catalog.slice(0, Math.max(0, currentIndex)),
    requeued: restored?.requeuedIDs ?? [],
    cycle: [],
    catalog: [],
    advanced: !!restored,
  });
  const [start, setStart] = useState(begin);
  const shuffleLoading = shuffleLoad?.start === start && !shuffleLoad.failed;

  const sameIdentity = start.identity === identity && start.epoch === epoch;
  if (start.id !== current.id || start.startID !== startID || !sameIdentity) {
    const advance = transition.current;
    if (sameIdentity && continuing === start.run && advance?.id === current.id) {
      setStart({ ...start, ...advance, startID, advanced: true });
    } else {
      setStart(begin());
      setShuffled(null);
      if (!sameIdentity) setRepeat("off");
    }
  }

  const usingServer = adopted || start.adopted;
  const source = catalog.length && !start.fullPlayAll ? catalog : start.catalog;
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
  const ranks = new Map(shuffled?.map((id, index) => [id, index]));
  const pendingOrder = (tail: readonly PlayQueueItem[]) => {
    const pending = unique([
      ...start.cycle.filter(({ id }) => !played.has(id)),
      ...musicRows.filter(({ id }) => !played.has(id) || requeued.has(id)),
      ...tail.filter(({ id }) => !played.has(id)),
    ]);
    return shuffled
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
    return unique([...continuation(full), ...start.cycle]);
  };

  const latest = useRef({ start, current, history, loadContinuation, onQueueEdit });
  latest.current = { start, current, history, loadContinuation, onQueueEdit };
  const isCurrent = (captured: Start) =>
    latest.current.start.run === captured.run &&
    latest.current.current.id === captured.id &&
    latest.current.start.epoch === captured.epoch;

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
          const edited = {
            ...value,
            history: latest.current.history,
            adopted: true,
            advanced: true,
            cycle: [],
            requeued: [...requeued],
          };
          latest.current.start = edited;
          latest.current.loadContinuation = async () => [];
          setStart(edited);
          setShuffled((value) => {
            if (!value) return value;
            if (intent.kind === "clear") return [];
            const next = value.filter((id) => id !== intent.mediaID);
            if (intent.kind === "remove") return next;
            const at = intent.kind === "move" ? next.indexOf(intent.anchor) : -1;
            const after = intent.kind === "append" || (intent.kind === "move" && intent.after);
            next.splice(at < 0 ? (after ? next.length : 0) : at + Number(after), 0, intent.mediaID);
            return next;
          });
          latest.current.onQueueEdit(edited.run, cursorFor(edited));
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
    repeat,
    shuffled: shuffled !== null,
    shuffleLoading,
    shuffleFailed: shuffleLoad?.start === start && shuffleLoad.failed,
    current,
    currentIndex: currentPosition,
    order,
    upcoming: order.slice(currentPosition + 1),
    adopted: usingServer,
    managesOrder: usingServer || source.length > 0 || serverIndex >= 0 || start.advanced,
    run: start.run,
    next,
    previous: visibleHistory.at(-1)?.item,
    cycleRepeat: () =>
      setRepeat((mode) => (mode === "off" ? "all" : mode === "all" ? "one" : "off")),
    toggleShuffle() {
      if (shuffleLoading) return;
      if (shuffled || !incomplete) {
        setShuffleLoad(undefined);
        setShuffled(shuffled ? null : shuffle(upcoming));
        return;
      }
      setShuffleLoad({ start, failed: false });
      void loadCatalog().then(
        (full) => {
          if (latest.current.start !== start || epoch?.aborted) return;
          setStart({ ...start, catalog: full, fullPlayAll: true });
          setShuffled(shuffle(pendingOrder(continuation(full))));
          setShuffleLoad(undefined);
        },
        () => {
          if (latest.current.start === start && !epoch?.aborted) {
            setShuffleLoad({ start, failed: true });
          }
        },
      );
    },
    async completeOrder() {
      const tail = !usingServer && fullCatalog ? await loadContinuation() : catalogTail;
      if (!isCurrent(start) || epoch?.aborted) throw new Error("The playback position changed");
      return classifyMusic(
        [...history.map(resolve), current, ...pendingOrder(tail).map(resolve)],
        epoch,
      );
    },
    prepareAdvance(id: string, wrap: boolean, previous = false) {
      const index = wrap ? 0 : currentPosition + (previous ? -1 : 1);
      if (order[index]?.id !== id) return;
      const advance = {
        id,
        history: wrap
          ? []
          : previous
            ? history.slice(0, visibleHistory.at(-1)?.index)
            : [...history, current],
        requeued: previous ? start.requeued : start.requeued.filter((entry) => entry !== id),
        cycle:
          wrap || previous
            ? order.filter((item) => !rows.some((row) => row.id === item.id))
            : start.cycle,
        catalog: source,
      };
      if (id === current.id) {
        if (!wrap || usingServer) {
          const advanced = { ...start, ...advance, advanced: true };
          setStart(advanced);
          if (usingServer) onQueueEdit(advanced.run, cursorFor(advanced));
        }
      } else {
        transition.current = advance;
      }
      return {
        cursor: usingServer ? cursorFor(advance) : undefined,
        nextID: (order[index + 1] ?? (repeat === "all" ? order[0] : undefined))?.id,
      };
    },
  };
}

export type MusicPlayback = ReturnType<typeof useMusicPlayback>;
