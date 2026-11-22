// The WebGL2 host for Oscillux's calm photo-slideshow pass (CORE-2980). Host glue only: a canvas
// over the slideshow stage, photo decoding and upload, the clock and the playlist's layout and
// style choice. The picture is Oscillux's: the vendored slideshow pass draws the slides in linear
// light and the vendored present pass tone maps and encodes them (ADR-0228). The DOM slide stays
// underneath for its alt text and load events, so dropping the canvas is a hard cut.

import { loadOscillux, type Oscillux, shaderSource } from "../../viewer/player/visualizer/oscillux";
import { type FeedbackFormat, pickFeedbackFormat } from "../../viewer/player/visualizer/renderer";
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
const MAX_CANVAS_SIDE = 8192;

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

interface Targets {
  width: number;
  height: number;
  scene: WebGLTexture;
  sceneFbo: WebGLFramebuffer;
  frame: WebGLTexture;
  history: { tex: WebGLTexture; fbo: WebGLFramebuffer }[];
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

  constructor(
    private readonly canvas: HTMLCanvasElement,
    private readonly gl: WebGL2RenderingContext,
    private readonly options: SlideshowDriverOptions,
  ) {
    canvas.className = MOTION_CANVAS_CLASS;
    canvas.setAttribute("aria-hidden", "true");
    canvas.hidden = true;
    canvas.addEventListener("webglcontextlost", this.lose);
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
    this.canvas.remove();
  }

  private photoSlide(slide: HTMLElement): boolean {
    return !slide.querySelector("video") && !!slide.querySelector("img.bf-slideshow-media");
  }

  private async build(): Promise<void> {
    const [osc, vertex, slideshowSource, presentSource] = await Promise.all([
      loadOscillux(),
      shaderSource("visuals/vertex.web.vert"),
      shaderSource("slideshow/slideshow.frag"),
      shaderSource("post/present.frag"),
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
    this.gpu = { osc, overlay, regions, slideshow, present, quad, blockUbo, presentUbo };
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
    this.canvas.hidden = false;
    if (!this.frameId) {
      this.lastFrame = now;
      this.frameId = requestAnimationFrame(this.frame);
    }
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
    const gpu = this.gpu;
    if (this.dead || !gpu || !this.current || this.gl.isContextLost()) return;
    // The slide left the page without a change (a revalidation, say): so does its photo.
    if (!this.slideEl?.isConnected) {
      this.clear();
      return;
    }
    const dt = Math.max(0, now - this.lastFrame);
    this.lastFrame = now;
    try {
      const t = this.resize();
      if (t) this.draw(gpu, t, now, dt);
    } catch (error) {
      console.warn("slideshow: Oscillux pass failed", error);
      this.stop();
      return;
    }
    this.frameId = requestAnimationFrame(this.frame);
  };

  private draw(
    gpu: NonNullable<SlideshowDriver["gpu"]>,
    t: Targets,
    now: number,
    dt: number,
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
    t.history.reverse();

    // 3. The frame to the canvas.
    gl.bindFramebuffer(gl.DRAW_FRAMEBUFFER, null);
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, next.fbo);
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

  private clock(now: number): number {
    return (this.pausedAt ?? now) - this.pausedMs;
  }

  private frameAspect(): number {
    const r = this.canvas.parentElement?.getBoundingClientRect();
    return r && r.width > 0 && r.height > 0 ? r.width / r.height : 16 / 9;
  }

  // Keeps the canvas at the stage's size in device pixels, with targets to match.
  private resize(): Targets | null {
    const gl = this.gl;
    const r = this.canvas.getBoundingClientRect();
    const dpr = window.devicePixelRatio || 1;
    const max = Math.min(MAX_CANVAS_SIDE, gl.getParameter(gl.MAX_TEXTURE_SIZE) as number);
    const width = Math.min(max, Math.round(r.width * dpr));
    const height = Math.min(max, Math.round(r.height * dpr));
    if (width < 1 || height < 1) return null;
    const t = this.targets;
    if (t && t.width === width && t.height === height) return t;
    if (t) this.freeTargets(t);
    this.canvas.width = width;
    this.canvas.height = height;
    const { internal, type } = this.format;
    const scene = texture(gl, width, height, internal, type, gl.LINEAR);
    const frame = texture(gl, width, height, gl.RGBA8, gl.UNSIGNED_BYTE, gl.NEAREST);
    const history = [0, 1].map(() => {
      const tex = texture(gl, width, height, internal, type, gl.NEAREST);
      const fbo = framebuffer(gl, [frame, tex]);
      // No history yet: the present pass reads w = 0 and damps nothing on the first frame.
      gl.clearBufferfv(gl.COLOR, 1, [0, 0, 0, 0]);
      return { tex, fbo };
    });
    const next = { width, height, scene, sceneFbo: framebuffer(gl, [scene]), frame, history };
    this.targets = next;
    return next;
  }

  private freeTargets(t: Targets): void {
    const gl = this.gl;
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
    if (this.targets) this.freeTargets(this.targets);
    if (gpu) {
      const gl = this.gl;
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

function framebuffer(gl: WebGL2RenderingContext, attachments: WebGLTexture[]): WebGLFramebuffer {
  const f = must(gl.createFramebuffer());
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
