import { useEffect, useRef, useState } from "react";
import {
  playableMusic,
  registerActiveMusic,
  updateMusicQueue,
  useMusicQueueSnapshot,
} from "../lib/musicQueue";
import { newStartID, type PlayQueueItem } from "../lib/watchQueue";

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
  startID?: string;
  run: string;
  catalogAnchor: string;
  adopted: boolean;
  history: PlayQueueItem[];
  advanced: boolean;
  cycle: PlayQueueItem[];
  catalog: readonly PlayQueueItem[];
}

interface MusicPlaybackOptions {
  current: PlayQueueItem;
  audio: boolean;
  identity: string | null;
  profileID?: string;
  startID?: string;
  continuing?: string;
  adopted: boolean;
  catalog: readonly PlayQueueItem[];
  fullCatalog?: () => Promise<readonly PlayQueueItem[] | undefined>;
  onAdopt: () => void;
}

export function useMusicPlayback({
  current,
  audio,
  identity,
  profileID,
  startID,
  continuing,
  adopted,
  catalog,
  fullCatalog,
  onAdopt,
}: MusicPlaybackOptions) {
  const { rows, busy } = useMusicQueueSnapshot(profileID);
  const [repeat, setRepeat] = useState<RepeatMode>("off");
  const [shuffled, setShuffled] = useState<string[] | null>(null);
  const transition = useRef<Pick<Start, "id" | "history" | "cycle" | "catalog"> | null>(null);
  const currentIndex = catalog.findIndex(({ id }) => id === current.id);
  const begin = (): Start => ({
    id: current.id,
    identity,
    startID,
    run: continuing || newStartID(),
    catalogAnchor: current.id,
    adopted,
    history: catalog.slice(0, Math.max(0, currentIndex)),
    cycle: [],
    catalog: [],
    advanced: false,
  });
  const [start, setStart] = useState(begin);

  if (start.id !== current.id || start.startID !== startID || start.identity !== identity) {
    const advance = transition.current;
    if (start.identity === identity && continuing === start.run && advance?.id === current.id) {
      setStart({ ...start, ...advance, startID, advanced: true });
    } else {
      setStart(begin());
      setShuffled(null);
      if (start.identity !== identity) setRepeat("off");
    }
  }

  const usingServer = adopted || start.adopted;
  const source = catalog.length ? catalog : start.catalog;
  const catalogAnchor = source.findIndex(({ id }) => id === start.catalogAnchor);
  const catalogTail = usingServer ? [] : source.slice(catalogAnchor + 1);
  const history =
    start.advanced || usingServer ? start.history : catalog.slice(0, Math.max(0, currentIndex));
  const played = new Set([...history.map(({ id }) => id), current.id]);
  const pending = unique([...start.cycle, ...rows.filter(playableMusic), ...catalogTail]).filter(
    ({ id }) => !played.has(id),
  );
  const ranks = new Map(shuffled?.map((id, index) => [id, index]));
  const lastRank = ranks.size;
  const upcoming = shuffled
    ? [...pending].sort((a, b) => (ranks.get(a.id) ?? lastRank) - (ranks.get(b.id) ?? lastRank))
    : pending;
  const order = [...history, current, ...upcoming];

  const fallback = unique([...catalogTail, ...start.cycle]);
  const latest = useRef({ start, current, history, fallback, fullCatalog, onAdopt });
  latest.current = { start, current, history, fallback, fullCatalog, onAdopt };
  useEffect(() => {
    if (!audio || !profileID || !identity) return;
    const unregister = registerActiveMusic(profileID, () => {
      const captured = latest.current;
      return {
        currentID: captured.current.id,
        history: captured.history,
        catalog: captured.fallback,
        fullCatalog: captured.start.adopted ? undefined : captured.fullCatalog,
        adopt: () => {
          if (latest.current.start.run !== captured.start.run || latest.current.start.adopted)
            return;
          setStart((value) => ({
            ...value,
            history: latest.current.history,
            adopted: true,
            cycle: [],
          }));
          latest.current.onAdopt();
        },
        discard: (id) => {
          if (latest.current.start.run !== captured.start.run) return;
          setStart((value) => ({
            ...value,
            cycle: id ? value.cycle.filter((item) => item.id !== id) : [],
          }));
        },
        reprioritize: (id, anchor, after) => {
          if (latest.current.start.run !== captured.start.run) return;
          setShuffled((value) => {
            if (!value) return value;
            const next = value.filter((entry) => entry !== id);
            const at = anchor ? next.indexOf(anchor) : -1;
            next.splice(at < 0 ? (after ? next.length : 0) : at + Number(after), 0, id);
            return next;
          });
        },
      };
    });
    void updateMusicQueue(profileID, { kind: "refresh" });
    return unregister;
  }, [audio, identity, profileID]);

  return {
    busy,
    repeat,
    shuffled: shuffled !== null,
    order,
    upcoming,
    adopted: usingServer,
    containsCurrent: rows.some((row) => row.id === current.id),
    run: start.run,
    next: upcoming[0] ?? (repeat === "all" ? order[0] : undefined),
    previous: history.at(-1),
    cycleRepeat: () =>
      setRepeat((mode) => (mode === "off" ? "all" : mode === "all" ? "one" : "off")),
    toggleShuffle: () => setShuffled(shuffled ? null : shuffle(upcoming)),
    prepareAdvance(id: string, wrap: boolean) {
      const index = order.findIndex((item) => item.id === id);
      if (index < 0) return;
      transition.current = {
        id,
        history: wrap ? [] : order.slice(0, index),
        cycle:
          wrap || index < history.length
            ? order.filter((item) => !rows.some((row) => row.id === item.id))
            : start.cycle,
        catalog: catalog.length ? catalog : start.catalog,
      };
      return (order[index + 1] ?? (repeat === "all" ? order[0] : undefined))?.id;
    },
  };
}

export type MusicPlayback = ReturnType<typeof useMusicPlayback>;
