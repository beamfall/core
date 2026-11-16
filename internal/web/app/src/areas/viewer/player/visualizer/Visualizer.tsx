import { type RefObject, useEffect, useRef, useState } from "react";
import type { PlaybackStream } from "../../lib/playback";
import { decodable } from "../useGaplessAudio";
import { decodeTrack, easeFrame, silentFrame, TrackAnalysis } from "./analysis";
import { loadArtwork } from "./artwork";
import { loadOscillux } from "./oscillux";
import { Renderer } from "./renderer";
import { VISUALS, visualFor } from "./visuals";

// The longest step one frame advances the visual, so a tab coming back from the background
// does not jump its clock.
const MAX_DT = 0.1;
// Render no more pixels than this: the visual sits behind the artwork card.
const MAX_DPR = 2;
// How the audio values glide across a track change (CORE-2218): toward rest while the next
// track's analysis is not ready, and up into it once it is, for this long.
const SETTLE_TAU = 0.5;
const ARRIVE_TAU = 0.12;
const ARRIVE_SECONDS = 0.6;
// readyState from which a paused element is paused by the listener, not loading a new source.
const HAVE_FUTURE_DATA = 3;

/** True where this browser can draw an Oscillux visual. */
export function canVisualize(): boolean {
  return (
    typeof WebGL2RenderingContext !== "undefined" && typeof OfflineAudioContext !== "undefined"
  );
}

/** The item the player moves to next, which the visualizer decodes ahead. */
export interface NextTrack {
  key: string;
  stream?: PlaybackStream | null;
}

interface Decode {
  abort: AbortController;
  pcm: Promise<Float32Array | null>;
}

