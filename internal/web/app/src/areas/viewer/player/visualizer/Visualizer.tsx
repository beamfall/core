import { type RefObject, useEffect, useRef, useState } from "react";
import type { PlaybackStream } from "../../lib/playback";
import { usePrefersReducedMotion } from "../trackChangeMotion";
import { decodable, heardTime } from "../useGaplessAudio";
import { decodeTrack, easeFrame, silentFrame, TrackAnalysis } from "./analysis";
import { loadArtwork } from "./artwork";
import { loadOscillux } from "./oscillux";
import { Renderer } from "./renderer";
import { canVisualize, type VisualChoice } from "./visualChoice";
import {
  chosenVisual,
  nextInRotation,
  ROTATE_GRACE_SECONDS,
  ROTATE_SECONDS,
  type Visual,
  visualFor,
} from "./visuals";

// The longest step one frame advances the visual, so a tab coming back from the background
// does not jump its clock. The governor gets the raw interval instead, so it can tell a pause
// (a stall, a throttled or occluded page) from a slow frame and ignore it.
const MAX_DT = 0.1;
// Render no more pixels than this: the visual sits behind the artwork card.
const MAX_DPR = 2;

/**
 * The canvas's backing size in device pixels, with the pixel ratio capped at MAX_DPR. The
 * observer's device-pixel box is exact, so the blit lands one texel per pixel at a fractional
 * ratio; without it the CSS size times the ratio is rounded (CORE-2516).
 */
export function backingSize(
  device: { inlineSize: number; blockSize: number } | null,
  cssWidth: number,
  cssHeight: number,
  ratio: number,
): [number, number] {
  const r = ratio > 0 ? ratio : 1;
  const dpr = Math.min(MAX_DPR, r);
  if (device) {
    const k = dpr / r;
    return [Math.round(device.inlineSize * k), Math.round(device.blockSize * k)];
  }
  return [Math.round(cssWidth * dpr), Math.round(cssHeight * dpr)];
}
// How the audio values glide across a track change (CORE-2218): toward rest while the next
// track's analysis is not ready, and up into it once it is, for this long.
const SETTLE_TAU = 0.5;
const ARRIVE_TAU = 0.12;
const ARRIVE_SECONDS = 0.6;
// readyState from which a paused element is paused by the listener, not loading a new source.
const HAVE_FUTURE_DATA = 3;

export { canVisualize };

/** The item the player moves to next, which the visualizer decodes ahead. */
export interface NextTrack {
  key: string;
  stream?: PlaybackStream | null;
}

interface Decode {
  abort: AbortController;
  pcm: Promise<Float32Array | null>;
}

// A rotation: the drawn time on the current visual, when it moves on, and the visual it moves
// to, picked and compiled ahead.
export type Rotation = { rotating: boolean; played: number; at: number; upcoming: Visual | null };

// The rotation's next visual: the one compiled ahead while it still fits, or a fresh pick.
export function takeUpcoming(r: Rotation, current: Visual | null, reducedMotion: boolean): Visual {
  const u = r.upcoming;
  r.upcoming = null;
  if (u && u.id !== current?.id && (!reducedMotion || u.calm)) return u;
  return nextInRotation(current?.id ?? null, reducedMotion);
}

