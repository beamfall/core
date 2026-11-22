// The WebGL2 host for Oscillux's calm photo-slideshow pass (CORE-2980). Host glue only: a canvas
// over the slideshow stage, photo decoding and upload, the clock and the playlist's layout and
// style choice. The picture is Oscillux's: the vendored slideshow pass draws the slides in linear
// light, the vendored present pass tone maps and encodes them, and the vendored frame flash
// limiter judges every frame before the canvas shows it, from the first photo on (ADR-0228; the
// slideshow pass README: the limiter sees the whole slideshow). The DOM slide stays underneath for
// its alt text and load events, so dropping the canvas is a hard cut.

import {
  BLACK_CREDIT_MS,
  BLACK_S,
  CREDIT_MS,
  creditScale,
  FADE_S,
  FrameLimiter,
  type Limiter,
  lagBounds,
  lagHistory,
  limiterSources,
  limiterWindow,
  linkLimiter,
  recordLag,
} from "../../viewer/player/visualizer/limiter";
import { loadOscillux, type Oscillux, shaderSource } from "../../viewer/player/visualizer/oscillux";
import {
  type FeedbackFormat,
  frameSize,
  pickFeedbackFormat,
} from "../../viewer/player/visualizer/renderer";
import {
  chooseLayout,
  MIN_TRANSITION_GAP_MS,
  packSlideshowBlock,
  SLIDESHOW_BLOCK_FLOATS,
  type SlideFields,
  type SlideshowLayout,
  type SlideshowStyle,
  slideProgress,
  transitionFor,
  transitionProgress,
} from "./slideshowPass";

/** The motion canvas's class (slideshow.css stacks it over the DOM slide). */
export const MOTION_CANVAS_CLASS = "bf-slideshow-motion";

// The present pass's block (post/present.frag): present0, the 320-byte overlay block that
// oscillux_overlay_pack writes, post0 and post1.
const PRESENT_FLOATS = 92;
const OVERLAY_FLOAT = 4;
const OVERLAY_BYTES = 320;
const POST_FLOAT = 84;
// The longest photo side uploaded: past this the photo is scaled down first (README: a photo
// much larger than the frame aliases).
const MAX_PHOTO_SIDE = 4096;

interface Photo {
  tex: WebGLTexture;
  aspect: number;
}

interface Slide {
  photos: Photo[];
  layout: SlideshowLayout;
  direction: number;
  /** When its incoming transition started, on the slide clock (paused while the page is): k = 0. */
  enteredAt: number;
  /** Its time on screen, interval plus incoming transition: k runs 0..1 over it. */
  spanMs: number;
}

interface Change {
  slide: Slide;
  style: SlideshowStyle;
  durationMs: number;
  /** In page time: a transition runs to its end while the page is paused. */
  startedAt: number;
  /** Whether it started under Reduce Motion. */
  reduced: boolean;
}

/** One frame size's targets: the frame is the present pass's RGBA8 output, the limiter's input. */
interface Targets {
  width: number;
  height: number;
  scene: WebGLTexture;
  sceneFbo: WebGLFramebuffer;
  frame: WebGLTexture;
  history: { tex: WebGLTexture; fbo: WebGLFramebuffer }[];
  limiter: Limiter;
}

/** The frame size for the canvas's box: its size in texels and a texel's side in device pixels. */
interface Want {
  width: number;
  height: number;
  texel: number;
}

export interface SlideshowDriverOptions {
  /** The slide interval in milliseconds, read at each change. */
  intervalMs: () => number;
  /** Whether Reduce Motion is on, read at each change and frame. */
  reducedMotion: () => boolean;
}

/** Whether this browser can make a WebGL2 context, probed once. */
let webgl2: boolean | undefined;
export function webgl2Available(): boolean {
  if (webgl2 === undefined) {
    try {
      const gl = document.createElement("canvas").getContext("webgl2");
      webgl2 = !!gl;
      gl?.getExtension("WEBGL_lose_context")?.loseContext();
    } catch {
      webgl2 = false;
    }
  }
  return webgl2;
}

