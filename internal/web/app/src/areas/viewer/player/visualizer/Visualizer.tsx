import { type RefObject, useEffect, useLayoutEffect, useRef, useState } from "react";
import type { PlaybackStream } from "../../lib/playback";
import { usePrefersReducedMotion } from "../trackChangeMotion";
import { decodable, heardTime } from "../useGaplessAudio";
import { decodeTrack, easeFrame, silentFrame, TrackAnalysis } from "./analysis";
import { loadArtwork } from "./artwork";
import { lagBounds, lagHistory, recordLag } from "./limiter";
import { loadOscillux } from "./oscillux";
import { MAX_SIDE, Renderer, sameFrame, TRACKED_BLACK } from "./renderer";
import { canVisualize, setShownVisual, type VisualChoice } from "./visualChoice";
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
// Frames a device pixel ratio change waits for the slot's report at the new ratio before it
// takes the device-pixel box it already has (tick).
const REPORT_FRAMES = 3;
// The visual steps at 60 Hz at most, as in Oscillux's reference hosts (hosts/web/host.js at the
// pinned rev): its feedback, trails and limiter are tuned per 60 Hz step, so a faster display
// skips a tick that comes early rather than stepping them faster, and a late step's remainder
// carries, so a 120 Hz display steps on every other tick and a 144 Hz one near 60 Hz. No deficit
// carries: a display a little faster than 60 Hz steps on every tick rather than dropping one.
const STEP_MS = 1000 / 60;
// How early a tick may come and still step: a display's intervals jitter around its rate.
const STEP_SLACK_MS = 1.5;
// Render no more pixels than this: the visual sits behind the artwork card.
const MAX_DPR = 2;

// The paint-lag bounds the limiter is credited with are shared with the slideshow (limiter.ts).
export { type LagHistory, lagBound, lagBounds, lagHistory, recordLag } from "./limiter";

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
  const [width, height, texel] = measure([0, 0, 0, 0, 0], device, cssWidth, cssHeight, ratio);
  return [width, height, texel];
}

/** The slot's size in whole physical pixels, which can cut the last texel in each dimension. */
export function slotPixels(
  device: { inlineSize: number; blockSize: number } | null,
  cssWidth: number,
  cssHeight: number,
  ratio: number,
): [number, number] {
  const m = measure([0, 0, 0, 0, 0], device, cssWidth, cssHeight, ratio);
  return [m[3], m[4]];
}

/** A draw request, as the renderer's draw takes it: the backing size in texels, the texel's side
 * and the slot's size in physical pixels. */
export type Request = [
  width: number,
  height: number,
  texel: number,
  slotWidth: number,
  slotHeight: number,
];

/** Writes backingSize and slotPixels for a slot into `into`, so a frame allocates neither
 * (Codex round 29). */
export function measure(
  into: Request,
  device: { inlineSize: number; blockSize: number } | null,
  cssWidth: number,
  cssHeight: number,
  ratio: number,
): Request {
  const r = ratio > 0 ? ratio : 1;
  const w = device ? device.inlineSize : cssWidth * r;
  const h = device ? device.blockSize : cssHeight * r;
  // A slot narrower than the display-scale texel takes a smaller one, so it has a whole texel to
  // show: the renderer shows whole texels only. Only a side over MAX_SIDE keeps a larger texel,
  // and a slot narrower than that one shows black.
  const texel = Math.max(
    1,
    Math.min(Math.ceil(r / MAX_DPR - 1e-9), Math.floor(w + 1e-6), Math.floor(h + 1e-6)),
    Math.ceil(w / MAX_SIDE),
    Math.ceil(h / MAX_SIDE),
  );
  into[0] = Math.max(1, Math.floor(w / texel + 1e-6));
  into[1] = Math.max(1, Math.floor(h / texel + 1e-6));
  into[2] = texel;
  into[3] = Math.floor(w + 1e-6);
  into[4] = Math.floor(h + 1e-6);
  return into;
}

/** The canvas's clip-path for a frame: its whole texels in view, from the top left, as the frame
 * started with them. Nothing past them shows, and only a start changes them: the clip follows
 * what a draw judged, never the slot's size (Codex rounds 24, 29). Empty when the whole frame is
 * in view. */