// Picks the rotation's next visual and compiles it while the page is idle, so the switch to it
// does not stall a frame. Only that one compiles ahead: compiling all 61 stalled frames through
// the first seconds of play and drove the governor down to a coarse render (CORE-2368).
function queueUpcoming(renderer: Renderer, r: Rotation, current: Visual): void {
  if (!r.rotating) return;
  const reduced = !!window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
  const next = nextInRotation(current.id, reduced);
  r.upcoming = next;
  const compile = () => void renderer.prepare(next).catch(() => {});
  if (window.requestIdleCallback) window.requestIdleCallback(compile, { timeout: 2000 });
  else window.setTimeout(compile, 200);
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
  choice = "album",
  artworkURL = "",
  next = null,
}: {
  videoRef: RefObject<HTMLVideoElement | null>;
  stream: PlaybackStream | null;
  /** Stable per track: keys its analysis. */
  trackKey: string;
  /** Picks the visual for "album"; tracks that share it keep the same visual. Defaults to the track. */
  visualKey?: string;
  /** Which visual shows: a rotation, one per visualKey, or a pinned visual (CORE-2368). */
  choice?: VisualChoice;
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
  // The visual shown, or about to be; and for a rotation, the drawn time when it next moves on.
  const visualRef = useRef<Visual | null>(null);
  const rotateRef = useRef<Rotation>({
    rotating: choice === "rotate",
    played: 0,
    at: ROTATE_SECONDS,
    upcoming: null,
  });
  const trackRef = useRef(trackKey);
  trackRef.current = trackKey;
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
    const silent = silentFrame();
    const shown = silentFrame();
    const reduced = window.matchMedia?.("(prefers-reduced-motion: reduce)");
    // The canvas's size from a ResizeObserver, in device pixels where the browser reports them,
    // so the draw loop reads no layout. Until it reports, and without one, the loop measures.
    let device: { inlineSize: number; blockSize: number } | null = null;
    let css: { width: number; height: number } | null = null;
    const observer =
      typeof ResizeObserver === "undefined"
        ? null
        : new ResizeObserver((entries) => {
            const entry = entries[entries.length - 1];
            if (!entry) return;
            device = entry.devicePixelContentBoxSize?.[0] ?? null;
            css = { width: entry.contentRect.width, height: entry.contentRect.height };
          });
    try {
      observer?.observe(canvas, { box: "device-pixel-content-box" });
    } catch {
      observer?.observe(canvas);
    }
    const fail = (err: unknown) => {
      if (!alive) return;
      console.warn("visualizer: off", err);
      setFailed(true);
    };
    // A rotation moves on after ROTATE_SECONDS of drawn time, at the next downbeat, or a little
    // later without one; under reduced motion it stays on the track's visual.
    const rotate = (renderer: Renderer, dt: number, downbeat: boolean, reducedMotion: boolean) => {
      const r = rotateRef.current;
      if (!r.rotating || reducedMotion) return;
      r.played += dt;
      if (r.played < r.at || (!downbeat && r.played < r.at + ROTATE_GRACE_SECONDS)) return;
      r.played = 0;
      r.at = ROTATE_SECONDS;
      const next = takeUpcoming(r, visualRef.current, reducedMotion);
      visualRef.current = next;
      renderer
        .use(next)
        .then(() => queueUpcoming(renderer, r, next))
        // A renderer replaced after a context loss may still reject; only the live one fails.
        .catch((err: unknown) => {
          if (rendererRef.current === renderer) fail(err);
        });
    };
    const tick = (now: number) => {
      frame = requestAnimationFrame(tick);
      const renderer = rendererRef.current;
      const video = videoRef.current;
      const frameMs = last ? now - last : 1000 / 60;
      const dt = Math.min(MAX_DT, frameMs / 1000);
      last = now;
      if (!renderer || !video || document.hidden) return;
      // Paused by the listener: hold still. Paused while a new source loads: keep moving.
      if (video.paused && video.readyState >= HAVE_FUTURE_DATA) return;
      const box = css ?? canvas.getBoundingClientRect();
      const [width, height] = backingSize(device, box.width, box.height, window.devicePixelRatio);
      try {
        const current = analysisRef.current;
        // The time heard, not the element's: a decoded track sounds while the element shadows it.
        const heard = heardTime(video, trackRef.current);
        const target = current?.analysis.sample(heard * 1000) ?? silent;
        let tau = 0;
        if (target === silent) tau = SETTLE_TAU;
        else if (current && now - current.since < ARRIVE_SECONDS * 1000) tau = ARRIVE_TAU;
        easeFrame(shown, target, tau ? 1 - Math.exp(-dt / tau) : 1);
        renderer.draw(width, height, dt, shown, !!reduced?.matches, frameMs);
        rotate(renderer, dt, shown.values.downbeat > 0.5, !!reduced?.matches);
      } catch (err) {
        cancelAnimationFrame(frame);
        fail(err);
      }
    };
    // Builds the renderer and starts drawing. It runs again after a lost context is restored,
    // when every GL resource is new: the visual and artwork are applied afresh and the
    // feedback and limiter start empty. A newer build supersedes an older one still loading, and a
    // superseded build's failure is ignored: a context lost mid-create rejects it, and removing the
    // canvas then would leave no context to restore.
    let build = 0;
    const start = () => {
      const mine = ++build;
      const current = () => alive && mine === build;
      return loadOscillux()
        .then((osc) => Renderer.create(canvas, osc))
        .then((renderer) => {
          // No hardware WebGL2: the slot stays empty, as without WebGL2 at all.
          if (!renderer) {
            if (current()) setFailed(true);
            return;
          }
          if (!current()) return renderer.dispose();
          rendererRef.current = renderer;
          renderer.setImage(imageRef.current);
          const first = visualRef.current ?? visualFor(trackRef.current);
          visualRef.current = first;
          return renderer.use(first).then(() => {
            if (!current()) return;
            last = 0;
            cancelAnimationFrame(frame);
            frame = requestAnimationFrame(tick);
            queueUpcoming(renderer, rotateRef.current, first);
          });
        })
        .catch((err: unknown) => {
          if (current()) fail(err);
        });
    };
    // A lost context (CORE-2482): preventDefault asks the browser to restore it, rendering
    // stops, and the dead renderer is dropped. Its resources went with the context.
    const lost = (event: Event) => {
      event.preventDefault();
      build++;
      cancelAnimationFrame(frame);
      const old = rendererRef.current;
      rendererRef.current = null;
      try {
        old?.dispose();
      } catch {
        // Deleting objects on a lost context is a no-op or throws; either way it is gone.
      }
    };
    const restored = () => void start();
    canvas.addEventListener("webglcontextlost", lost);
    canvas.addEventListener("webglcontextrestored", restored);
    void start();
    return () => {
      alive = false;
      cancelAnimationFrame(frame);
      observer?.disconnect();
      canvas.removeEventListener("webglcontextlost", lost);
      canvas.removeEventListener("webglcontextrestored", restored);
      rendererRef.current?.dispose();
      rendererRef.current = null;
    };
  }, [enabled, videoRef]);

  // The visual for the choice, applied now or once the renderer is ready: a pinned visual, one
  // per visualKey, or for a rotation a new one at each track and whenever the choice changes.
  const reducedMotion = usePrefersReducedMotion();
  useEffect(() => {
    const rotating = choice === "rotate";
    const rotation: Rotation = {
      rotating,
      played: 0,
      at: ROTATE_SECONDS,
      upcoming: rotateRef.current.upcoming,
    };
    const visual = rotating
      ? takeUpcoming(rotation, visualRef.current, reducedMotion)
      : chosenVisual(choice, visualKey, reducedMotion);
    visualRef.current = visual;
    rotateRef.current = rotation;
    const renderer = rendererRef.current;
    renderer
      ?.use(visual)
      .then(() => queueUpcoming(renderer, rotation, visual))
      .catch((err) => {
        // Ignore a switch on a renderer that a context loss has since replaced.
        if (rendererRef.current !== renderer) return;
        console.warn("visualizer: off", err);
        setFailed(true);
      });
  }, [choice, visualKey, trackKey, reducedMotion]);

  // The track's artwork as the visual's image input, applied now or once the renderer is ready.
  // The previous image stays until the next one has loaded, so a change never shows the neutral
  // stand-in for a few frames (CORE-2218); without usable artwork the stand-in returns.
  useEffect(() => {
    if (!enabled) return;
    const abort = new AbortController();
    const swap = (bitmap: ImageBitmap | null) => {
      const old = imageRef.current;
      imageRef.current = bitmap;
      rendererRef.current?.setImage(bitmap);
      if (old !== bitmap) old?.close();
    };
    loadArtwork(artworkURL, abort.signal)
      .then((bitmap) => {
        if (abort.signal.aborted) return bitmap?.close();
        swap(bitmap);
      })
      .catch((err) => {
        if (abort.signal.aborted) return;
        console.warn("visualizer: no artwork", err);
        swap(null);
      });
    return () => abort.abort();
  }, [enabled, artworkURL]);

  // The last image goes with the slot.
  useEffect(() => {
    if (enabled) return;
    imageRef.current?.close();
    imageRef.current = null;
  }, [enabled]);
  useEffect(
    () => () => {
      imageRef.current?.close();
      imageRef.current = null;
    },
    [],
  );

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