/** The calm driver, or null without WebGL2 (the page then hard-cuts). */
export function createSlideshowDriver(options: SlideshowDriverOptions) {
  const canvas = document.createElement("canvas");
  const gl = canvas.getContext("webgl2", {
    alpha: false,
    antialias: false,
    depth: false,
    stencil: false,
    premultipliedAlpha: false,
    preserveDrawingBuffer: false,
    failIfMajorPerformanceCaveat: true,
  });
  if (!gl) return null;
  return new SlideshowDriver(canvas, gl, options);
}

export class SlideshowDriver {
  private readonly block = new Float32Array(SLIDESHOW_BLOCK_FLOATS);
  private readonly presentBlock = new Float32Array(PRESENT_FLOATS);
  private readonly format: FeedbackFormat;
  private gpu: {
    osc: Oscillux;
    overlay: InstanceType<Oscillux["OverlayConfig"]>;
    regions: InstanceType<Oscillux["OverlayRegions"]>;
    slideshow: WebGLProgram;
    present: WebGLProgram;
    quad: WebGLBuffer;
    blockUbo: WebGLBuffer;
    presentUbo: WebGLBuffer;
    limiterPrograms: WebGLProgram[];
    flash: FrameLimiter;
  } | null = null;
  private targets: Targets | null = null;
  private current: Slide | null = null;
  private change: Change | null = null;
  private generation = 0;
  private changes = 0;
  private lastStart = Number.NEGATIVE_INFINITY;
  private frameId = 0;
  private lastFrame = 0;
  private presented = 0;
  private watch: MutationObserver | null = null;
  // The slide element the canvas covers: once it leaves the page, so does the canvas.
  private slideEl: HTMLElement | null = null;
  // The slide clock: page time less the time spent paused, frozen while paused.
  private pausedAt: number | null = null;
  private pausedMs = 0;
  private readonly ready: Promise<void>;
  private dead = false;
  // The flash limiter's timing, as the visualizer keeps it (Visualizer.tsx): the recent
  // animation-frame intervals bound the paint lag, and the first frame after a start or a break
  // in painting credits no time. A geometry change fades the frame on screen to black through the
  // limiter for FADE_S; every start follows BLACK_S of black on the canvas.
  private readonly lags = lagHistory();
  private lastTick: number | null = null;
  private lastShown: number | null = null;
  private fadeLeft = 0;
  private blackMs = 0;
  private blackLongestMs = 0;
  private blackShown = false;

  constructor(
    private readonly canvas: HTMLCanvasElement,
    private readonly gl: WebGL2RenderingContext,
    private readonly options: SlideshowDriverOptions,
  ) {
    canvas.className = MOTION_CANVAS_CLASS;
    canvas.setAttribute("aria-hidden", "true");
    canvas.hidden = true;
    canvas.addEventListener("webglcontextlost", this.lose);
    document.addEventListener("visibilitychange", this.restartClocks);
    this.format = pickFeedbackFormat(gl);
    this.ready = this.build().catch((error: unknown) => {
      console.warn("slideshow: Oscillux pass unavailable", error);
      this.stop();
    });
  }

  /** Holds the Ken Burns move while the page is paused; a running transition still ends. */
  setRunning(running: boolean): void {
    const now = performance.now();
    if (running && this.pausedAt !== null) {
      this.pausedMs += now - this.pausedAt;
      this.pausedAt = null;
    } else if (!running && this.pausedAt === null) {
      this.pausedAt = now;
    }
  }