export function viewClip(shown: NonNullable<Renderer["shown"]>, ratio: number): string {
  const r = ratio > 0 ? ratio : 1;
  const right = (Math.max(0, shown.width - shown.visibleWidth) * shown.texel) / r;
  const bottom = (Math.max(0, shown.height - shown.visibleHeight) * shown.texel) / r;
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
// does not stall a frame. Only that one compiles ahead: compiling all 62 stalled frames through
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

  // The slot shows wherever the canvas shows no judged pixel: past its edge, through its clip
  // (viewClip) and with no frame on screen. There the limiter's trackers hold black, so the slot
  // is that black from before the canvas first paints, and never a themed colour: a strip that
  // came into view from the theme's scrim rose from a level its tracker did not hold (Codex
  // round 25). An empty slot keeps no background, so the artwork backdrop shows through.
  useLayoutEffect(() => {
    const slot = canvasRef.current?.parentElement;
    if (!enabled || !slot) return;
    slot.style.backgroundColor = TRACKED_BLACK;
    return () => {
      slot.style.backgroundColor = "";
    };
  }, [enabled]);

  // The renderer and its draw loop, for as long as the slot is shown.
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!enabled || !canvas) return;
    let alive = true;
    let frame = 0;
    let lastDrawn: number | null = null;
    let lastWall: number | null = null;
    // The step clock: the last tick's timestamp and the time owed to the next step.
    let lastTick: number | null = null;
    let due = 0;
    // The recent animation-frame intervals, for the paint-lag bounds.
    const lags = lagHistory();
    // After a break in painting (the page hidden, the context restored) the next draw is timed as
    // a first one: no interval spans the break, and the limiter is credited none of it, with no
    // stamp cleared (Codex rounds 4 and 5 of CORE-2744).
    const restartClocks = () => {
      lastTick = null;
      lastDrawn = null;
      lastWall = null;
    };
    // Whether the last draw is unpainted, and the request of the last frame: a draw sets the
    // flag, and the next animation frame, which runs after the paint, clears it. The request is
    // measured in place, as the slot an observer reports is (`seen`), so neither a frame nor
    // an observation allocates (Codex rounds 28 to 30).
    let unpainted = false;
    const drawn: Request = [0, 0, 0, 0, 0];
    const seen: Request = [0, 0, 0, 0, 0];
    const silent = silentFrame();
    const shown = silentFrame();
    const reduced = window.matchMedia?.("(prefers-reduced-motion: reduce)");
    // The slot's size from a ResizeObserver, in device pixels where the browser reports them,
    // so the draw loop reads no layout. Until it first reports, the loop measures. The
    // slot, not the canvas, is observed: the loop sizes the canvas to the frame it shows.
    const slot = canvas.parentElement ?? canvas;
    // Both are kept objects written in place: `device` is `deviceBox` or null without a
    // device-pixel box, and `css` holds nothing until `measured`.
    const deviceBox = { inlineSize: 0, blockSize: 0 };
    let device: typeof deviceBox | null = null;
    const css = { width: 0, height: 0 };
    let measured = false;
    // The device pixel ratio the slot was last reported at; the one a fresh report is awaited
    // at, or 0 with none awaited; and the frames it has been waited for (tick). Any report, a
    // ratio back at the reported one and a lost context end the wait, so a ratio visited again
    // is observed afresh and waits its frames again (Codex round 28).
    let reportedAt = 0;
    let askedAt = 0;
    let waited = 0;
    // Size, filter and clip the canvas to the frame the renderer shows, and size it to nothing
    // while it shows none. This is the only thing that changes which canvas pixels are visible: the
    // canvas has its own size, the slot does not clip it (player-deferred.css), and a frame
    // keeps its size and its texels in view from its start until it is black. A slot that
    // shrinks therefore crops nothing, and one that grows again brings nothing back, whether or
    // not an observer ever saw the smaller size (Codex round 29): the frame fades where it
    // stands, over the slot's neighbours if the slot is now smaller, and the next one starts at
    // the slot's size. The frame is one object from its start to its end, so a frame that
    // changes nothing writes no style.
    let fitted: Renderer["shown"] | undefined;
    let fittedAt = 0;
    const fitCanvas = (renderer: Renderer) => {
      const shown = renderer.shown;
      const r = window.devicePixelRatio > 0 ? window.devicePixelRatio : 1;
      if (shown === fitted && r === fittedAt) return;
      fitted = shown;
      fittedAt = r;
      if (!shown) {
        // No frame: the canvas is black, as the slot behind it is, and takes no room, so the
        // last frame's size overflows nothing while the next one waits.
        canvas.style.width = "0px";
        canvas.style.height = "0px";
        return;
      }
      const texel = shown.texel;
      canvas.style.imageRendering = texel > 1 ? "pixelated" : "";
      canvas.style.width = `${(shown.width * texel) / r}px`;
      canvas.style.height = `${(shown.height * texel) / r}px`;
      canvas.style.clipPath = viewClip(shown, r);
    };
    // A slot with no pixels whose canvas is not rendered (the player hidden or docked) shows
    // nothing of the frame, so the frame is dropped, not faded: a fade would hold it, with the
    // clip it started with, and show it again when the slot has pixels before the limiter
    // granted the fall (Codex round 30). The renderer then starts as at a mount: black, two
    // draws asking for one geometry, every budget spent. A canvas still rendered can overflow
    // a collapsed slot and show its frame, which a drop would turn black in one paint, so that
    // frame fades like any other (Codex round 31). Returns whether the frame was dropped.
    const dropEmpty = (request: Request) => {
      if (request[3] >= 1 && request[4] >= 1) return false;
      if (canvas.getClientRects().length > 0) return false;
      const renderer = rendererRef.current;
      if (!renderer) return true;
      try {
        renderer.drop();
        fitCanvas(renderer);
      } catch {
        // A lost context shows nothing to drop; its restore starts the limiter afresh.
      }
      return true;
    };
    const observer = new ResizeObserver((entries) => {
      const entry = entries[entries.length - 1];
      if (!entry) return;
      const box = entry.devicePixelContentBoxSize?.[0];
      if (box) {
        deviceBox.inlineSize = box.inlineSize;
        deviceBox.blockSize = box.blockSize;
      }
      device = box ? deviceBox : null;
      css.width = entry.contentRect.width;
      css.height = entry.contentRect.height;
      measured = true;
      reportedAt = window.devicePixelRatio;
      askedAt = 0;
      measure(seen, device, css.width, css.height, window.devicePixelRatio);
      // A hidden slot shows nothing of the frame: it goes now, before the slot can have pixels
      // again (dropEmpty).
      if (dropEmpty(seen)) {
        unpainted = false;
        return;
      }
      // Observers run after animation frames and before the paint: a slot resized since this
      // frame's draw would show a frame judged against the old area limit, so it is withdrawn
      // and the next draw starts the fade from the frame on screen (Codex round 18).
      if (!unpainted) return;
      unpainted = false;
      // The comparison is of the geometry the renderer shows, not of the slot's pixels: a slot
      // that gains or loses a pixel no whole texel fills is the same frame, and withdrawing for
      // it would stop the limiter's clock for as long as the slot jitters (Codex round 26).
      if (sameFrame(seen, drawn)) return;
      const renderer = rendererRef.current;
      if (!renderer) return;
      try {
        renderer.withdraw();
        // A withdrawn start leaves no frame: the canvas it was sized for shows nothing.
        fitCanvas(renderer);
      } catch {
        // A lost context shows nothing to withdraw; its restore starts the limiter afresh.
      }
    });
    // An observer reports a target when it is first observed, before the next paint. Observing
    // one already observed with the same box is a no-op in Chromium, so a fresh report takes
    // an unobserve first (Codex round 27).
    const watch = () => {
      observer.unobserve(slot);
      try {
        observer.observe(slot, { box: "device-pixel-content-box" });
      } catch {
        observer.observe(slot);
      }
    };
    watch();
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
      setShownVisual(next.id);
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
      unpainted = false;
      const renderer = rendererRef.current;
      const video = videoRef.current;
      if (!renderer || !video) return;
      // No frame is painted while the page is hidden, so the time it is hidden is no interval.
      if (document.hidden) {
        restartClocks();
        return;
      }
      // Every animation-frame interval bounds the lag of the paint before it, whether this frame
      // draws or not, so a pause or a wait is not taken for one long interval when it ends
      // (Codex round 3).
      const ticked = lastTick;
      if (ticked !== null) recordLag(lags, now - ticked, now);
      lastTick = now;
      // A report made at another device pixel ratio does not measure the slot at this one: CSS
      // pixels times the new ratio is a guess, and a fade or a clip from a guessed slot can cut a
      // lit frame with no grant. The frame on screen is held as it is, refitted to the same
      // physical pixels, and the slot is observed afresh; the report comes before the paint, so
      // the next frame draws from a measurement (Codex round 26). A report that has not come in
      // REPORT_FRAMES frames is not coming, and the observer reports every change to the
      // device-pixel box, so that box is still the slot's: drawing goes on from it, a
      // measurement, with the CSS size read from layout (Codex round 27).
      const ratio = window.devicePixelRatio;
      if (!measured || ratio === reportedAt) {
        askedAt = 0;
      } else {
        if (ratio !== askedAt) {
          askedAt = ratio;
          waited = 0;
          watch();
        }
        if (++waited <= REPORT_FRAMES) {
          fitCanvas(renderer);
          return;
        }
        // A slot with no size is the one case a fresh observation does not report, and the box
        // kept may be from before it emptied.
        const rect = slot.getBoundingClientRect();
        css.width = rect.width;
        css.height = rect.height;
        measured = true;
        if (!(rect.width > 0 && rect.height > 0)) device = null;
        reportedAt = ratio;
        askedAt = 0;
      }
      const box = measured ? css : slot.getBoundingClientRect();
      measure(drawn, device, box.width, box.height, window.devicePixelRatio);
      // Nothing is drawn for a hidden slot, and the time it is hidden is no frame interval: the
      // next draw is timed as a first one.
      if (dropEmpty(drawn)) {
        lastDrawn = null;
        lastWall = null;
        return;
      }
      const width = drawn[0];
      const height = drawn[1];
      const texel = drawn[2];
      const slotWidth = drawn[3];
      const slotHeight = drawn[4];
      try {
        // Paused by the listener: hold still. Paused while a new source loads: keep moving. A
        // geometry change still draws while paused, through its fade and black, until the
        // renderer settles. Refitting the held frame accounts for a DPR change even when the
        // physical frame stays the same.
        if (
          video.paused &&
          video.readyState >= HAVE_FUTURE_DATA &&
          renderer.settled(width, height, texel, slotWidth, slotHeight)
        ) {
          // The held frame is a step behind once playback resumes: the first tick after draws.
          due = STEP_MS;
          fitCanvas(renderer);
          return;
        }
        due = lastDrawn === null || ticked === null ? 0 : Math.min(due + now - ticked, 2 * STEP_MS);
        if (lastDrawn !== null && due < STEP_MS - STEP_SLACK_MS) return;
        if (lastDrawn !== null) due = Math.max(0, due - STEP_MS);
        const frameMs = lastDrawn === null ? 1000 / 60 : now - lastDrawn;
        const dt = Math.min(MAX_DT, frameMs / 1000);
        // The limiter is credited with the wall-clock time between draws and the bound on a
        // paint's lateness, the largest recent refreshes; the renderer turns them into time it cannot overstate
        // (CORE-2744). A per-frame lower bound (half of each interval at a steady cadence) ran
        // its clock at half speed and held ragged areas of every moving visual.
        const wall = performance.now();
        // The first draw, or the first after a break, credits no time: nothing measured was shown
        // before it (Codex round 5 of CORE-2744).
        const shownMs = lastWall === null ? 0 : Math.max(0, wall - lastWall);
        const [lagMs, recentLagMs] = lagBounds(lags, now);
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
          lagMs,
          recentLagMs,
        );
        lastDrawn = now;
        lastWall = wall;
        unpainted = true;
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
          setShownVisual(first.id);
          return renderer.use(first).then(() => {
            if (!current()) return;
            restartClocks();
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
      unpainted = false;
      askedAt = 0;
      // There is no frame until the restore: the canvas takes no room and has no clip, so the
      // lost frame's box overflows nothing however the page is resized meanwhile (Codex round
      // 30). The restored renderer's first frame fits it again.
      fitted = undefined;
      canvas.style.width = "0px";
      canvas.style.height = "0px";
      canvas.style.clipPath = "";
      const old = rendererRef.current;
      rendererRef.current = null;
      try {
        old?.dispose();
      } catch {
        // Deleting objects on a lost context is a no-op or throws; either way it is gone.
      }
    };
    const restored = () => void start();
    // Browsers usually stop animation frames while the page is hidden, so the tick cannot see
    // the hide itself: the first interval after it would span the whole time hidden.
    const visibility = () => restartClocks();
    canvas.addEventListener("webglcontextlost", lost);
    canvas.addEventListener("webglcontextrestored", restored);
    document.addEventListener("visibilitychange", visibility);
    void start();
    return () => {
      alive = false;
      cancelAnimationFrame(frame);
      observer.disconnect();
      canvas.removeEventListener("webglcontextlost", lost);
      canvas.removeEventListener("webglcontextrestored", restored);
      document.removeEventListener("visibilitychange", visibility);
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
    setShownVisual(visual.id);
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
