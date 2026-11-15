import { type RefObject, useCallback, useEffect, useRef, useState } from "react";
import { buildCastLoad, fetchCastPlaybackInfo, toLoadRequest } from "./castMedia";
import {
  type CastContextLike,
  type CastSdk,
  castSupported,
  loadCastSdk,
  type RemotePlayerControllerLike,
  type RemotePlayerLike,
} from "./castSdk";

// The Cast sender state for one player (CORE-1679): whether a receiver is
// reachable, the live receiver session, and the RemotePlayerController that
// the sender's own controls drive.

export type CastStatus = "idle" | "starting" | "failed" | "no-route";

export interface RemoteState {
  loaded: boolean;
  paused: boolean;
  currentTime: number;
  duration: number;
}

export interface CastController {
  /** The SDK loaded and reports Cast support. */
  available: boolean;
  /** At least one receiver is on the network, or a session is live. */
  devicesFound: boolean;
  connected: boolean;
  device: string;
  status: CastStatus;
  remote: RemoteState;
  /** Open the receiver picker when needed, then load this Media on the receiver. */
  start: () => void;
  playOrPause: () => void;
  seek: (seconds: number) => void;
  stop: () => void;
}

const IDLE_REMOTE: RemoteState = { loaded: false, paused: true, currentTime: 0, duration: 0 };

// CastContext.setOptions is meant to run once per page.
let configuredContext: CastContextLike | null = null;

function configure(sdk: CastSdk): CastContextLike {
  const context = sdk.framework.CastContext.getInstance();
  if (configuredContext !== context) {
    context.setOptions({
      receiverApplicationId: sdk.chromeCast.media.DEFAULT_MEDIA_RECEIVER_APP_ID,
      autoJoinPolicy: sdk.chromeCast.AutoJoinPolicy.ORIGIN_SCOPED,
    });
    configuredContext = context;
  }
  return context;
}

function remoteStateOf(player: RemotePlayerLike): RemoteState {
  return {
    loaded: !!player.isMediaLoaded,
    paused: !!player.isPaused,
    currentTime: Number.isFinite(player.currentTime) ? player.currentTime : 0,
    duration: Number.isFinite(player.duration) ? player.duration : 0,
  };
}

export function useCast({
  videoRef,
  mediaID,
  title,
}: {
  videoRef: RefObject<HTMLVideoElement | null>;
  mediaID: string;
  title: string;
}): CastController {
  const [sdk, setSdk] = useState<CastSdk | null>(null);
  const [castState, setCastState] = useState("");
  const [device, setDevice] = useState("");
  const [status, setStatus] = useState<CastStatus>("idle");
  const [remote, setRemote] = useState<RemoteState>(IDLE_REMOTE);
  const playerRef = useRef<{
    player: RemotePlayerLike;
    controller: RemotePlayerControllerLike;
  } | null>(null);
  const lastRemoteTime = useRef(0);
  const wasConnected = useRef(false);

  // Load the SDK only where it can work; anywhere else nothing is fetched.
  useEffect(() => {
    if (!castSupported()) return;
    let cancelled = false;
    void loadCastSdk().then((loaded) => {
      if (!cancelled && loaded) setSdk(loaded);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!sdk) return;
    const { framework } = sdk;
    const context = configure(sdk);
    const player = new framework.RemotePlayer();
    const controller = new framework.RemotePlayerController(player);
    playerRef.current = { player, controller };
    const connectedState = framework.CastState.CONNECTED;

    const readSession = (state: string) => {
      setCastState(state);
      const connected = state === connectedState;
      setDevice(connected ? (context.getCurrentSession()?.getCastDevice().friendlyName ?? "") : "");
      // Casting ended: carry the receiver's position back to the local player,
      // the way a route change hands off position.
      if (wasConnected.current && !connected) {
        const video = videoRef.current;
        if (video && lastRemoteTime.current > 0) video.currentTime = lastRemoteTime.current;
        setRemote(IDLE_REMOTE);
      }
      wasConnected.current = connected;
    };
    const onCastState = (event: { castState?: string }) =>
      readSession(event.castState ?? context.getCastState());
    const onRemoteChange = () => {
      const next = remoteStateOf(player);
      if (next.loaded) lastRemoteTime.current = next.currentTime;
      setRemote(next);
    };

    readSession(context.getCastState());
    context.addEventListener(framework.CastContextEventType.CAST_STATE_CHANGED, onCastState);
    controller.addEventListener(framework.RemotePlayerEventType.ANY_CHANGE, onRemoteChange);
    return () => {
      context.removeEventListener(framework.CastContextEventType.CAST_STATE_CHANGED, onCastState);
      controller.removeEventListener(framework.RemotePlayerEventType.ANY_CHANGE, onRemoteChange);
      playerRef.current = null;
    };
  }, [sdk, videoRef]);

  const start = useCallback(() => {
    if (!sdk) return;
    const context = configure(sdk);
    setStatus("starting");
    // requestSession must run inside the click, before any await.
    const session = context.getCurrentSession()
      ? Promise.resolve(null)
      : Promise.resolve(context.requestSession());
    void session
      .then(async (error) => {
        const live = context.getCurrentSession();
        if (error || !live) {
          setStatus(!error || error === "cancel" ? "idle" : "failed");
          return;
        }
        const video = videoRef.current;
        const localSeconds = video?.currentTime ?? 0;
        video?.pause();
        const info = await fetchCastPlaybackInfo(mediaID);
        const load = buildCastLoad(info, { origin: window.location.origin, title, localSeconds });
        if (load === "no-route") {
          setStatus("no-route");
          return;
        }
        await live.loadMedia(toLoadRequest(sdk.chromeCast, load));
        setStatus("idle");
      })
      .catch((error: unknown) => {
        // Closing the receiver picker rejects with "cancel"; that is not a failure.
        setStatus(error === "cancel" ? "idle" : "failed");
      });
  }, [sdk, mediaID, title, videoRef]);

  const playOrPause = useCallback(() => playerRef.current?.controller.playOrPause(), []);

  const seek = useCallback((seconds: number) => {
    const current = playerRef.current;
    if (!current) return;
    current.player.currentTime = Math.max(0, seconds);
    current.controller.seek();
  }, []);

  const stop = useCallback(() => {
    if (!sdk) return;
    playerRef.current?.controller.stop();
    configure(sdk).endCurrentSession(true);
  }, [sdk]);

  const connected = !!sdk && castState === sdk.framework.CastState.CONNECTED;
  return {
    available: !!sdk,
    devicesFound:
      !!sdk && castState !== "" && castState !== sdk.framework.CastState.NO_DEVICES_AVAILABLE,
    connected,
    device,
    status,
    remote: connected ? remote : IDLE_REMOTE,
    start,
    playOrPause,
    seek,
    stop,
  };
}

/** Forget the configured context (tests only). */
export function resetCastContextForTests(): void {
  configuredContext = null;
}
