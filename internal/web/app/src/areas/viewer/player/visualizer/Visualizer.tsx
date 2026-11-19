import { type RefObject, useEffect, useRef, useState } from "react";
import type { PlaybackStream } from "../../lib/playback";
import { usePrefersReducedMotion } from "../trackChangeMotion";
import { decodable, heardTime } from "../useGaplessAudio";
import { decodeTrack, easeFrame, silentFrame, TrackAnalysis } from "./analysis";
import { loadArtwork } from "./artwork";
import { loadOscillux } from "./oscillux";
import { MAX_SIDE, Renderer } from "./renderer";
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
// The shortest refresh period the limiter's shown time trusts (a 250 Hz display): two callbacks
// closer than that are a browser catching up, not a display refreshing.
const MIN_REFRESH_MS = 4;
// The longest refresh period of any display a browser presents on (24 Hz): a host drawing slower
// than its display, as a throttled one does, cannot measure the refresh, and a paint can be late by
// one refresh, so a frame is assumed shown for no longer than this (Codex round 20).
const MAX_REFRESH_MS = 1000 / 24;
// Render no more pixels than this: the visual sits behind the artwork card.
const MAX_DPR = 2;

/**
 * The canvas's backing size in texels and the texel's side in physical pixels: a whole number,
 * the fewest that keep the ratio of texels to CSS pixels at most MAX_DPR and each side at most
 * MAX_SIDE. The canvas is drawn pixelated when a texel spans several physical pixels, so each
 * shows its texel's colour: interpolated pixels between texels would make changes of their own
 * that the flash limiter never saw (Codex round 12). The observer's device-pixel box is exact;
 * without it the CSS size times the ratio is used (CORE-2516). The texels fit inside the box,
 * rounded down, and the canvas is sized to exactly texels times texel device pixels, letterboxed,
 * so no texel is stretched across a fractional pixel (Codex round 13).
 */
export function backingSize(
  device: { inlineSize: number; blockSize: number } | null,
  cssWidth: number,
  cssHeight: number,
  ratio: number,
): [number, number, number] {
  const [width, height, texel] = texels(device, cssWidth, cssHeight, ratio);
  return [Math.max(1, width), Math.max(1, height), texel];
}
/**
 * True when the slot is under one physical pixel in either dimension: it shows nothing. A slot
 * that cuts a texel still shows it, judged against the pixels in view (Codex rounds 20 to 23).
 */
export function collapsed(
  device: { inlineSize: number; blockSize: number } | null,
  cssWidth: number,
  cssHeight: number,
  ratio: number,
): boolean {
  const [width, height] = texels(device, cssWidth, cssHeight, ratio);
  return width < 1 || height < 1;
}
function texels(
  device: { inlineSize: number; blockSize: number } | null,
  cssWidth: number,
  cssHeight: number,
  ratio: number,
): [number, number, number] {
  const r = ratio > 0 ? ratio : 1;
  const w = device ? device.inlineSize : cssWidth * r;
  const h = device ? device.blockSize : cssHeight * r;
  // A slot narrower than the display-scale texel takes a smaller one rather than clip a texel:
  // a clipped texel is partly on screen, and neither showing nor clearing it is safe when the
  // slot changes (Codex rounds 21 and 22). Only a side over MAX_SIDE keeps a larger texel.
  const texel = Math.max(
    1,
    Math.min(Math.ceil(r / MAX_DPR - 1e-9), Math.floor(w + 1e-6), Math.floor(h + 1e-6)),
    Math.ceil(w / MAX_SIDE),
    Math.ceil(h / MAX_SIDE),
  );
  // A slot under the texel a side over MAX_SIDE needs keeps one texel, cut by the slot: the
  // renderer judges a cut texel in full against the pixels shown (Codex round 23).
  const fit = (n: number) => (n < 1 - 1e-6 ? 0 : Math.max(1, Math.floor(n / texel + 1e-6)));
  return [fit(w), fit(h), texel];
}

/** The slot's size in whole physical pixels, which can cut the last texel in each dimension. */
export function slotPixels(
  device: { inlineSize: number; blockSize: number } | null,
  cssWidth: number,
  cssHeight: number,
  ratio: number,
): [number, number] {
  const r = ratio > 0 ? ratio : 1;
  const w = device ? device.inlineSize : cssWidth * r;
  const h = device ? device.blockSize : cssHeight * r;
  return [Math.floor(w + 1e-6), Math.floor(h + 1e-6)];
}

/** The canvas's clip-path for a frame of which the limiter has judged viewWidth x viewHeight
 * physical pixels in view, from the top left: nothing past them shows, so a slot that grows
 * before the next draw, or past a texel it cut, reveals the slot's background and never a pixel
 * the limiter has not judged (Codex round 24). Empty when the whole frame is in view. */