  advance(_from: HTMLElement | null, to: HTMLElement): void {
    if (this.dead) return;
    const generation = ++this.generation;
    this.slideEl = to;
    const stage = to.parentElement;
    if (stage && this.canvas.parentElement !== stage) stage.append(this.canvas);
    this.watch?.disconnect();
    this.watch = null;
    // A clip plays in the DOM: the canvas steps aside, and the next photo cuts in.
    if (!this.photoSlide(to)) {
      this.clear();
      return;
    }
    // A photo after the DOM (the first, or one after a clip): the canvas covers the stage in black
    // before the slide paints, and stays black for BLACK_S while the photo decodes, so the
    // limiter's first frame rises from black that was on screen.
    if (this.canvas.hidden) {
      this.canvas.hidden = false;
      this.presentBlack();
      this.loop(performance.now());
    }
    // A preview clip can replace the poster after the slide mounted.
    this.watch = new MutationObserver(() => {
      if (generation === this.generation && !this.photoSlide(to)) this.clear();
    });
    this.watch.observe(to, { childList: true, subtree: true });
    const images = [...to.querySelectorAll<HTMLImageElement>("img.bf-slideshow-media")];
    // The pass may still be building: a photo decoded first waits for it.
    void Promise.all([this.ready, ...images.map(decoded)]).then(
      () => {
        if (generation !== this.generation || this.dead || !this.gpu) return;
        if (!to.isConnected || !this.photoSlide(to)) return;
        this.enter(images);
      },
      // A broken photo: the DOM slide shows until the page skips it.
      () => {
        if (generation === this.generation) this.clear();
      },
    );
  }

  dispose(): void {
    this.stop();
    this.canvas.removeEventListener("webglcontextlost", this.lose);
    document.removeEventListener("visibilitychange", this.restartClocks);
    this.canvas.remove();
  }

  private photoSlide(slide: HTMLElement): boolean {
    return !slide.querySelector("video") && !!slide.querySelector("img.bf-slideshow-media");
  }

