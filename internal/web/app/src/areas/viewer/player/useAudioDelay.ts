import { useCallback, useEffect, useRef, useState } from "react";
import type { PlaybackStream } from "../lib/playback";
import { AUDIO_DELAY_LIMIT, clampSync, readSync, writeSync } from "./playbackSync";
import { engineFor } from "./usePlayer";

// Audio delay for the web player (CORE-1671). The browser has no A/V offset
// on a media element, so the element's sound is routed through a Web Audio
// DelayNode. A delay line can only hold sound back: positive values (audio
// later) apply, and the control says plainly that the browser cannot play audio
// early. The graph is built only once a non-zero delay is wanted and the
// AudioContext is running, because createMediaElementSource is permanent for
// the element and a suspended context would silence it.

interface DelayGraph {
  context: AudioContext;
  delay: DelayNode | null;
}

// One graph per element: createMediaElementSource may only be called once.
const graphs = new WeakMap<HTMLMediaElement, DelayGraph>();

type AudioContextCtor = typeof AudioContext;

function audioContextCtor(): AudioContextCtor | null {
  if (typeof window === "undefined") return null;
  const w = window as unknown as {
    AudioContext?: AudioContextCtor;
    webkitAudioContext?: AudioContextCtor;
  };
  return w.AudioContext ?? w.webkitAudioContext ?? null;
}

/**
 * Whether the element's sound can be routed through Web Audio. A cross-origin
 * source fetched without CORS plays silent through a MediaElementSource, so only
 * same-origin and MSE (blob:) sources qualify. The browser's own HLS playback
 * (Safari's native engine) also plays silent through one, so it never does.
 */
export function audioDelayRoutable(video: HTMLMediaElement | null, nativeHls = false): boolean {
  if (nativeHls || !video || !audioContextCtor()) return false;
  const src = video.currentSrc || video.src;
  if (!src) return false;
  if (src.startsWith("blob:")) return true;
  try {
    return new URL(src, window.location.href).origin === window.location.origin;
  } catch {
    return false;
  }
}

/**
 * True when this browser plays HLS through its own engine. A source node cannot
 * be removed once made, and the same element outlives the item: it can switch to
 * an HLS sibling mid-playback (a quality step down) or play the next queued item
 * over HLS, and either would then go silent. So a native-HLS browser never routes
 * the element at all, whatever the current item carries.
 */
export function browserPlaysNativeHls(video: HTMLVideoElement | null): boolean {
  if (!video) return false;
  return engineFor(video, { kind: "hls", url: "" } as PlaybackStream) === "native";
}

export function useAudioDelay({
  videoRef,
  profileID = "",
  mediaID = "",
  stream,
}: {
  videoRef: React.RefObject<HTMLVideoElement | null>;
  profileID?: string;
  mediaID?: string;
  /** The stream playing now; a change re-checks routability. */
  stream: PlaybackStream | null;
}) {
  const [audioDelay, setAudioDelay] = useState(() => readSync(profileID, mediaID).audioDelay);
  const [supported, setSupported] = useState(false);
  const delayRef = useRef(audioDelay);
  delayRef.current = audioDelay;
  const keyRef = useRef({ profileID, mediaID });
  keyRef.current = { profileID, mediaID };
  const nativeHls = browserPlaysNativeHls(videoRef.current);
  const nativeHlsRef = useRef(nativeHls);
  nativeHlsRef.current = nativeHls;

  // Builds (once) and updates the delay line. Stored negative values from
  // another platform clamp to zero here.
  const apply = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    const seconds = Math.max(0, delayRef.current);
    let graph = graphs.get(video);
    if (!graph) {
      if (seconds === 0 || !audioDelayRoutable(video, nativeHlsRef.current)) return;
      const Ctor = audioContextCtor();
      if (!Ctor) return;
      graph = { context: new Ctor(), delay: null };
      graphs.set(video, graph);
    }
    const { context } = graph;
    if (context.state !== "running") {
      // Autoplay policy: resume needs a user gesture. Retried on play and on
      // the next interaction; until then the element plays undelayed.
      void context.resume().then(
        () => {
          if (context.state === "running") apply();
        },
        () => {},
      );
      return;
    }
    if (!graph.delay) {
      if (seconds === 0) return;
      const source = context.createMediaElementSource(video);
      const delay = context.createDelay(AUDIO_DELAY_LIMIT);
      source.connect(delay);
      delay.connect(context.destination);
      graph.delay = delay;
    }
    graph.delay.delayTime.setValueAtTime(seconds, context.currentTime);
  }, [videoRef]);

  // A different item (or Profile) brings its own persisted delay.
  useEffect(() => {
    const stored = readSync(profileID, mediaID).audioDelay;
    delayRef.current = stored;
    setAudioDelay(stored);
    apply();
  }, [profileID, mediaID, apply]);

  useEffect(() => {
    const video = videoRef.current;
    setSupported(audioDelayRoutable(video, nativeHls));
    if (!video) return;
    // An existing graph carries the element's sound even at zero delay, so it
    // must be resumed after an interruption however the delay is set.
    const retry = () => {
      if (delayRef.current > 0 || graphs.has(video)) apply();
    };
    video.addEventListener("play", retry);
    video.addEventListener("loadedmetadata", retry);
    document.addEventListener("pointerdown", retry);
    document.addEventListener("keydown", retry);
    retry();
    return () => {
      video.removeEventListener("play", retry);
      video.removeEventListener("loadedmetadata", retry);
      document.removeEventListener("pointerdown", retry);
      document.removeEventListener("keydown", retry);
    };
  }, [videoRef, stream, nativeHls, apply]);

  const commit = useCallback(
    (next: number) => {
      delayRef.current = next;
      setAudioDelay(next);
      writeSync(keyRef.current.profileID, keyRef.current.mediaID, { audioDelay: next });
      apply();
    },
    [apply],
  );

  const nudgeAudioDelay = useCallback(
    // The web route can only delay, so the floor is zero.
    (delta: number) =>
      commit(clampSync(Math.max(0, delayRef.current) + delta, 0, AUDIO_DELAY_LIMIT)),
    [commit],
  );

  const resetAudioDelay = useCallback(() => commit(0), [commit]);

  return {
    audioDelay: Math.max(0, audioDelay),
    nudgeAudioDelay,
    resetAudioDelay,
    audioDelaySupported: supported,
  };
}
