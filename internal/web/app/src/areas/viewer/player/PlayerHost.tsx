import {
  createContext,
  type ReactNode,
  Suspense,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { queuePlayNextMedia } from "@/lib/graphql";
import { MUSIC_QUEUE_PARAM } from "../lib/musicQueue";
import {
  insertQueueNext,
  type PlayQueueItem,
  readPlayerNavState,
  setActivePlayNextHandler,
} from "../lib/watchQueue";
import { LazyHostedPlayer } from "./LazyPlayerPage";
import "./mini-player.css";

// The persistent player host (CORE-1633). It sits above the viewer routes and
// owns the player engine (the media element, playback-info, progress beacons,
// Media Session, the queue), so music keeps playing while the listener
// browses. The watch route attaches to it and the host shows the full player;
// when the route closes, an audio Media carries on in a docked mini player and
// anything else stops, as leaving the route always did. The host is keyed by
// Profile with the routes, so a Profile switch ends playback.

/** What the watch route was opened with: the Media and its route's state and query. */
export interface WatchEntry {
  mediaId: string;
  state: unknown;
  search: string;
  /** Epoch at which the host selected this title, if this entry is a selection. */
  titleSelectionEpoch?: number;
}

export interface PlayerHostControls {
  /** The watch route shows this entry in the full player. */
  attach: (entry: WatchEntry) => void;
  /** The watch route closed: audio docks in the mini player, anything else stops. */
  detach: () => void;
  /** Ends playback (a pre-play queue starting, the mini player's Stop, a terminal block). */
  stop: () => void;
  /** Inserts an item immediately after the currently playing item (CORE-3250). */
  playNext?: (item: PlayQueueItem, identity: string | null) => Promise<boolean>;
}

/** What the host hands the player it mounts. */
export interface HostedPlayerProps {
  entry: WatchEntry;
  /** Docked: no watch route is open, so the player shows as the mini player. */
  docked: boolean;
  /** Whether the playing Media is audio, which decides what detach does. */
  onAudioChange: (audio: boolean) => void;
  /** Plays another entry without a route change (an advance while docked). */
  onAdvanceDocked: (entry: WatchEntry) => void;
  onStop: () => void;
}

const PlayerHostContext = createContext<PlayerHostControls | null>(null);

/** The host's controls, or null when the player renders without one. */
export function usePlayerHost(): PlayerHostControls | null {
  return useContext(PlayerHostContext);
}

export function PlayerHost({ children }: { children: ReactNode }) {
  const [entry, setEntry] = useState<WatchEntry | null>(null);
  const [attached, setAttached] = useState(false);
  const audioRef = useRef(false);
  const entryRef = useRef<WatchEntry | null>(null);
  entryRef.current = entry;

  const attach = useCallback((next: WatchEntry) => {
    entryRef.current = next;
    setEntry(next);
    setAttached(true);
  }, []);
  const stop = useCallback(() => {
    audioRef.current = false;
    entryRef.current = null;
    setEntry(null);
    setAttached(false);
  }, []);
  const detach = useCallback(() => {
    setAttached(false);
    if (!audioRef.current) {
      entryRef.current = null;
      setEntry(null);
    }
  }, []);
  const onAudioChange = useCallback((audio: boolean) => {
    audioRef.current = audio;
  }, []);
  const playNext = useCallback(async (item: PlayQueueItem, identity: string | null) => {
    const current = entryRef.current;
    if (!current) return false;
    const isServerQueue = new URLSearchParams(current.search).has(MUSIC_QUEUE_PARAM);
    if (isServerQueue) {
      await queuePlayNextMedia(item.id);
      return true;
    }
    const navState = readPlayerNavState(current.state, identity);
    const nextQueue = insertQueueNext(navState.queue, current.mediaId, navState.title, item);
    const baseState = current.state && typeof current.state === "object" ? current.state : {};
    const updated: WatchEntry = {
      ...current,
      state: {
        ...baseState,
        ...navState,
        queue: nextQueue,
        ...(identity ? { queueIdentity: identity } : {}),
      },
    };
    entryRef.current = updated;
    setEntry(updated);
    return true;
  }, []);

  useEffect(() => {
    setActivePlayNextHandler(playNext);
    return () => setActivePlayNextHandler(null);
  }, [playNext]);

  const controls = useMemo(
    () => ({ attach, detach, stop, playNext }),
    [attach, detach, stop, playNext],
  );
  const docked = !!entry && !attached;

  return (
    <PlayerHostContext.Provider value={controls}>
      {children}
      {/* Room below the page so the docked player never covers its end. */}
      {docked && <div aria-hidden="true" className="bf-mini-spacer" />}
      {entry && (
        <Suspense fallback={attached ? <div className="min-h-screen bg-scrim" /> : null}>
          <LazyHostedPlayer
            entry={entry}
            docked={docked}
            onAudioChange={onAudioChange}
            onAdvanceDocked={setEntry}
            onStop={stop}
          />
        </Suspense>
      )}
    </PlayerHostContext.Provider>
  );
}