  // Builds the passes. Any failure, the limiter's included, gives up to hard cuts: no frame is
  // ever shown without it.
  private async build(): Promise<void> {
    const [osc, vertex, slideshowSource, presentSource, limiterSource] = await Promise.all([
      loadOscillux(),
      shaderSource("visuals/vertex.web.vert"),
      shaderSource("slideshow/slideshow.frag"),
      shaderSource("post/present.frag"),
      limiterSources(),
    ]);
    if (this.dead) return;
    const gl = this.gl;
    const slideshow = this.program(vertex, slideshowSource, [
      "uPhoto0",
      "uPhoto1",
      "uPhoto2",
      "uPhoto3",
    ]);
    gl.uniformBlockBinding(slideshow, gl.getUniformBlockIndex(slideshow, "SlideshowBlock"), 0);
    const present = this.program(vertex, presentSource, ["uA", "uB", "uH", "uBloom"]);
    gl.uniformBlockBinding(present, gl.getUniformBlockIndex(present, "PresentBlock"), 0);
    const limiterPrograms = linkLimiter(gl, limiterSource, (v, f) => this.program(v, f, []));
    const flash = new FrameLimiter(gl, limiterPrograms);
    const quad = must(gl.createBuffer());
    gl.bindBuffer(gl.ARRAY_BUFFER, quad);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW);
    gl.enableVertexAttribArray(0);
    gl.vertexAttribPointer(0, 2, gl.FLOAT, false, 0, 0);
    const blockUbo = ubo(gl, this.block.byteLength);
    const presentUbo = ubo(gl, this.presentBlock.byteLength);
    const overlay = new osc.OverlayConfig();
    overlay.outputLevels = 255;
    overlay.intensity = 1;
    overlay.alpha = 1;
    overlay.contrastRatio = 4.5;
    overlay.premultipliedAlpha = false;
    const regions = new osc.OverlayRegions();
    this.gpu = {
      osc,
      overlay,
      regions,
      slideshow,
      present,
      quad,
      blockUbo,
      presentUbo,
      limiterPrograms,
      flash,
    };
  }

  private program(vertex: string, fragment: string, samplers: string[]): WebGLProgram {
    const gl = this.gl;
    const p = must(gl.createProgram());
    for (const [type, source] of [
      [gl.VERTEX_SHADER, vertex],
      [gl.FRAGMENT_SHADER, fragment],
    ] as const) {
      const s = must(gl.createShader(type));
      gl.shaderSource(s, source);
      gl.compileShader(s);
      gl.attachShader(p, s);
      gl.deleteShader(s);
    }
    gl.bindAttribLocation(p, 0, "aPos");
    gl.linkProgram(p);
    if (!gl.getProgramParameter(p, gl.LINK_STATUS)) {
      throw new Error(`slideshow: link ${gl.getProgramInfoLog(p) ?? ""}`);
    }
    gl.useProgram(p);
    samplers.forEach((name, unit) => gl.uniform1i(gl.getUniformLocation(p, name), unit));
    return p;
  }

  // The new slide's photos are decoded: upload them and start its transition (or cut it in).
  private enter(images: HTMLImageElement[]): void {
    const now = performance.now();
    const clock = this.clock(now);
    const frame = this.frameAspect();
    let photos: Photo[];
    try {
      photos = images.map((image) => this.upload(image));
    } catch (error) {
      // A photo the page may show but WebGL may not read (a cross-origin URL): cut from here on.
      console.warn("slideshow: photo upload", error);
      this.stop();
      return;
    }
    const reduced = this.options.reducedMotion();
    const transition = transitionFor(this.changes, reduced);
    const slide: Slide = {
      photos,
      layout: chooseLayout(
        photos.map((photo) => photo.aspect),
        frame,
      ),
      direction: this.changes % 2,
      enteredAt: clock,
      spanMs: this.options.intervalMs() + transition.durationMs,
    };
    this.changes += 1;
    if (this.change) this.finishChange();
    if (!this.current || now - this.lastStart < MIN_TRANSITION_GAP_MS) {
      if (this.current) this.drop(this.current);
      this.current = slide;
    } else {
      this.change = { slide, ...transition, startedAt: now, reduced };
      this.lastStart = now;
    }
    this.loop(now);
  }

  // Keeps the animation frames running while the canvas is shown.
  private loop(now: number): void {
    if (this.frameId || this.canvas.hidden) return;
    this.lastFrame = now;
    this.frameId = requestAnimationFrame(this.frame);
  }

  private upload(image: HTMLImageElement): Photo {
    const gl = this.gl;
    const w = image.naturalWidth;
    const h = image.naturalHeight;
    const limit = Math.min(MAX_PHOTO_SIDE, gl.getParameter(gl.MAX_TEXTURE_SIZE) as number);
    const scale = Math.min(1, limit / Math.max(w, h));
    let source: TexImageSource = image;
    if (scale < 1) {
      const c = document.createElement("canvas");
      c.width = Math.max(1, Math.round(w * scale));
      c.height = Math.max(1, Math.round(h * scale));
      const ctx = c.getContext("2d");
      if (!ctx) throw new Error("slideshow: no 2d context to scale a photo");
      ctx.imageSmoothingQuality = "high";
      ctx.drawImage(image, 0, 0, c.width, c.height);
      source = c;
    }
    // Errors raised before this upload are not its own.
    let stale = gl.getError();
    while (stale !== gl.NO_ERROR && stale !== gl.CONTEXT_LOST_WEBGL) stale = gl.getError();
    const tex = must(gl.createTexture());
    gl.bindTexture(gl.TEXTURE_2D, tex);
    // Row 0 of the image is the top row: photo uv v = 0 (README, "uv").
    gl.pixelStorei(gl.UNPACK_FLIP_Y_WEBGL, false);
    gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, false);
    // sRGB storage: the pass samples linear light.
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.SRGB8_ALPHA8, gl.RGBA, gl.UNSIGNED_BYTE, source);
    gl.generateMipmap(gl.TEXTURE_2D);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR_MIPMAP_LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    // Out of memory is reported through the error state, not thrown.
    const error = gl.getError();
    if (error !== gl.NO_ERROR) {
      gl.deleteTexture(tex);
      throw new Error(`slideshow: photo upload GL error ${error}`);
    }
    return { tex, aspect: w / h };
  }

  private finishChange(): void {
    const change = this.change;
    if (!change) return;
    if (this.current) this.drop(this.current);
    this.current = change.slide;
    this.change = null;
  }

  private readonly frame = (now: number): void => {
    this.frameId = 0;
    if (this.dead || this.canvas.hidden || this.gl.isContextLost()) return;
    // The slide left the page without a change (a revalidation, say): so does its photo.
    if (!this.slideEl?.isConnected) {
      this.clear();
      return;
    }
    this.frameId = requestAnimationFrame(this.frame);
    const dt = Math.max(0, now - this.lastFrame);
    this.lastFrame = now;
    // No frame is painted while the page is hidden, so the time it is hidden is no interval.
    if (document.hidden) {
      this.restartClocks();
      return;
    }
    // Every animation-frame interval bounds the lag of the paint before it (Visualizer.tsx).
    if (this.lastTick !== null) recordLag(this.lags, now - this.lastTick, now);
    this.lastTick = now;
    const shownMs = this.lastShown === null ? 0 : Math.max(0, now - this.lastShown);
    this.lastShown = now;
    const [lagMs, recentLagMs] = lagBounds(this.lags, now);
    try {
      this.render(now, dt, shownMs, lagMs, recentLagMs);
    } catch (error) {
      console.warn("slideshow: Oscillux pass failed", error);
      this.stop();
    }
  };

  // After a break in painting the next frame is timed as a first one: no interval spans the
  // break, and the limiter is credited none of it.
  private readonly restartClocks = (): void => {
    this.lastTick = null;
    this.lastShown = null;
  };

  // One frame on the canvas: black before a start, else the slideshow and present passes, then the
  // limiter, whose output alone reaches the canvas. A change of frame size fades the frame on
  // screen to black through the limiter first, as the visualizer does (renderer.ts draw).
  private render(
    now: number,
    dt: number,
    shownMs: number,
    lagMs: number,
    recentLagMs: number,
  ): void {
    const gpu = this.gpu;
    const scale = creditScale(lagMs, recentLagMs);
    const want = this.measure();
    let t = this.targets;
    if (t && gpu && this.fadeLeft > 0) {
      this.fadeLeft = Math.max(0, this.fadeLeft - Math.min(shownMs * scale, CREDIT_MS) / 1000);
      if (this.fadeLeft === 0) {
        this.freeTargets(t);
        this.resetBlack();
        t = null;
      }
    } else if (t && gpu && !(want && want.width === t.width && want.height === t.height)) {
      // From this frame the limiter is fed black, its budgets written spent and its area limit
      // zero, so every fall to black waits for a grant.
      this.fadeLeft = FADE_S;
      gpu.flash.spend(t.limiter);
      t.limiter.window = { ...t.limiter.window, limit: 0 };
    }
    if (!t || !gpu) {
      if (this.blackShown && !this.blackOver) {
        const ms = Math.min(shownMs * scale, BLACK_CREDIT_MS);
        this.blackMs += ms;
        this.blackLongestMs = Math.max(this.blackLongestMs, ms);
      }
      this.blackShown = true;
      if (!gpu || !want || !this.current || !this.blackOver) {
        this.presentBlack();
        return;
      }
      t = this.start(gpu, want);
      // The first frame of a start credits no time.
      shownMs = 0;
    }
    const fading = this.fadeLeft > 0;
    this.draw(gpu, t, now, dt, fading);
    gpu.flash.run(t.limiter, t.frame, t.width, t.height, shownMs, lagMs, recentLagMs);
    this.showLimited(t);
  }

  private draw(
    gpu: NonNullable<SlideshowDriver["gpu"]>,
    t: Targets,
    now: number,
    dt: number,
    fading: boolean,
  ): void {
    const gl = this.gl;
    const reduced = this.options.reducedMotion();
    let change = this.change;
    // Reduce Motion turned on mid-sweep or mid-iris: the change completes now.
    if (change && (now - change.startedAt >= change.durationMs || (reduced && !change.reduced))) {
      this.finishChange();
      change = null;
    }
    const shown = this.current;
    if (!shown) return;
    const clock = this.clock(now);
    const words = (s: Slide): SlideFields => ({
      k: slideProgress(clock - s.enteredAt, s.spanMs),
      layout: s.layout,
      direction: s.direction,
      aspect0: s.photos[0]?.aspect ?? 1,
      aspect1: s.photos[1]?.aspect ?? s.photos[0]?.aspect ?? 1,
    });
    packSlideshowBlock(
      {
        p: change ? transitionProgress(now - change.startedAt, change.durationMs) : 0,
        style: change?.style ?? "dissolve",
        aspect: t.width / t.height,
        motion: reduced ? 0 : 1,
        width: t.width,
        height: t.height,
        a: words(shown),
        b: change ? words(change.slide) : undefined,
      },
      this.block,
    );
    gl.bindBuffer(gl.ARRAY_BUFFER, gpu.quad);

    // 1. The slideshow pass into the linear scene target.
    gl.useProgram(gpu.slideshow);
    gl.bindBuffer(gl.UNIFORM_BUFFER, gpu.blockUbo);
    gl.bufferSubData(gl.UNIFORM_BUFFER, 0, this.block);
    gl.bindBufferBase(gl.UNIFORM_BUFFER, 0, gpu.blockUbo);
    const b = change?.slide ?? shown;
    const photos = [
      shown.photos[0],
      shown.photos[1] ?? shown.photos[0],
      b.photos[0],
      b.photos[1] ?? b.photos[0],
    ];
    photos.forEach((photo, unit) => bind(gl, unit, photo?.tex ?? null));
    gl.bindFramebuffer(gl.FRAMEBUFFER, t.sceneFbo);
    gl.viewport(0, 0, t.width, t.height);
    gl.drawArrays(gl.TRIANGLES, 0, 3);

    // 2. The present pass: tone map, grade and encode into the frame and the next history.
    const o = gpu.overlay;
    o.viewportWidth = t.width;
    o.viewportHeight = t.height;
    o.frameIntervalMs = Math.min(Math.max(dt, 1e-3), 1000);
    o.reducedMotion = reduced;
    const status = gpu.regions.pack(o);
    if (status !== 0) throw new Error(`slideshow: overlay ${gpu.osc.statusString(status)}`);
    const block = this.presentBlock;
    // Photos are shown as they are: saturation 1, no switch, tone mapped, no vibrance; no bloom,
    // aberration or grain, triangular dither of one output code.
    block.set([1, 1, 1, 0]);
    block.set(
      new Float32Array(gpu.osc.wasmMemory().buffer, gpu.regions.uniformsPtr(), OVERLAY_BYTES / 4),
      OVERLAY_FLOAT,
    );
    block.set([0, 0, 0, 1, this.presented++ % 16777216, 0, 0, 0], POST_FLOAT);
    gl.useProgram(gpu.present);
    gl.bindBuffer(gl.UNIFORM_BUFFER, gpu.presentUbo);
    gl.bufferSubData(gl.UNIFORM_BUFFER, 0, block);
    gl.bindBufferBase(gl.UNIFORM_BUFFER, 0, gpu.presentUbo);
    const [prev, next] = t.history as [Targets["history"][0], Targets["history"][0]];
    bind(gl, 0, t.scene);
    bind(gl, 1, t.scene);
    bind(gl, 2, prev.tex);
    bind(gl, 3, t.scene);
    gl.bindFramebuffer(gl.FRAMEBUFFER, next.fbo);
    gl.drawArrays(gl.TRIANGLES, 0, 3);
    // While fading, the limiter is fed black, so its output falls to black through its grant.
    if (fading) gl.clearBufferfv(gl.COLOR, 0, [0, 0, 0, 1]);
    gl.bindFramebuffer(gl.FRAMEBUFFER, null);
    t.history.reverse();
  }

  // The limiter's output to the canvas: never the frame it judged.
  private showLimited(t: Targets): void {
    const gl = this.gl;
    const L = t.limiter;
    gl.bindFramebuffer(gl.DRAW_FRAMEBUFFER, null);
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, L.fOut[L.cur] as WebGLFramebuffer);
    gl.readBuffer(gl.COLOR_ATTACHMENT0);
    gl.blitFramebuffer(
      0,
      0,
      t.width,
      t.height,
      0,
      0,
      t.width,
      t.height,
      gl.COLOR_BUFFER_BIT,
      gl.NEAREST,
    );
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, null);
  }

  // Black to the canvas: all it shows with no frame on it.
  private presentBlack(): void {
    if (this.gl.isContextLost()) return;
    const gl = this.gl;
    gl.bindFramebuffer(gl.FRAMEBUFFER, null);
    gl.clearColor(0, 0, 0, 1);
    gl.clear(gl.COLOR_BUFFER_BIT);
  }

  // Whether the black before a start has been credited long enough (BLACK_S).
  private get blackOver(): boolean {
    return this.blackMs - this.blackLongestMs >= BLACK_S * 1000 - 1e-3;
  }

  private resetBlack(): void {
    this.blackMs = 0;
    this.blackLongestMs = 0;
    this.blackShown = false;
  }

  private clock(now: number): number {
    return (this.pausedAt ?? now) - this.pausedMs;
  }

  private frameAspect(): number {
    const r = this.canvas.parentElement?.getBoundingClientRect();
    return r && r.width > 0 && r.height > 0 ? r.width / r.height : 16 / 9;
  }

  // The frame size for the stage's size in device pixels: within the limiter's frame sizes
  // (frameSize) and the texture limit, keeping aspect. Null while the stage has no pixels.
  private measure(): Want | null {
    const gl = this.gl;
    const r = this.canvas.getBoundingClientRect();
    const dpr = window.devicePixelRatio || 1;
    const pw = Math.round(r.width * dpr);
    const ph = Math.round(r.height * dpr);
    if (pw < 1 || ph < 1) return null;
    const k = Math.min(1, (gl.getParameter(gl.MAX_TEXTURE_SIZE) as number) / Math.max(pw, ph));
    const [width, height] = frameSize(
      Math.max(1, Math.round(pw * k)),
      Math.max(1, Math.round(ph * k)),
    );
    return { width, height, texel: Math.max(1, pw / width) };
  }

  // Allocates a frame size's targets, the limiter's black with every budget spent. Whatever was
  // allocated is released if it throws or a target is incomplete, and the caller gives up.
  private start(gpu: NonNullable<SlideshowDriver["gpu"]>, want: Want): Targets {
    const gl = this.gl;
    const { width, height } = want;
    const { internal, type } = this.format;
    const textures: WebGLTexture[] = [];
    const framebuffers: WebGLFramebuffer[] = [];
    const tex = (internalFormat: number, t: number, mode: number) => {
      const x = texture(gl, width, height, internalFormat, t, mode);
      textures.push(x);
      return x;
    };
    const fbo = (attachments: WebGLTexture[]) => framebuffer(gl, attachments, framebuffers);
    try {
      this.canvas.width = width;
      this.canvas.height = height;
      const scene = tex(internal, type, gl.LINEAR);
      const frame = tex(gl.RGBA8, gl.UNSIGNED_BYTE, gl.NEAREST);
      const history = [0, 1].map(() => {
        const h = tex(internal, type, gl.NEAREST);
        const f = fbo([frame, h]);
        // No history yet: the present pass reads w = 0 and damps nothing on the first frame.
        gl.clearBufferfv(gl.COLOR, 1, [0, 0, 0, 0]);
        return { tex: h, fbo: f };
      });
      const sceneFbo = fbo([scene]);
      const field = limiterWindow(width, height, want.texel);
      const { limiter, complete } = gpu.flash.allocate(width, height, field, true);
      if (!complete) {
        gpu.flash.free(limiter);
        throw new Error("slideshow: flash limiter targets incomplete");
      }
      const next = { width, height, scene, sceneFbo, frame, history, limiter };
      this.targets = next;
      return next;
    } catch (error) {
      for (const f of framebuffers) gl.deleteFramebuffer(f);
      for (const t of textures) gl.deleteTexture(t);
      throw error;
    }
  }

  private freeTargets(t: Targets): void {
    const gl = this.gl;
    this.gpu?.flash.free(t.limiter);
    this.fadeLeft = 0;
    gl.deleteFramebuffer(t.sceneFbo);
    gl.deleteTexture(t.scene);
    gl.deleteTexture(t.frame);
    for (const h of t.history) {
      gl.deleteFramebuffer(h.fbo);
      gl.deleteTexture(h.tex);
    }
    this.targets = null;
  }

  private drop(slide: Slide): void {
    if (this.gl.isContextLost()) return;
    for (const photo of slide.photos) this.gl.deleteTexture(photo.tex);
  }

  // Back to the DOM slide: no slide on the canvas, the next photo cuts in.
  private clear(): void {
    this.generation += 1;
    if (this.change) this.drop(this.change.slide);
    if (this.current) this.drop(this.current);
    this.change = null;
    this.current = null;
    // The frame and its trackers go with the canvas: the next photo starts after BLACK_S of black.
    if (this.targets && !this.gl.isContextLost()) this.freeTargets(this.targets);
    this.targets = null;
    this.fadeLeft = 0;
    this.resetBlack();
    this.restartClocks();
    this.canvas.hidden = true;
    cancelAnimationFrame(this.frameId);
    this.frameId = 0;
  }

  private readonly lose = (event: Event): void => {
    event.preventDefault();
    this.stop();
  };

  // Gives up for good: every later change is a hard cut in the DOM.
  private stop(): void {
    if (this.dead) return;
    this.clear();
    this.dead = true;
    this.watch?.disconnect();
    this.watch = null;
    const gpu = this.gpu;
    this.gpu = null;
    gpu?.overlay.free();
    gpu?.regions.free();
    if (this.gl.isContextLost()) return;
    if (gpu) {
      const gl = this.gl;
      for (const p of gpu.limiterPrograms) gl.deleteProgram(p);
      gpu.flash.dispose();
      gl.deleteProgram(gpu.slideshow);
      gl.deleteProgram(gpu.present);
      gl.deleteBuffer(gpu.quad);
      gl.deleteBuffer(gpu.blockUbo);
      gl.deleteBuffer(gpu.presentUbo);
    }
    this.gl.getExtension("WEBGL_lose_context")?.loseContext();
  }
}