// The pinned Oscillux visualizer for an audio item (CORE-1309): one of the release's visuals,
// reacting to the playing track and passed through Oscillux's frame flash limiter. It draws
// only for a direct-play stream from this origin, which it decodes for analysis; otherwise,
// or without hardware WebGL2, the slot stays empty. A failure at any point leaves the slot empty too:
// the visual is decoration and never interrupts playback.
//
// Track changes keep it moving (CORE-2218): the renderer stays up while the next source loads,
// the next track is decoded ahead, and the audio values glide from one track to the next.
export function Visualizer({
  videoRef,
  stream,
  trackKey,
  visualKey = trackKey,
  artworkURL = "",
  next = null,
}: {
  videoRef: RefObject<HTMLVideoElement | null>;
  stream: PlaybackStream | null;
  /** Stable per track: keys its analysis. */
  trackKey: string;
  /** Picks the visual; tracks that share it keep the same visual. Defaults to the track. */
  visualKey?: string;
  /** The track's artwork: the visual's image input when it is served from this origin. */
  artworkURL?: string;
  next?: NextTrack | null;
}) {
  const live = (decodable(stream) && stream?.url) || "";
  // While the next source loads the stream is briefly null: keep the last one, so the renderer
  // (and its compiled visuals) stays up instead of being torn down and rebuilt.
  const [held, setHeld] = useState(live);
  if (stream !== null && held !== live) setHeld(live);
  const url = stream === null ? held : live;
  // Which track the current URL belongs to: the item changes before its stream arrives.
  const [owner, setOwner] = useState({ url, key: trackKey });
  if (owner.url !== url) setOwner({ url, key: trackKey });

  const [failed, setFailed] = useState(false);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const rendererRef = useRef<Renderer | null>(null);
  const analysisRef = useRef<{ analysis: TrackAnalysis; since: number } | null>(null);
  const keyRef = useRef(visualKey);
  const imageRef = useRef<ImageBitmap | null>(null);
  const decodes = useRef(new Map<string, Decode>());
  const enabled = !!url && !failed && canVisualize();

  // The renderer and its draw loop, for as long as the slot is shown.
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!enabled || !canvas) return;
    let alive = true;
    let frame = 0;
    let last = 0;
    let warm = 0;
    const silent = silentFrame();
    const shown = silentFrame();
    const reduced = window.matchMedia?.("(prefers-reduced-motion: reduce)");
    const fail = (err: unknown) => {
      if (!alive) return;
      console.warn("visualizer: off", err);
      setFailed(true);
    };
    const tick = (now: number) => {
      frame = requestAnimationFrame(tick);
      const renderer = rendererRef.current;
      const video = videoRef.current;
      const dt = last ? Math.min(MAX_DT, (now - last) / 1000) : 1 / 60;
      last = now;
      if (!renderer || !video || document.hidden) return;
      // Paused by the listener: hold still. Paused while a new source loads: keep moving.
      if (video.paused && video.readyState >= HAVE_FUTURE_DATA) return;
      const rect = canvas.getBoundingClientRect();
      const dpr = Math.min(MAX_DPR, window.devicePixelRatio || 1);
      try {
        const current = analysisRef.current;
        const target = current?.analysis.sample(video.currentTime * 1000) ?? silent;
        let tau = 0;
        if (target === silent) tau = SETTLE_TAU;
        else if (current && now - current.since < ARRIVE_SECONDS * 1000) tau = ARRIVE_TAU;
        easeFrame(shown, target, tau ? 1 - Math.exp(-dt / tau) : 1);
        renderer.draw(
          Math.round(rect.width * dpr),
          Math.round(rect.height * dpr),
          dt,
          shown,
          !!reduced?.matches,
        );
      } catch (err) {
        cancelAnimationFrame(frame);
        fail(err);
      }
    };
    // Compiles the other visuals while the page is idle, one at a time, so the first switch to
    // one does not stall a frame.
    const idle = (fn: () => void) =>
      window.requestIdleCallback ? window.requestIdleCallback(fn) : window.setTimeout(fn, 200);
    const warmUp = (renderer: Renderer, i: number) => {
      if (!alive || i >= VISUALS.length) return;
      warm = idle(() => {
        renderer
          .prepare(VISUALS[i])
          .then(() => warmUp(renderer, i + 1))
          .catch(() => {});
      });
    };
    loadOscillux()
      .then((osc) => Renderer.create(canvas, osc))
      .then((renderer) => {
        // No hardware WebGL2: the slot stays empty, as without WebGL2 at all.
        if (!renderer) {
          if (alive) setFailed(true);
          return;
        }
        if (!alive) return renderer.dispose();
        rendererRef.current = renderer;
        renderer.setImage(imageRef.current);
        return renderer.use(visualFor(keyRef.current)).then(() => {
          if (!alive) return;
          frame = requestAnimationFrame(tick);
          warmUp(renderer, 0);
        });
      })
      .catch(fail);
    const lost = (event: Event) => {
      event.preventDefault();
      fail(new Error("WebGL context lost"));
    };
    canvas.addEventListener("webglcontextlost", lost);
    return () => {
      alive = false;
      cancelAnimationFrame(frame);
      if (window.cancelIdleCallback) window.cancelIdleCallback(warm);
      else window.clearTimeout(warm);
      canvas.removeEventListener("webglcontextlost", lost);
      rendererRef.current?.dispose();
      rendererRef.current = null;
    };
  }, [enabled, videoRef]);

  // The visual, applied now or once the renderer is ready.
  useEffect(() => {
    keyRef.current = visualKey;
    rendererRef.current?.use(visualFor(visualKey)).catch((err) => {
      console.warn("visualizer: off", err);
      setFailed(true);
    });
  }, [visualKey]);

  // The track's artwork as the visual's image input, applied now or once the renderer is ready.
  // Without usable artwork the neutral stand-in stays.
  useEffect(() => {
    if (!enabled) return;
    const abort = new AbortController();
    let image: ImageBitmap | null = null;
    loadArtwork(artworkURL, abort.signal)
      .then((bitmap) => {
        if (abort.signal.aborted) return bitmap?.close();
        if (!bitmap) return;
        image = bitmap;
        imageRef.current = bitmap;
        rendererRef.current?.setImage(bitmap);
      })
      .catch((err) => {
        if (!abort.signal.aborted) console.warn("visualizer: no artwork", err);
      });
    return () => {
      abort.abort();
      if (imageRef.current === image) {
        imageRef.current = null;
        rendererRef.current?.setImage(null);
      }
      image?.close();
    };
  }, [enabled, artworkURL]);

  // The track's analysis: decoded ahead when it was the next track, else decoded now. Until it
  // is ready the visual glides toward rest.
  useEffect(() => {
    if (!enabled) return;
    const source = owner.key === trackKey ? owner.url : "";
    const decode = decodes.current.get(trackKey) ?? (source && start(trackKey, source));
    if (!decode) return;
    let alive = true;
    let analysis: TrackAnalysis | null = null;
    Promise.all([loadOscillux(), decode.pcm])
      .then(([osc, pcm]) => {
        // Too long to decode: the visual keeps drawing, idle.
        if (!alive || !pcm) return;
        analysis = new TrackAnalysis(osc, pcm);
        analysisRef.current = { analysis, since: performance.now() };
      })
      .catch((err) => {
        // Without analysis the visual keeps drawing, idle.
        if (alive && !decode.abort.signal.aborted) console.warn("visualizer: no analysis", err);
      });
    return () => {
      alive = false;
      if (analysisRef.current?.analysis === analysis) analysisRef.current = null;
      analysis?.free();
    };

    function start(key: string, from: string): Decode {
      const abort = new AbortController();
      const entry = { abort, pcm: decodeTrack(from, abort.signal) };
      entry.pcm.catch(() => {});
      decodes.current.set(key, entry);
      return entry;
    }
  }, [enabled, trackKey, owner]);

  // The next track, decoded once this one is, so the change to it starts with its analysis.
  // Decodes for any other track are dropped.
  const nextKey = next?.key ?? "";
  const nextURL = (decodable(next?.stream) && next?.stream?.url) || "";
  useEffect(() => {
    const map = decodes.current;
    for (const [key, decode] of map) {
      if (key !== trackKey && key !== nextKey) {
        decode.abort.abort();
        map.delete(key);
      }
    }
    if (!enabled || !nextKey || !nextURL || map.has(nextKey)) return;
    let alive = true;
    const current = map.get(trackKey)?.pcm ?? Promise.resolve(null);
    current
      .catch(() => null)
      .then(() => {
        if (!alive || map.has(nextKey)) return;
        const abort = new AbortController();
        const pcm = decodeTrack(nextURL, abort.signal);
        pcm.catch(() => {});
        map.set(nextKey, { abort, pcm });
      });
    return () => {
      alive = false;
    };
  }, [enabled, trackKey, nextKey, nextURL]);

  // Unmounting drops every decode in flight.
  useEffect(() => {
    const map = decodes.current;
    return () => {
      for (const decode of map.values()) decode.abort.abort();
      map.clear();
    };
  }, []);

  if (!enabled) return null;
  return <canvas ref={canvasRef} className="bf-audio-visualizer-canvas" />;
}