export function viewClip(
  shown: { width: number; height: number; texel: number; viewWidth: number; viewHeight: number },
  ratio: number,
): string {
  const r = ratio > 0 ? ratio : 1;
  const right = Math.max(0, shown.width * shown.texel - shown.viewWidth) / r;
  const bottom = Math.max(0, shown.height * shown.texel - shown.viewHeight) / r;
  return right > 0 || bottom > 0 ? `inset(0 ${right}px ${bottom}px 0)` : "";
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
    let lastDrawn: number | null = null;
    let lastWall: number | null = null;
    // The shortest interval between draws since the loop started, and at most a 24 Hz refresh:
    // about the display's refresh period. A frame can be painted late and the next one on time, so
    // the time a frame was shown can be as short as one refresh whatever the clocks between draws
    // say (Codex rounds 19 and 20).
    let refreshMs = MAX_REFRESH_MS;
    // The backing size of the last draw while it is unpainted: a draw sets it, and the next
    // animation frame, which runs after the paint, clears it.
    let unpainted: string | null = null;
    const silent = silentFrame();
    const shown = silentFrame();
    const reduced = window.matchMedia?.("(prefers-reduced-motion: reduce)");
    // The slot's size from a ResizeObserver, in device pixels where the browser reports them,
    // so the draw loop reads no layout. Until it first reports, the loop measures. The
    // slot, not the canvas, is observed: the loop sizes the canvas to the frame it shows.
    const slot = canvas.parentElement ?? canvas;
    let device: { inlineSize: number; blockSize: number } | null = null;
    let css: { width: number; height: number } | null = null;
    const observer = new ResizeObserver((entries) => {
      const entry = entries[entries.length - 1];
      if (!entry) return;
      device = entry.devicePixelContentBoxSize?.[0] ?? null;
      css = { width: entry.contentRect.width, height: entry.contentRect.height };
      // Observers run after animation frames and before the paint: a slot resized since this
      // frame's draw would show a frame judged against the old area limit, so it is withdrawn
      // and the next draw judges at the new size (Codex round 18).
      // A slot that collapsed shows nothing; reopening it exposes black area (Codex round 20).
      if (collapsed(device, css.width, css.height, window.devicePixelRatio)) {
        unpainted = null;
        try {
          rendererRef.current?.collapse();
        } catch {
          // A lost context shows nothing; its restore starts the limiter afresh.
        }
        return;
      }
      const drawn = unpainted;
      if (drawn === null) return;
      unpainted = null;
      const key = [
        ...backingSize(device, css.width, css.height, window.devicePixelRatio),
        ...slotPixels(device, css.width, css.height, window.devicePixelRatio),
      ].join();
      if (key === drawn) return;
      try {
        rendererRef.current?.withdraw();
      } catch {
        // A lost context shows nothing to withdraw; its restore starts the limiter afresh.
      }
    });
    try {
      observer.observe(slot, { box: "device-pixel-content-box" });
    } catch {
      observer.observe(slot);
    }
    // Fit, filter and clip the frame the renderer shows. During a display-scale fade, all three
    // still follow the old frame's texel size.
    let cssSize = "";
    let clipped = "";
    const fitCanvas = (renderer: Renderer) => {
      const shown = renderer.shown;
      if (!shown) return;
      const r = window.devicePixelRatio > 0 ? window.devicePixelRatio : 1;
      const texel = shown.texel;
      const rendering = texel > 1 ? "pixelated" : "";
      if (canvas.style.imageRendering !== rendering) canvas.style.imageRendering = rendering;
      const clip = viewClip(shown, r);
      if (clip !== clipped) {
        clipped = clip;
        canvas.style.clipPath = clip;
      }
      const next = `${(shown.width * texel) / r}px ${(shown.height * texel) / r}px`;
      if (next === cssSize) return;
      cssSize = next;
      canvas.style.width = `${(shown.width * texel) / r}px`;
      canvas.style.height = `${(shown.height * texel) / r}px`;
    };
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
        // A renderer replaced or lost after a context loss may still reject; only the live one fails.
        .catch((err: unknown) => {
          if (rendererRef.current === renderer && !renderer.contextLost) fail(err);
        });
    };
    const tick = (now: number) => {
      frame = requestAnimationFrame(tick);
      unpainted = null;
      const renderer = rendererRef.current;
      const video = videoRef.current;
      if (!renderer || !video || document.hidden) return;
      const box = css ?? slot.getBoundingClientRect();
      if (collapsed(device, box.width, box.height, window.devicePixelRatio)) {
        try {
          renderer.collapse();
        } catch {
          // A lost context shows nothing; its restore starts the limiter afresh.
        }
        return;
      }
      const [width, height, texel] = backingSize(
        device,
        box.width,
        box.height,
        window.devicePixelRatio,
      );
      const [slotWidth, slotHeight] = slotPixels(
        device,
        box.width,
        box.height,
        window.devicePixelRatio,
      );
      try {
        // Paused by the listener: hold still. Paused while a new source loads: keep moving. A size
        // or display-scale change still draws while paused until the renderer settles. Refitting
        // the held frame accounts for a DPR change even when the physical frame stays the same.
        if (
          video.paused &&
          video.readyState >= HAVE_FUTURE_DATA &&
          renderer.settled(width, height, texel, slotWidth, slotHeight)
        ) {
          fitCanvas(renderer);
          return;
        }
        const frameMs = lastDrawn === null ? 1000 / 60 : now - lastDrawn;
        const dt = Math.min(MAX_DT, frameMs / 1000);
        // The limiter ages its evidence by a lower bound on the time the last frame was shown:
        // the shorter of the frame timestamps' interval and the time between draws (a late
        // callback has a stale timestamp, Codex round 18), and no more than the refresh period,
        // since the last frame's paint may have been late (Codex round 19). A device that draws
        // slower than its display refreshes ages its evidence slower: held areas wait longer.
        const wall = performance.now();
        const interval =
          lastWall === null ? frameMs : Math.max(0, Math.min(frameMs, wall - lastWall));
        if (lastWall !== null) refreshMs = Math.min(refreshMs, Math.max(MIN_REFRESH_MS, interval));
        // refreshMs only bounds the display's refresh from above: a browser may run animation
        // frames at every other refresh (a 120 Hz display at 60 Hz, a throttled one at 30 Hz), so
        // the refresh can be as short as half the interval. A frame painted one refresh late was
        // shown for max(refresh, interval - refresh), which is least at half the interval when
        // the refresh is unknown (Codex round 21). A display that changes its refresh rate
        // between two paints is over-credited one interval (Codex round 22, OSC-0046).
        const shownMs = Math.min(refreshMs, Math.max(interval / 2, interval - refreshMs));
        const current = analysisRef.current;
        // The time heard, not the element's: a decoded track sounds while the element shadows it.
        const heard = heardTime(video, trackRef.current);
        const target = current?.analysis.sample(heard * 1000) ?? silent;
        let tau = 0;
        if (target === silent) tau = SETTLE_TAU;
        else if (current && now - current.since < ARRIVE_SECONDS * 1000) tau = ARRIVE_TAU;
        easeFrame(shown, target, tau ? 1 - Math.exp(-dt / tau) : 1);
        renderer.draw(
          width,
          height,
          dt,
          shown,
          !!reduced?.matches,
          frameMs,
          texel,
          shownMs,
          slotWidth,
          slotHeight,
        );
        lastDrawn = now;
        lastWall = wall;
        unpainted = [width, height, texel, slotWidth, slotHeight].join();
        fitCanvas(renderer);
        rotate(renderer, dt, shown.values.downbeat > 0.5, !!reduced?.matches);
      } catch (err) {
        cancelAnimationFrame(frame);
        // A context lost mid-frame throws before its webglcontextlost event arrives; the loss and
        // restore handlers recover it, so only a live context fails the visualizer.
        if (!renderer.contextLost) fail(err);
      }
    };
    // Builds the renderer and starts drawing. It runs again after a lost context is restored,
    // when every GL resource is new: the visual and artwork are applied afresh and the
    // feedback starts fresh while the limiter starts black with its budgets spent. A newer build
    // supersedes an older one still loading. Ignore a superseded build's failure: a context lost
    // mid-create rejects it, and removing the canvas then would leave no context to restore.
    let build = 0;
    // A context lost before its webglcontextlost event arrives: GL calls fail first, and the loss
    // and restore handlers will recover it, so a failure then does not remove the canvas.
    const lostNow = () => {
      try {
        return !!canvas.getContext("webgl2")?.isContextLost();
      } catch {
        return false;
      }
    };
    const start = (restored = false) => {
      const mine = ++build;
      const current = () => alive && mine === build;
      return loadOscillux()
        .then((osc) => Renderer.create(canvas, osc, restored))
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
            lastDrawn = null;
            lastWall = null;
            cancelAnimationFrame(frame);
            frame = requestAnimationFrame(tick);
            queueUpcoming(renderer, rotateRef.current, first);
          });
        })
        .catch((err: unknown) => {
          if (current() && !lostNow()) fail(err);
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
    const restored = () => void start(true);
    canvas.addEventListener("webglcontextlost", lost);
    canvas.addEventListener("webglcontextrestored", restored);
    void start();
    return () => {
      alive = false;
      cancelAnimationFrame(frame);
      observer.disconnect();
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
        // Ignore a switch on a renderer that a context loss has since replaced or is replacing.
        if (rendererRef.current !== renderer || renderer.contextLost) return;
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