function decoded(image: HTMLImageElement): Promise<HTMLImageElement> {
  if (image.complete && image.naturalWidth > 0) return Promise.resolve(image);
  return image.decode().then(() => image);
}

function must<T>(value: T | null): T {
  if (value === null) throw new Error("slideshow: WebGL allocation failed");
  return value;
}

function ubo(gl: WebGL2RenderingContext, bytes: number): WebGLBuffer {
  const b = must(gl.createBuffer());
  gl.bindBuffer(gl.UNIFORM_BUFFER, b);
  gl.bufferData(gl.UNIFORM_BUFFER, bytes, gl.DYNAMIC_DRAW);
  return b;
}

function bind(gl: WebGL2RenderingContext, unit: number, tex: WebGLTexture | null): void {
  gl.activeTexture(gl.TEXTURE0 + unit);
  gl.bindTexture(gl.TEXTURE_2D, tex);
}

function texture(
  gl: WebGL2RenderingContext,
  w: number,
  h: number,
  internal: number,
  type: number,
  mode: number,
): WebGLTexture {
  const t = must(gl.createTexture());
  gl.bindTexture(gl.TEXTURE_2D, t);
  gl.texImage2D(gl.TEXTURE_2D, 0, internal, w, h, 0, gl.RGBA, type, null);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, mode);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, mode);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
  return t;
}

function framebuffer(
  gl: WebGL2RenderingContext,
  attachments: WebGLTexture[],
  made: WebGLFramebuffer[],
): WebGLFramebuffer {
  const f = must(gl.createFramebuffer());
  made.push(f);
  gl.bindFramebuffer(gl.FRAMEBUFFER, f);
  attachments.forEach((t, i) =>
    gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0 + i, gl.TEXTURE_2D, t, 0),
  );
  gl.drawBuffers(attachments.map((_, i) => gl.COLOR_ATTACHMENT0 + i));
  if (gl.checkFramebufferStatus(gl.FRAMEBUFFER) !== gl.FRAMEBUFFER_COMPLETE) {
    throw new Error("slideshow: incomplete framebuffer");
  }
  return f;
}
