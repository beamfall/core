// Draws one Oscillux visual on a WebGL2 canvas. A frame is, in order: the visual's pass into a
// feedback target (during a switch, the outgoing visual's pass too and Oscillux's switch pass
// blending the two), Oscillux's present pass (its overlay block packed by the wasm core) into an
// RGBA8 frame, the six passes of Oscillux's frame flash limiter, and a blit of the limited
// frame to the canvas. The pass order, bindings and limiter clock follow the release's own
// reference hosts (hosts/web/host.js and tools/shader/limiter-check.mjs at the pinned rev).
// Every shader is Oscillux's, unchanged; this file only binds and schedules them.

import type { AudioFrame } from "./analysis";
import { AUDIO_KEYS } from "./analysis";
import { CadenceGate } from "./cadence";
import type { Oscillux } from "./oscillux";
import { shaderSource } from "./oscillux";
import type { Visual } from "./visuals";

// The frame flash limiter's clock (tools/shader/limiter-model.mjs) and its WCAG 2.x reference
// field, a third of the frame each way (tools/shader/limiter-geometry.mjs).
const TICK_MS = 1000 / 15 / 64;
const TICKS = 240;
const GRID_X = 16;
const GRID_Y = 9;
const SUB = 4;
const TOKEN = 65025;
const MAX_FLASH_HZ = 3;
/** The limiter's grant state with every budget just spent: two luma tokens of deficit (bit 8 of x
 * carries one), a red token and no credit (Oscillux shaders/limiter/grant.glsl). */
const SPENT = new Uint32Array([256, TOKEN, TOKEN, 0]);
/** How long a display-scale change presents black: longer than the limiter's coverage interval,
 * 1 / (5 maxFlashHz) s (Oscillux shaders/limiter/README.md, "Ping-pong and reset"). */
export const SCALE_BLACK_S = 0.1;
/** How long a display-scale change first fades the output to black through the limiter at the old
 * size: longer than the slowest grant of a fall, the red token's refill of 8 / (2 maxFlashHz) s
 * (Oscillux shaders/limiter/README.md, "Ping-pong and reset"). */
export const SCALE_FADE_S = 2;
/** What the host shows wherever the canvas shows no judged pixel (past its edge, through its clip,
 * before its first frame): the black every tracker starts at (clearOutput), whatever the theme,
 * so a pixel that comes into view rises from what its tracker holds (Codex round 25). */
export const TRACKED_BLACK = "#000";
/** The limiter's smallest frame: one pixel per sub-block. */
export const MIN_WIDTH = 64;
export const MIN_HEIGHT = 36;
/** The largest frame side the limiter supports: its sub-block counts are 16-bit (Oscillux
 * shaders/limiter/README.md, "Frame size"). Larger canvases render scaled down, keeping aspect. */
export const MAX_SIDE = 8192;

/** The render size for a canvas of `width` x `height`: at least the minimum on each side and at
 * most MAX_SIDE on the longer one, scaled down uniformly so the frame keeps its aspect. */
/** Whether a display-scale change from `from` to `to` physical pixels per texel keeps every pixel
 * on screen: `to` is a smaller divisor of `from`. */
export function exactDrop(from: number, to: number): boolean {
  return from > 0 && to > 0 && to < from && from % to === 0;
}

export function frameSize(width: number, height: number): [number, number] {
  const k = Math.min(1, MAX_SIDE / Math.max(width, height));
  return [
    Math.min(MAX_SIDE, Math.max(MIN_WIDTH, Math.round(width * k))),
    Math.min(MAX_SIDE, Math.max(MIN_HEIGHT, Math.round(height * k))),
  ];
}

const LIMITER = ["rows", "reduce", "cells", "windows", "grant", "output"] as const;
const LIMITER_SAMPLERS: Record<(typeof LIMITER)[number], readonly string[]> = {
  rows: ["uFrame", "uTrack"],
  reduce: ["uRows"],
  cells: ["uS"],
  windows: ["uC"],
  grant: ["uC", "uW", "uG"],
  output: ["uFrame", "uHist", "uTrack", "uG"],
};

export interface LimiterWindow {
  cols: number;
  rows: number;
  limit: number;
}

// The most cells a run of `len` pixels touches along an axis of `n` pixels split into `cells`
// cells of SUB sub-blocks each.
function span(len: number, n: number, cells: number): number {
  const cellOf = (x: number) => Math.floor(Math.floor((cells * SUB * (x + 1) - 1) / n) / SUB);
  let most = 1;
  for (let s = 0; s + len <= n; s++) most = Math.max(most, cellOf(s + len - 1) - cellOf(s) + 1);
  return most;
}

/** windowOf("wcag", W, H) from tools/shader/limiter-geometry.mjs for a W x H frame whose texels
 * are `texel` physical pixels on a side: the field is a third of the visible frame each way,
 * the window the fewest whole cells that contain every texel the field touches wherever it sits,
 * and the area limit a quarter of the field's physical pixels over the texel's area, so a patch
 * over the physical limit is over it in texels (Codex round 13). */
export function limiterWindow(
  W: number,
  H: number,
  texel = 1,
  visibleWidth = W,
  visibleHeight = H,
  visiblePxWidth = visibleWidth * texel,
  visiblePxHeight = visibleHeight * texel,
): LimiterWindow {
  // The field is a third of the physical pixels on screen. A texel the slot cuts is judged whole,
  // so its area counts in full against a limit from the pixels shown: an overcount, never less.
  const fieldPx = (px: number) => Math.min(px, Math.max(1, Math.round(px / 3)));
  const touched = (px: number, n: number) =>
    Math.min(n, texel === 1 ? px : Math.ceil((px - 1) / texel) + 1);
  const fwPx = fieldPx(visiblePxWidth);
  const fhPx = fieldPx(visiblePxHeight);
  return {
    cols: span(touched(fwPx, W), W, GRID_X),
    rows: span(touched(fhPx, H), H, GRID_Y),
    limit: Math.floor((fwPx * fhPx) / 4 / (texel * texel)),
  };
}
const LIMITER_VERTEX =
  "#version 300 es\nvoid main() { vec2 p = vec2(gl_VertexID == 1 ? 3.0 : -1.0, gl_VertexID == 2 ? 3.0 : -1.0); gl_Position = vec4(p, 0.0, 1.0); }";

// The default preset's accent and motion (tools/shader/preset.mjs).
const ACCENT = [0.15, 0.9, 0.72, 1];
const MOTION = [0, 0, 0, 0];
// presentation0 for a visual drawn on its own (no switch in progress).
const PRESENTATION = [0, 1, 0, 0];
// The safe rectangle with no overlay regions tracked (hosts/web/overlay.js).
const SAFE_RECT = [0.05, 0.05, 0.95, 0.95];
// The PresentBlock: present0, the 320-byte overlay block, post0, post1.
const PRESENT_FLOATS = 92;
const OVERLAY_FLOAT = 4;
const OVERLAY_BYTES = 320;
const POST_FLOAT = 84;

// The switch between visuals (CORE-2219): Oscillux's Luma Key at full motion, focus halfway,
// over 1.2 s with smoothstep easing, timed by the wasm core's transition scheduler
// (shaders/switch/README.md at the pinned rev). The incoming visual starts at once: no pre-roll.
const SWITCH_STYLE = 0;
const SWITCH_SECONDS = 1.2;
const SWITCH_EASING_SMOOTHSTEP = 1;
const SWITCH_FOCUS = 0.5;
const TRANSITION_DONE = 4;

// The quality ladder the Oscillux governor steps through (CORE-2215), as in the reference web
// host (hosts/web/host.js at the pinned rev): the visual's render scale and its detail reduction
// (presentation1.y) per level, lowest quality first.
export const QUALITY_LADDER: readonly { scale: number; reduce: number }[] = [
  { scale: 0.5, reduce: 1 },
  { scale: 0.67, reduce: 0.6 },
  { scale: 0.83, reduce: 0.25 },
  { scale: 1, reduce: 0 },
];
// KHR_parallel_shader_compile's query, and how often a compiling visual is polled.
const COMPLETION_STATUS_KHR = 0x91b1;
const COMPILE_POLL_MS = 32;
const TOP_LEVEL = QUALITY_LADDER.length - 1;

/** The visual's render size and detail reduction for a W x H canvas at a governor level. */
export function renderQuality(
  W: number,
  H: number,
  level: number,
): { width: number; height: number; reduce: number } {
  const step = QUALITY_LADDER[Math.min(TOP_LEVEL, Math.max(0, Math.trunc(level)))];
  return {
    width: Math.max(1, Math.round(W * step.scale)),
    height: Math.max(1, Math.round(H * step.scale)),
    reduce: step.reduce,
  };
}

/**
 * What a request for a visual does: nothing ("same"), replace the visual now ("instant": the
 * first visual, before the first frame, or under reduced motion), wait for the switch in
 * progress to end ("queue"), or blend to it ("blend").
 */
export function switchAction(
  current: string | null,
  switching: boolean,
  next: string,
  reducedMotion: boolean,
): "same" | "instant" | "queue" | "blend" {
  if (switching && !reducedMotion) return "queue";
  if (current === next) return "same";
  if (current === null || reducedMotion) return "instant";
  return "blend";
}

interface Layer {
  visual: Visual;
  program: WebGLProgram;
  locations: Map<string, WebGLUniformLocation | null>;
  /** The visual's image input. */
  image: WebGLTexture;
}

interface Target {
  tex: WebGLTexture;
  fbo: WebGLFramebuffer;
}

interface Limiter {
  rows: WebGLTexture;
  s: WebGLTexture;
  c: WebGLTexture;
  w: WebGLTexture;
  g: WebGLTexture[];
  /** Per-pixel tracker state, RGBA16UI at the frame size. */
  track: WebGLTexture[];
  out: WebGLTexture[];
  fRows: WebGLFramebuffer;
  fS: WebGLFramebuffer;
  fC: WebGLFramebuffer;
  fW: WebGLFramebuffer;
  fG: WebGLFramebuffer[];
  /** The output pass's targets: out[i] and track[i]. */
  fOut: WebGLFramebuffer[];
  window: LimiterWindow;
  cur: number;
}

interface Size {
  width: number;
  height: number;
  /** The texels in view, a cut one included (all of them before a draw: they start black), and
   * the physical pixels the limiter has judged in view (none before a draw). */
  visibleWidth: number;
  visibleHeight: number;
  visiblePxWidth: number;
  visiblePxHeight: number;
  /** The visual's render size at the governor's level: its feedback, and a switch's targets. */
  renderWidth: number;
  renderHeight: number;
  /** The visual's feedback pair: [current, next]. */
  feedback: Target[];
  /** During a switch, the outgoing visual's feedback pair and the switch pass's output. */
  outgoing: Target[] | null;
  blend: Target | null;
  /** The presented frame, the limiter's input. */
  frame: WebGLTexture;
  /** The present pass's history pair, each framebuffer writing the frame and one history. */
  history: Target[];
  limiter: Limiter;
}

export type FeedbackLabel = "float" | "half-float" | "8-bit";
export interface FeedbackFormat {
  internal: number;
  type: number;
  label: FeedbackLabel;
}

/**
 * The feedback format: RGBA16F where the browser renders to it, through EXT_color_buffer_float
 * or else EXT_color_buffer_half_float, otherwise RGBA8, which clamps HDR feedback and steps
 * gradients (CORE-2481). An enabled extension does not promise a complete framebuffer, so each
 * is probed on a 1x1 attachment, as the reference host does (hosts/web/host.js feedbackFormat).
 */
export function pickFeedbackFormat(gl: WebGL2RenderingContext): FeedbackFormat {
  const candidates: [string, FeedbackLabel][] = [
    ["EXT_color_buffer_float", "float"],
    ["EXT_color_buffer_half_float", "half-float"],
  ];
  for (const [ext, label] of candidates) {
    if (!gl.getExtension(ext)) continue;
    const texture = gl.createTexture();
    const fbo = gl.createFramebuffer();
    let complete = false;
    try {
      gl.bindTexture(gl.TEXTURE_2D, texture);
      gl.texStorage2D(gl.TEXTURE_2D, 1, gl.RGBA16F, 1, 1);
      gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
      gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, texture, 0);
      complete = gl.checkFramebufferStatus(gl.FRAMEBUFFER) === gl.FRAMEBUFFER_COMPLETE;
    } finally {
      gl.bindFramebuffer(gl.FRAMEBUFFER, null);
      gl.bindTexture(gl.TEXTURE_2D, null);
      gl.deleteFramebuffer(fbo);
      gl.deleteTexture(texture);
    }
    if (complete) return { internal: gl.RGBA16F, type: gl.HALF_FLOAT, label };
  }
  return { internal: gl.RGBA8, type: gl.UNSIGNED_BYTE, label: "8-bit" };
}

/** A WebGL2 renderer for one canvas, or null where the browser cannot run it. */
export class Renderer {
  private readonly gl: WebGL2RenderingContext;
  private readonly osc: Oscillux;
  private readonly overlay: InstanceType<Oscillux["OverlayConfig"]>;
  private readonly regions: InstanceType<Oscillux["OverlayRegions"]>;
  private readonly feedbackFormat: FeedbackFormat;
  private readonly quad: WebGLBuffer;
  // The current visual's image input; during a switch the outgoing visual keeps the one it had.
  private image: WebGLTexture;
  private readonly transition: InstanceType<Oscillux["Transition"]>;
  // Oscillux's quality governor and the level the targets are built for (CORE-2215).
  private readonly governor: InstanceType<Oscillux["Governor"]>;
  private readonly cadence = new CadenceGate();
  private level = TOP_LEVEL;
  private readonly switchUbo: WebGLBuffer;
  private readonly switchBlock = new Float32Array(12);
  private readonly presentUbo: WebGLBuffer;
  private readonly limiterUbo: WebGLBuffer;
  private readonly presentBlock = new Float32Array(PRESENT_FLOATS);
  // Oscillux's vivid colour mode: present-pass saturation and vibrance (CORE-2369).
  private readonly vivid: Float64Array;
  private readonly limiterBlock = new Float32Array(8);
  private readonly grantWord = new Uint32Array(4);
  private grantSettled = true;
  private limiterSpent = false;
  // The state before the last frame drawn, until the next draw (withdraw): the fade and black
  // clocks, and the limiter's state when the frame ran it.
  private undo: {
    fadeLeft: number;
    black: number;
    blackShown: boolean;
    size: Size | null;
    limiter: { cur: number; clockMs: number; carry: number; spent: boolean } | null;
  } | null = null;
  // The fraction of a refill unit earned and not yet granted (limiter README, Codex round 15).
  private refillCarry = 0;
  private readonly visualVertex: string;
  private present: WebGLProgram;
  private switchPass: WebGLProgram;
  private limiter: WebGLProgram[];
  private readonly shaders: WebGLShader[] = [];
  // Every program linked, for dispose.
  private readonly linked: WebGLProgram[] = [];
  private disposed = false;
  private readonly programs = new Map<string, WebGLProgram>();
  // Visuals compiling now, so a second request for one waits on the same program.
  private readonly compiling = new Map<string, Promise<WebGLProgram | null>>();
  // KHR_parallel_shader_compile: a visual compiles off the page's thread and is polled for
  // completion, instead of the page waiting on its status (CORE-2368).
  private parallel = false;
  private visual: Layer | null = null;
  private outgoing: Layer | null = null;
  // The visual asked for while a switch runs, started when it ends.
  private pending: { visual: Visual; program: WebGLProgram } | null = null;
  // The visual last asked for: an older request still compiling is dropped.
  private wanted: Visual | null = null;
  private reducedMotion = false;
  private size: Size | null = null;
  // Physical pixels per frame texel at the last resize (0 before the first).
  private displayScale = 0;
  // Seconds of black left after a display-scale change or restore, counted once black is shown.
  private black = 0;
  private blackShown = false;
  // Seconds left of the fade to black before a display-scale change, or -1 outside one.
  private fadeLeft = -1;
  private time = 0;
  private clockMs = 0;
  private presented = 0;

  /** Whether the GL context is lost, which a draw can hit before the loss event arrives (CORE-2482). */
  get contextLost(): boolean {
    return this.gl.isContextLost();
  }

  /** The feedback and history format in use, for diagnostics (CORE-2481). */
  get feedbackLabel(): FeedbackLabel {
    return this.feedbackFormat.label;
  }

  /**
   * A renderer on the canvas, or null without a hardware WebGL2 context. A software one
   * (SwiftShader, say) runs every full-frame pass on the CPU and stalls the page, so it is
   * refused rather than used for decoration.
   */
  static async create(
    canvas: HTMLCanvasElement,
    osc: Oscillux,
    restored = false,
  ): Promise<Renderer | null> {
    const gl = canvas.getContext("webgl2", {
      failIfMajorPerformanceCaveat: true,
      alpha: false,
      antialias: false,
      depth: false,
      stencil: false,
      preserveDrawingBuffer: false,
    });
    if (!gl) return null;
    const [vertex, present, blend, ...limiter] = await Promise.all([
      shaderSource("visuals/vertex.web.vert"),
      shaderSource("post/present.frag"),
      shaderSource("switch/switch.frag"),
      ...LIMITER.map((name) => shaderSource(`limiter/${name}.frag`)),
    ]);
    return new Renderer(gl, osc, vertex, present, blend, limiter, restored);
  }

  private constructor(
    gl: WebGL2RenderingContext,
    osc: Oscillux,
    vertex: string,
    present: string,
    blend: string,
    limiter: string[],
    private readonly restored: boolean,
  ) {
    this.gl = gl;
    this.osc = osc;
    this.vivid = osc.vividPresent();
    // Whatever was allocated before a failure is released here, as no caller holds the
    // instance to dispose it.
    try {
      this.overlay = new osc.OverlayConfig();
      this.overlay.outputLevels = 255;
      this.overlay.intensity = 1;
      this.overlay.alpha = 1;
      this.overlay.contrastRatio = 4.5;
      this.overlay.premultipliedAlpha = false;
      this.regions = new osc.OverlayRegions();
      const config = new osc.TransitionConfig();
      try {
        config.easing = SWITCH_EASING_SMOOTHSTEP;
        config.fallbackSeconds = SWITCH_SECONDS;
        config.preRollSeconds = 0;
        config.autoAdvanceBars = 0;
        this.transition = new osc.Transition(config);
      } finally {
        config.free();
      }
      // The reference defaults, timed by the frame interval as the reference host does without
      // a GPU timer query, starting at full quality.
      const quality = new osc.GovernorConfig();
      try {
        quality.maxLevel = TOP_LEVEL;
        quality.initialLevel = TOP_LEVEL;
        this.governor = new osc.Governor(quality);
      } finally {
        quality.free();
      }
      this.parallel = !!gl.getExtension("KHR_parallel_shader_compile");
      this.feedbackFormat = pickFeedbackFormat(gl);

      this.quad = must(gl.createBuffer());
      gl.bindBuffer(gl.ARRAY_BUFFER, this.quad);
      gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW);
      gl.enableVertexAttribArray(0);
      gl.vertexAttribPointer(0, 2, gl.FLOAT, false, 0, 0);

      // The visual's image input: a neutral stand-in until setImage hands it the artwork.
      this.image = must(gl.createTexture());
      this.setImage(null);

      this.presentUbo = must(gl.createBuffer());
      gl.bindBuffer(gl.UNIFORM_BUFFER, this.presentUbo);
      gl.bufferData(gl.UNIFORM_BUFFER, this.presentBlock.byteLength, gl.DYNAMIC_DRAW);
      this.limiterUbo = must(gl.createBuffer());
      gl.bindBuffer(gl.UNIFORM_BUFFER, this.limiterUbo);
      gl.bufferData(gl.UNIFORM_BUFFER, this.limiterBlock.byteLength, gl.DYNAMIC_DRAW);
      this.switchUbo = must(gl.createBuffer());
      gl.bindBuffer(gl.UNIFORM_BUFFER, this.switchUbo);
      gl.bufferData(gl.UNIFORM_BUFFER, this.switchBlock.byteLength, gl.DYNAMIC_DRAW);

      // Offscreen passes draw into targets read back with a top-left origin, so the visual's
      // vertex shader flips y; the present pass draws in canvas orientation.
      this.visualVertex = vertex.replace(
        "gl_Position = vec4(aPos, 0.0, 1.0);",
        "gl_Position = vec4(aPos.x, -aPos.y, 0.0, 1.0);",
      );
      this.present = this.link(vertex, present);
      gl.useProgram(this.present);
      gl.uniformBlockBinding(
        this.present,
        gl.getUniformBlockIndex(this.present, "PresentBlock"),
        0,
      );
      ["uA", "uB", "uH"].forEach((name, unit) =>
        gl.uniform1i(gl.getUniformLocation(this.present, name), unit),
      );
      gl.uniform1i(gl.getUniformLocation(this.present, "uBloom"), 4);

      // The switch pass works in the offscreen targets' top-left orientation, as its uv does.
      this.switchPass = this.link(LIMITER_VERTEX, blend);
      gl.useProgram(this.switchPass);
      gl.uniformBlockBinding(
        this.switchPass,
        gl.getUniformBlockIndex(this.switchPass, "SwitchBlock"),
        0,
      );
      ["uFrom", "uTo", "uPhoto"].forEach((name, unit) =>
        gl.uniform1i(gl.getUniformLocation(this.switchPass, name), unit),
      );

      this.limiter = LIMITER.map((name, i) => {
        const p = this.link(LIMITER_VERTEX, limiter[i]);
        gl.useProgram(p);
        gl.uniformBlockBinding(p, gl.getUniformBlockIndex(p, "LimiterBlock"), 0);
        LIMITER_SAMPLERS[name].forEach((s, unit) =>
          gl.uniform1i(gl.getUniformLocation(p, s), unit),
        );
        return p;
      });
    } catch (err) {
      this.dispose();
      throw err;
    }
  }

  /**
   * Compiles a visual (once) and switches to it. The incoming visual starts from the outgoing
   * one's last frame as its feedback and the clock runs on (CORE-2218); Oscillux's switch pass
   * then blends the two over 1.2 s (CORE-2219). A request during a switch waits for it to end;
   * under reduced motion the visual changes at once.
   */
  async use(visual: Visual): Promise<void> {
    this.wanted = visual;
    if (!this.outgoing && this.visual?.visual.id === visual.id) return;
    const program = await this.prepare(visual);
    if (!program || this.wanted !== visual) return;
    this.apply(visual, program);
  }

  private apply(visual: Visual, program: WebGLProgram): void {
    const size = this.size;
    const action = switchAction(
      this.visual?.visual.id ?? null,
      !!this.outgoing,
      visual.id,
      this.reducedMotion || !size,
    );
    if (action === "queue") {
      this.pending = this.visual?.visual.id === visual.id ? null : { visual, program };
      return;
    }
    this.pending = null;
    if (action === "same") return;
    if (action === "instant") {
      this.endSwitch();
      this.visual = { visual, program, locations: new Map(), image: this.image };
      return;
    }
    // blend: the current visual goes out with its feedback and image; the incoming one gets a
    // copy of that feedback and shares the image until setImage hands it its own.
    const s = size as Size;
    const { internal, type } = this.feedbackFormat;
    const feedback = this.feedbackPair(s.renderWidth, s.renderHeight, internal, type, false);
    this.copy(
      s.feedback[0],
      s.renderWidth,
      s.renderHeight,
      feedback[0],
      s.renderWidth,
      s.renderHeight,
    );
    s.outgoing = s.feedback;
    s.feedback = feedback;
    this.outgoing = this.visual;
    this.visual = { visual, program, locations: new Map(), image: this.image };
    const status = this.transition.request(this.time, 0, 0, 0, 0, 0, 0);
    if (status !== 0) {
      console.warn("visualizer: switch", this.osc.statusString(status));
      this.endSwitch();
    }
  }

  // Drops the outgoing visual, its feedback, its image (when not shared) and the blend target.
  private endSwitch(): void {
    const out = this.outgoing;
    if (!out) return;
    this.outgoing = null;
    this.transition.reset();
    const gl = this.gl;
    if (out.image !== this.image) gl.deleteTexture(out.image);
    const s = this.size;
    if (!s) return;
    for (const t of [...(s.outgoing ?? []), ...(s.blend ? [s.blend] : [])]) {
      gl.deleteTexture(t.tex);
      gl.deleteFramebuffer(t.fbo);
    }
    s.outgoing = null;
    s.blend = null;
  }

  // The switch's eased progress this frame, or null with no switch running. A switch that has
  // ended (or that a resize cut short) is dropped here, and a visual queued behind it starts.
  private switchProgress(reducedMotion: boolean): number | null {
    if (!this.outgoing && !this.pending) return null;
    if (this.outgoing && !reducedMotion) {
      const status = this.transition.status(this.time);
      if (status === 0 && this.transition.state !== TRANSITION_DONE) {
        return this.transition.progress;
      }
    }
    this.endSwitch();
    const next = this.pending;
    this.pending = null;
    if (next) this.apply(next.visual, next.program);
    return this.outgoing ? 0 : null;
  }

  /**
   * Compiles a visual ahead of its first use, so switching to it later does not stall a frame.
   * With KHR_parallel_shader_compile the page keeps drawing while the driver compiles.
   */
  prepare(visual: Visual): Promise<WebGLProgram | null> {
    const ready = this.programs.get(visual.id);
    if (ready) return Promise.resolve(ready);
    let compiling = this.compiling.get(visual.id);
    if (!compiling) {
      compiling = this.compileVisual(visual).finally(() => this.compiling.delete(visual.id));
      this.compiling.set(visual.id, compiling);
    }
    return compiling;
  }

  private async compileVisual(visual: Visual): Promise<WebGLProgram | null> {
    const source = await shaderSource(`visuals/${visual.id}.web.frag`);
    // Disposed while the source loaded: allocate nothing more.
    if (this.disposed) return null;
    const program = this.compile(this.visualVertex, source);
    while (this.parallel && !this.disposed && !this.gl.isContextLost()) {
      if (this.gl.getProgramParameter(program, COMPLETION_STATUS_KHR)) break;
      await new Promise((resolve) => setTimeout(resolve, COMPILE_POLL_MS));
    }
    if (this.disposed) return null;
    this.check(program);
    this.programs.set(visual.id, program);
    return program;
  }

  /** Draws one frame at width x height texels, each `texel` physical pixels on a side, dt seconds
   * after the last, in a slot slotWidth x slotHeight physical pixels: more than the texels cover
   * when the slot cuts a texel. */
  draw(
    width: number,
    height: number,
    dt: number,
    audio: AudioFrame,
    reducedMotion: boolean,
    frameMs = dt * 1000,
    texel = 1,
    shownMs = frameMs,
    slotWidth = width * texel,
    slotHeight = height * texel,
  ): void {
    if (!this.visual) return;
    const gl = this.gl;
    this.reducedMotion = reducedMotion;
    // A withdrawn frame was never shown, so it takes back its fade and black time too (Codex
    // round 23).
    this.undo = {
      fadeLeft: this.fadeLeft,
      black: this.black,
      blackShown: this.blackShown,
      size: this.size,
      limiter: null,
    };
    // A display-scale change first fades the output to black at the old size (fading), then
    // switches with the limiter's state tracked black and its budgets spent (resize).
    // The fade runs on the time the limiter is credited with, so its grant has come by its end;
    // the black interval on the same time (Codex rounds 18 and 22).
    // A slot grown past a texel it cut goes the same way at its own scale: the view is held
    // through the fade and starts again from nothing in view (inView, Codex round 24).
    const reseat = this.size !== null && this.inView(this.size, slotWidth, slotHeight).held;
    const fading = this.fading(texel, shownMs / 1000, reseat);
    const size =
      fading && this.size ? this.size : this.resize(...frameSize(width, height), texel, reseat);
    const { width: W, height: H } = size;
    // The slot clips a minimum-sized backing or an old surface still fading at its own scale.
    const v = this.inView(size, slotWidth, slotHeight);
    if (v.width !== size.visibleWidth || v.height !== size.visibleHeight) {
      this.expose(size, v.width, v.height);
      size.visibleWidth = v.width;
      size.visibleHeight = v.height;
    }
    if (v.pxWidth !== size.visiblePxWidth || v.pxHeight !== size.visiblePxHeight) {
      // The area limit follows the pixels in view; a grant earned under another is not spent
      // under it (expose).
      size.visiblePxWidth = v.pxWidth;
      size.visiblePxHeight = v.pxHeight;
      size.limiter.window = limiterWindow(
        W,
        H,
        this.displayScale,
        v.width,
        v.height,
        v.pxWidth,
        v.pxHeight,
      );
      this.spend(size.limiter);
    }
    this.time += dt;
    const p = this.switchProgress(reducedMotion);
    // The governor times frames outside a switch only, and its level changes between switches,
    // as in the reference host. It reads the raw frame interval, not the clamped step, so a
    // stall or a throttled page is a pause it ignores rather than a slow frame that steps the
    // render scale down.
    // A steady browser cap at 30 Hz is not overload: the cadence gate scales it to the target
    // (CORE-2483).
    if (p === null) {
      const sample = this.cadence.sample(frameMs);
      if (sample !== null) this.governor.record(sample);
      this.setLevel(size, this.governor.level);
    }
    const layer = this.visual;
    const { renderWidth: rW, renderHeight: rH } = size;
    gl.bindBuffer(gl.ARRAY_BUFFER, this.quad);

    // 1. The visual at its render size, reading its previous frame; during a switch the
    // outgoing one too.
    const next = this.drawVisual(layer, size.feedback, rW, rH, dt, audio, reducedMotion);
    size.feedback.reverse();
    let shown = next;
    if (p !== null && this.outgoing && size.outgoing) {
      const from = this.drawVisual(this.outgoing, size.outgoing, rW, rH, dt, audio, reducedMotion);
      size.outgoing.reverse();
      shown = this.blend(size, from, next, p, audio);
    }

    // 2. The present pass at the canvas size, into the frame and the next history. It samples
    // the visual with normalized coordinates, so a smaller render size scales up here.
    this.packPresent(W, H, dt, reducedMotion);
    gl.viewport(0, 0, W, H);
    gl.useProgram(this.present);
    gl.bindBuffer(gl.UNIFORM_BUFFER, this.presentUbo);
    gl.bufferSubData(gl.UNIFORM_BUFFER, 0, this.presentBlock);
    gl.bindBufferBase(gl.UNIFORM_BUFFER, 0, this.presentUbo);
    const [histPrev, histNext] = size.history;
    bind(gl, 0, shown.tex);
    bind(gl, 1, shown.tex);
    bind(gl, 2, histPrev.tex);
    bind(gl, 3, null);
    bind(gl, 4, null);
    gl.bindFramebuffer(gl.FRAMEBUFFER, histNext.fbo);
    gl.drawArrays(gl.TRIANGLES, 0, 3);
    // While fading, the limiter is fed black, so the output falls to black through its grant.
    if (fading) gl.clearBufferfv(gl.COLOR, 0, [0, 0, 0, 1]);
    size.history = [histNext, histPrev];

    // Show black for SCALE_BLACK_S from the first black frame after a scale change or restore.
    // The visual and its history keep running; the limiter starts from black with every budget
    // spent (resize). Run the limiter again once the interval ends, even if playback is paused.
    if (this.black > 0 && this.blackShown) this.black = Math.max(0, this.black - shownMs / 1000);
    if (this.black > 0) {
      this.blackShown = true;
      gl.bindFramebuffer(gl.FRAMEBUFFER, null);
      gl.clearColor(0, 0, 0, 1);
      gl.clear(gl.COLOR_BUFFER_BIT);
      return;
    }

    // 3. The frame flash limiter, on the time the frames are shown apart (shownMs).
    this.limit(size, shownMs);

    // 4. The limited frame to the canvas.
    this.showLimited(size);
  }

  /** Withdraws the last frame drawn, before it is painted: the canvas shows the limiter's previous
   * output again and the limiter's state goes back to before the frame, so the next draw judges
   * afresh. A host calls it when the slot's size changes between a draw and its paint, so a frame
   * judged against the old area limit is never shown in the new one (Codex round 18). */
  withdraw(): void {
    const u = this.undo;
    const size = this.size;
    this.undo = null;
    if (!u || !size) return;
    // A frame that switched the scale keeps its black: the old frame is gone.
    if (u.size === size) {
      this.fadeLeft = u.fadeLeft;
      this.black = u.black;
      this.blackShown = u.blackShown;
    }
    // A black frame stays black: it was judged against no area limit.
    const l = u.limiter;
    if (!l) return;
    size.limiter.cur = l.cur;
    this.clockMs = l.clockMs;
    this.refillCarry = l.carry;
    this.limiterSpent = l.spent;
    this.grantSettled = false;
    this.showLimited(size);
  }

  /** The slot shows nothing (a zero width or height): the canvas goes black and the limiter
   * forgets its visible region, so a reopened slot exposes black area and spends the budgets like
   * any newly shown area. Its backing keeps the minimum size, which the slot's key alone would not
   * tell apart from a one-pixel slot (Codex round 20). */
  collapse(): void {
    const size = this.size;
    this.undo = null;
    if (!size) return;
    size.visibleWidth = 0;
    size.visibleHeight = 0;
    size.visiblePxWidth = 0;
    size.visiblePxHeight = 0;
    this.grantSettled = false;
    this.showLimited(size);
  }

  // The limited frame to the canvas: its visible part only, the rest black. A slot that grows
  // before the next draw then reveals black, never pixels the limiter has not judged in view
  // (Codex round 17). GL rows run bottom-up, so the top-left anchored visible rows are the top
  // ones.
  private showLimited(size: Size): void {
    const gl = this.gl;
    const L = size.limiter;
    const { height: H, visibleWidth: vw, visibleHeight: vh } = size;
    gl.bindFramebuffer(gl.DRAW_FRAMEBUFFER, null);
    gl.clearColor(0, 0, 0, 1);
    gl.clear(gl.COLOR_BUFFER_BIT);
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, L.fOut[L.cur]);
    gl.blitFramebuffer(0, H - vh, vw, H, 0, H - vh, vw, H, gl.COLOR_BUFFER_BIT, gl.NEAREST);
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, null);
  }

  /** The frame shown: its size in texels, the texel's side in physical pixels, and the physical
   * pixels of it the limiter has judged in view, from the top left. During a display-scale
   * change's fade it is the old one, which the host keeps presenting at its old size. The host
   * clips the canvas to the view: a slot grown since the draw, or past a texel it cut, then shows
   * no pixel the limiter has not judged (Codex round 24). */
  get shown(): {
    width: number;
    height: number;
    texel: number;
    viewWidth: number;
    viewHeight: number;
  } | null {
    const size = this.size;
    return size
      ? {
          width: size.width,
          height: size.height,
          texel: this.displayScale,
          viewWidth: size.visiblePxWidth,
          viewHeight: size.visiblePxHeight,
        }
      : null;
  }

  /** Whether the shown frame can be held for this request while playback is paused. */
  settled(
    width: number,
    height: number,
    texel: number,
    slotWidth = width * texel,
    slotHeight = height * texel,
  ): boolean {
    const size = this.size;
    if (!size || this.fadeLeft > 0 || this.black > 0 || texel !== this.displayScale) return false;
    const [W, H] = frameSize(width, height);
    if (size.width !== W || size.height !== H) return false;
    const v = this.inView(size, slotWidth, slotHeight);
    if (
      v.held ||
      size.visibleWidth !== v.width ||
      size.visibleHeight !== v.height ||
      size.visiblePxWidth !== v.pxWidth ||
      size.visiblePxHeight !== v.pxHeight
    )
      return false;
    // A grant state the host wrote spent has judged no frame, so its clear held bits say nothing
    // about the input: a withdrawn first draw would otherwise hold black (Codex round 24).
    if (this.limiterSpent) return false;
    if (this.grantSettled) return true;
    // A denied direction can leave a steady input held. Read the last grant only when
    // deciding whether to stop drawing, and reuse an all-admitted result until the next draw.
    const gl = this.gl;
    const L = size.limiter;
    this.grantWord[0] = 0;
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, L.fG[L.cur]);
    gl.readPixels(0, 0, 1, 1, gl.RGBA_INTEGER, gl.UNSIGNED_INT, this.grantWord);
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, null);
    // Bits 4 to 7 are the directions some pixel fires in that the grant did not admit (Oscillux
    // v0.2.9): clear, the output shows the input exactly (Codex round 18).
    this.grantSettled = (this.grantWord[0] & 0xf0) === 0;
    return this.grantSettled;
  }

  // The part of the frame at the current scale that a slotWidth x slotHeight slot shows, from the
  // top left, given the part judged in view so far: physical pixels, and texels including one the
  // slot cuts. A slot that shrinks may cut a texel: it stays partly on screen, so it is judged and
  // shown like the rest (Codex round 23). A slot that grows shows more whole texels, each exposed
  // black (expose), or from nothing in view under one texel, that texel cut. Past a texel it cut
  // there is nothing safe to show: the texel's hidden pixels would appear in its colour with no
  // tracker event, and clearing the texel would drop the pixels it shows with no grant. The view
  // is then held, and draw fades the frame out and starts the view again (Codex round 24).
  private inView(
    size: Pick<Size, "width" | "height" | "visiblePxWidth" | "visiblePxHeight">,
    slotWidth: number,
    slotHeight: number,
  ): { width: number; height: number; pxWidth: number; pxHeight: number; held: boolean } {
    const s = this.displayScale;
    let held = false;
    const axis = (slot: number, texels: number, seen: number): number => {
      const px = Math.max(0, Math.min(Math.floor(slot), texels * s));
      if (px <= seen) return px;
      if (seen % s === 0) return px < s ? px : Math.floor(px / s) * s;
      held = true;
      return seen;
    };
    const pxWidth = axis(slotWidth, size.width, size.visiblePxWidth);
    const pxHeight = axis(slotHeight, size.height, size.visiblePxHeight);
    return {
      width: Math.ceil(pxWidth / s),
      height: Math.ceil(pxHeight / s),
      pxWidth,
      pxHeight,
      held,
    };
  }

  // Whether this frame fades to black before a display-scale change to `texel`: for SCALE_FADE_S
  // of the time the limiter is credited with (shownMs), after the change is first asked for, so
  // the fall's grant has come whatever the frame rate (Codex round 22). A change asked for before
  // the first frame, back to the current scale, or down to a divisor of it needs none: the
  // divisor keeps every pixel on screen (resize). A held view (`reseat`) fades at any scale. A
  // spend during the fade starts it again (spend).
  private fading(texel: number, shownS: number, reseat = false): boolean {
    if (
      !this.size ||
      this.displayScale === 0 ||
      (!reseat && (texel === this.displayScale || exactDrop(this.displayScale, texel)))
    ) {
      this.fadeLeft = -1;
      return false;
    }
    this.fadeLeft = this.fadeLeft < 0 ? SCALE_FADE_S : this.fadeLeft - shownS;
    return this.fadeLeft > 0;
  }

  // Draws a visual from feedback[0] into feedback[1] and returns that target.
  private drawVisual(
    v: Layer,
    feedback: Target[],
    W: number,
    H: number,
    dt: number,
    audio: AudioFrame,
    reducedMotion: boolean,
  ): Target {
    const gl = this.gl;
    const [prev, next] = feedback;
    gl.useProgram(v.program);
    this.setVisualUniforms(v, W, H, dt, audio, reducedMotion);
    bind(gl, 0, v.image);
    bind(gl, 1, prev.tex);
    gl.bindFramebuffer(gl.FRAMEBUFFER, next.fbo);
    gl.viewport(0, 0, W, H);
    gl.drawArrays(gl.TRIANGLES, 0, 3);
    return next;
  }

  // Oscillux's switch pass from the outgoing frame to the incoming one at progress p, into the
  // blend target at the render size: SwitchBlock switch0 = (p, beatPhase, energy, focus),
  // switch1 = (motion, aspect, style, 0), size = (W, H, 0, 0); textures from, to and photo (the
  // incoming image, which Luma Key does not read).
  private blend(size: Size, from: Target, to: Target, p: number, audio: AudioFrame): Target {
    const gl = this.gl;
    const W = size.renderWidth;
    const H = size.renderHeight;
    if (!size.blend) {
      const { internal, type } = this.feedbackFormat;
      const tex = this.texture(W, H, internal, gl.RGBA, type, gl.LINEAR);
      size.blend = { tex, fbo: this.framebuffer([tex], false) };
    }
    const energy = Math.min(1, Math.max(0, audio.values.level));
    this.switchBlock.set([
      p,
      audio.values.beatPhase,
      energy,
      SWITCH_FOCUS,
      1,
      W / H,
      SWITCH_STYLE,
      0,
      W,
      H,
      0,
      0,
    ]);
    gl.bindBuffer(gl.UNIFORM_BUFFER, this.switchUbo);
    gl.bufferSubData(gl.UNIFORM_BUFFER, 0, this.switchBlock);
    gl.bindBufferBase(gl.UNIFORM_BUFFER, 0, this.switchUbo);
    this.pass(this.switchPass, size.blend.fbo, W, H, [from.tex, to.tex, this.image]);
    return size.blend;
  }

  /**
   * Sets the visual's image input: the track's artwork, or the neutral stand-in for null. The
   * texture keeps its own copy, so the caller may close the bitmap afterwards. During a switch
   * the outgoing visual keeps the image it had.
   */
  setImage(image: ImageBitmap | null): void {
    if (this.disposed) return;
    const gl = this.gl;
    if (this.outgoing && this.outgoing.image === this.image) {
      this.image = must(gl.createTexture());
      if (this.visual) this.visual.image = this.image;
    }
    gl.bindTexture(gl.TEXTURE_2D, this.image);
    if (image) {
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, gl.RGBA, gl.UNSIGNED_BYTE, image);
    } else {
      const grey = new Uint8Array([128, 128, 128, 255]);
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, 1, 1, 0, gl.RGBA, gl.UNSIGNED_BYTE, grey);
    }
    filter(gl, gl.LINEAR);
  }

  /** Releases every GL and Oscillux resource; safe on a partly built renderer, and twice. */
  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    const gl = this.gl;
    this.endSwitch();
    this.freeSize();
    for (const p of this.linked) gl.deleteProgram(p);
    for (const s of this.shaders) gl.deleteShader(s);
    // Fields a failed constructor never reached are undefined; GL treats them as null.
    gl.deleteBuffer(this.quad ?? null);
    gl.deleteBuffer(this.presentUbo ?? null);
    gl.deleteBuffer(this.limiterUbo ?? null);
    gl.deleteBuffer(this.switchUbo ?? null);
    gl.deleteTexture(this.image ?? null);
    this.linked.length = 0;
    this.shaders.length = 0;
    this.programs.clear();
    this.visual = null;
    this.pending = null;
    this.transition?.free();
    this.governor?.free();
    this.regions?.free();
    this.overlay?.free();
  }

  private setVisualUniforms(
    v: Layer,
    W: number,
    H: number,
    dt: number,
    audio: AudioFrame,
    reducedMotion: boolean,
  ): void {
    const gl = this.gl;
    const at = (name: string) => {
      let loc = v.locations.get(name);
      if (loc === undefined) {
        loc = gl.getUniformLocation(v.program, name);
        v.locations.set(name, loc);
      }
      return loc;
    };
    const t = v.visual;
    gl.uniform2f(at("u.resolution"), W, H);
    gl.uniform1f(at("u.time"), this.time);
    gl.uniform1f(at("u.dt"), dt);
    gl.uniform1f(at("u.sensitivity"), 1);
    gl.uniform1f(at("u.param0"), t.params[0]);
    gl.uniform1f(at("u.param1"), t.params[1]);
    gl.uniform1f(at("u.param2"), t.params[2]);
    gl.uniform1f(at("u.trailDecay"), reducedMotion ? 0 : t.trailDecay);
    gl.uniform4fv(at("u.accent"), ACCENT);
    gl.uniform4fv(at("u.motion"), MOTION);
    // Oscillux's palette tour: the visual cross-fades through the vivid palettes (CORE-2369).
    gl.uniform4f(at("u.palette"), -1, 0, this.osc.paletteTour(this.time, t.paletteOffset), 0);
    gl.uniform4fv(at("u.presentation0"), PRESENTATION);
    gl.uniform4f(
      at("u.presentation1"),
      this.presentBlock[OVERLAY_FLOAT],
      QUALITY_LADDER[this.level].reduce,
      0,
      0,
    );
    gl.uniform4fv(at("u.safeRect"), SAFE_RECT);
    for (const k of AUDIO_KEYS) gl.uniform1f(at(`u.${k}`), audio.values[k]);
    gl.uniform4fv(at("u.spectrum[0]"), audio.spectrum);
    gl.uniform1i(at("uImage"), 0);
    gl.uniform1i(at("uPrev"), 1);
  }

  // present0 (Oscillux's vivid saturation, no switch, history on, vivid vibrance), the overlay block the wasm
  // core packs for this frame, post0 (no bloom, aberration or grain; dither of one code) and
  // post1 (the frame seed).
  private packPresent(W: number, H: number, dt: number, reducedMotion: boolean): void {
    const o = this.overlay;
    o.viewportWidth = W;
    o.viewportHeight = H;
    o.frameIntervalMs = Math.min(Math.max(dt * 1000, 1e-3), 1000);
    o.reducedMotion = reducedMotion;
    const status = this.regions.pack(o);
    if (status !== 0) throw new Error(`visualizer: overlay ${this.osc.statusString(status)}`);
    const block = this.presentBlock;
    block.set([this.vivid[0], 1, 1, this.vivid[1]]);
    block.set(
      new Float32Array(this.osc.wasmMemory().buffer, this.regions.uniformsPtr(), OVERLAY_BYTES / 4),
      OVERLAY_FLOAT,
    );
    block.set([0, 0, 0, 1, this.presented++ % 16777216, 0, 0, 0], POST_FLOAT);
  }

  private limit(size: Size, frameMs: number): void {
    const gl = this.gl;
    const L = size.limiter;
    if (this.undo)
      this.undo.limiter = {
        cur: L.cur,
        clockMs: this.clockMs,
        carry: this.refillCarry,
        spent: this.limiterSpent,
      };
    this.clockMs += frameMs;
    // The interval up to a resize may age copied trackers, but cannot refill its new deficit.
    // The fraction of a unit carries to the next frame, so the refill keeps the exact rate. A
    // token is TOKEN - 1 units of credit, so a carried fraction cannot finish one early (Codex
    // round 16).
    if (this.limiterSpent) this.refillCarry = 0;
    const refillMs = this.limiterSpent ? 0 : frameMs;
    const earned =
      this.refillCarry +
      Math.min(1, Math.max(0, (2 * MAX_FLASH_HZ * refillMs) / 1000)) * (TOKEN - 1);
    const refill = Math.min(TOKEN, Math.floor(earned));
    this.refillCarry = Math.min(1, earned - refill);
    const tick = Math.floor(this.clockMs / TICK_MS) % TICKS;
    this.limiterBlock.set([
      L.window.cols,
      L.window.rows,
      L.window.limit,
      refill,
      tick,
      frameMs,
      0,
      0,
    ]);
    gl.bindBuffer(gl.UNIFORM_BUFFER, this.limiterUbo);
    gl.bufferSubData(gl.UNIFORM_BUFFER, 0, this.limiterBlock);
    gl.bindBufferBase(gl.UNIFORM_BUFFER, 0, this.limiterUbo);
    const a = L.cur;
    const b = a ^ 1;
    const [rows, reduce, cells, windows, grant, output] = this.limiter;
    const W = size.width;
    const H = size.height;
    this.pass(rows, L.fRows, 64, H, [size.frame, L.track[a]]);
    this.pass(reduce, L.fS, 64, 36, [L.rows]);
    this.pass(cells, L.fC, 16, 9, [L.s]);
    this.pass(windows, L.fW, 16, 9, [L.c]);
    this.pass(grant, L.fG[b], 1, 1, [L.c, L.w, L.g[a]]);
    this.pass(output, L.fOut[b], W, H, [size.frame, L.out[a], L.track[a], L.g[b]]);
    L.cur = b;
    this.limiterSpent = false;
    this.grantSettled = false;
  }

  private pass(
    p: WebGLProgram,
    fbo: WebGLFramebuffer,
    w: number,
    h: number,
    inputs: WebGLTexture[],
  ): void {
    const gl = this.gl;
    gl.useProgram(p);
    gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
    gl.viewport(0, 0, w, h);
    inputs.forEach((t, unit) => bind(gl, unit, t));
    gl.drawArrays(gl.TRIANGLES, 0, 3);
  }

  // The targets for a W x H canvas, rebuilt when the size changes. The flash limiter's state is
  // never cleared after the start and every resize spends its budgets, so resizing never hands a
  // strobe a fresh flash budget (CORE-2516). `reset` starts the frame again as a display-scale
  // change does, at any size (a held view, draw).
  private resize(W: number, H: number, texel: number, reset = false): Size {
    if (
      !reset &&
      this.size &&
      this.size.width === W &&
      this.size.height === H &&
      texel === this.displayScale
    ) {
      return this.size;
    }
    // A size change ends a switch, as in the reference host.
    this.endSwitch();
    const old = this.size;
    const kept = old?.limiter ?? null;
    this.freeSize(kept !== null);
    const gl = this.gl;
    const canvas = gl.canvas as HTMLCanvasElement;
    canvas.width = W;
    canvas.height = H;
    // Physical pixels per texel: a resize at the same scale keeps every on-screen position, and
    // so does a drop to a divisor of the scale, each old texel becoming k x k new ones.
    const k = exactDrop(this.displayScale, texel) ? this.displayScale / texel : 1;
    const sameScale = texel === this.displayScale || k > 1;
    this.displayScale = texel;
    const { internal, type } = this.feedbackFormat;
    const render = renderQuality(W, H, this.level);
    const feedback = this.feedbackPair(render.width, render.height, internal, type);
    const frame = this.storage(W, H, gl.RGBA8);
    const history = [0, 1].map(() => {
      const tex = this.texture(W, H, internal, gl.RGBA, type, gl.NEAREST);
      const fbo = this.framebuffer([frame, tex]);
      gl.clearBufferfv(gl.COLOR, 1, [0, 0, 0, 0]);
      return { tex, fbo };
    });
    const rows = this.storage(64, H, gl.RGBA32UI);
    const out = [0, 1].map(() => this.storage(W, H, gl.RGBA8));
    const track = [0, 1].map(() => this.storage(W, H, gl.RGBA16UI));
    const fOut = out.map((x, i) => this.framebuffer([x, track[i]]));
    for (const f of fOut) this.clearOutput(f);
    // The view the limiter has judged: at the same scale the old one, in physical pixels, so a
    // texel the old slot cut stays cut (Codex round 24); otherwise none, with every texel black.
    // Draw exposes the rest of the visible slot.
    const carried = kept !== null && old !== null && sameScale && !reset;
    const px: [number, number] = carried
      ? [Math.min(W * texel, old.visiblePxWidth), Math.min(H * texel, old.visiblePxHeight)]
      : [0, 0];
    const visible: [number, number] = carried
      ? [Math.ceil(px[0] / texel), Math.ceil(px[1] / texel)]
      : [W, H];
    const window = carried
      ? limiterWindow(W, H, texel, visible[0], visible[1], px[0], px[1])
      : limiterWindow(W, H, texel);
    const limiter: Limiter = kept
      ? { ...kept, rows, out, track, fRows: this.framebuffer([rows]), fOut, window }
      : this.newLimiterState(rows, out, track, fOut, window);
    // The limiter's history follows what is on screen (Oscillux shaders/limiter/README.md,
    // "Ping-pong and reset"): at the same scale the overlap is copied 1:1 from the top-left,
    // where the canvas is anchored, and new area starts tracked black. A drop to a divisor of
    // the scale copies each texel and its tracker k x k times, so the screen does not change
    // (Codex round 22). After a scale change, a reset or a context restore, the whole frame
    // starts tracked black and the canvas presents black for SCALE_BLACK_S (draw).
    // Only the old visible region is copied: pixels hidden by a clipped slot were never judged
    // in view, so they start black and tracked black like new area (Codex round 17).
    if (carried) {
      const i = kept.cur;
      const vw = old.visibleWidth;
      const vh = old.visibleHeight;
      this.copyAnchored(kept.out[i], old.width, old.height, out[i], W, H, vw, vh, k);
      this.copyAnchored(kept.track[i], old.width, old.height, track[i], W, H, vw, vh, k);
    } else if (kept || this.restored) {
      this.black = SCALE_BLACK_S;
      this.blackShown = false;
    }
    // Every size starts with the budgets written spent, the first one too: the host cannot
    // know what the canvas replaced on screen, so the first frame is rationed like any change
    // after a host write (Oscillux shaders/limiter/README.md, Codex round 24).
    this.spend(limiter);
    if (kept) {
      gl.deleteTexture(kept.rows);
      gl.deleteFramebuffer(kept.fRows);
      [...kept.out, ...kept.track].forEach((t) => gl.deleteTexture(t));
      kept.fOut.forEach((f) => gl.deleteFramebuffer(f));
    }
    this.size = {
      width: W,
      height: H,
      visibleWidth: visible[0],
      visibleHeight: visible[1],
      visiblePxWidth: px[0],
      visiblePxHeight: px[1],
      renderWidth: render.width,
      renderHeight: render.height,
      feedback,
      outgoing: null,
      blend: null,
      frame,
      history,
      limiter,
    };
    return this.size;
  }

  // A slot that grows within an unchanged backing shows pixels the limiter last saw while they
  // were hidden. They start black and tracked black, as newly exposed area does on a resize, and
  // the budgets are spent, so revealing hidden content is rationed like any rise (Oscillux
  // shaders/limiter/README.md, Codex round 16). Any change of the visible size changes the area
  // limit, so a shrink spends the budgets too: a grant earned under the old limit is not spent
  // under the new one (Codex round 17).
  private expose(size: Size, visibleWidth: number, visibleHeight: number): void {
    const { height: H, visibleWidth: oldWidth, visibleHeight: oldHeight } = size;
    const gl = this.gl;
    const L = size.limiter;
    // GL rows run bottom-up, so the top-left anchored visible rows are the top ones.
    const regions: [number, number, number, number][] = [];
    if (visibleWidth > oldWidth)
      regions.push([oldWidth, H - visibleHeight, visibleWidth - oldWidth, visibleHeight]);
    if (visibleHeight > oldHeight)
      regions.push([0, H - visibleHeight, visibleWidth, visibleHeight - oldHeight]);
    gl.enable(gl.SCISSOR_TEST);
    for (const [x, y, w, h] of regions) {
      gl.scissor(x, y, w, h);
      for (const f of L.fOut) this.clearOutput(f);
    }
    gl.disable(gl.SCISSOR_TEST);
    gl.bindFramebuffer(gl.FRAMEBUFFER, null);
    this.spend(L);
  }

  // Marks every limiter budget just spent, in the grant state the next frame reads. A fade to
  // black waits out the grant of its fall, so a spend during one starts it again: the switch at
  // its end would otherwise clear a frame the limiter still holds (Codex round 24). The spend
  // outlives a withdrawn frame, so the restart does too: the fade a withdraw goes back to starts
  // again even when the frame that spent had cancelled it (Codex round 25).
  private spend(L: Limiter): void {
    const gl = this.gl;
    gl.bindFramebuffer(gl.FRAMEBUFFER, L.fG[L.cur]);
    gl.clearBufferuiv(gl.COLOR, 0, SPENT);
    gl.bindFramebuffer(gl.FRAMEBUFFER, null);
    this.limiterSpent = true;
    if (this.fadeLeft > 0) this.fadeLeft = SCALE_FADE_S;
    if (this.undo && this.undo.fadeLeft > 0) this.undo.fadeLeft = SCALE_FADE_S;
  }

  // Moves the visual to a governor level between switches: only its feedback pair is rebuilt,
  // at the new render size, with the last frame scaled in so the trails carry on. The present
  // history and the flash limiter stay at the canvas size and keep their state.
  private setLevel(size: Size, level: number): void {
    if (level === this.level) return;
    this.level = Math.min(TOP_LEVEL, Math.max(0, level));
    const render = renderQuality(size.width, size.height, this.level);
    if (render.width === size.renderWidth && render.height === size.renderHeight) return;
    const { internal, type } = this.feedbackFormat;
    const feedback = this.feedbackPair(render.width, render.height, internal, type, false);
    this.copy(
      size.feedback[0],
      size.renderWidth,
      size.renderHeight,
      feedback[0],
      render.width,
      render.height,
    );
    for (const t of size.feedback) {
      this.gl.deleteTexture(t.tex);
      this.gl.deleteFramebuffer(t.fbo);
    }
    size.feedback = feedback;
    size.renderWidth = render.width;
    size.renderHeight = render.height;
  }

  // Copies the overlap of one texture's top-left vw x vh into another of the same format, each
  // texel k x k times, through framebuffers of one attachment each (an integer blit must be
  // NEAREST and match every draw buffer).
  private copyAnchored(
    from: WebGLTexture,
    fw: number,
    fh: number,
    to: WebGLTexture,
    tw: number,
    th: number,
    vw = fw,
    vh = fh,
    k = 1,
  ): void {
    const gl = this.gl;
    const read = this.framebuffer([from], false);
    const draw = this.framebuffer([to], false);
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, read);
    gl.bindFramebuffer(gl.DRAW_FRAMEBUFFER, draw);
    // GL rows run bottom-up, so the top-left anchor is the top rows of each. Only the source's
    // top-left vw x vh is copied, in whole texels.
    // A texel the target cuts is copied too, its pixels outside the target clipped: they are on
    // screen in both (Codex round 23).
    const w = Math.min(vw, Math.ceil(tw / k));
    const h = Math.min(vh, Math.ceil(th / k));
    gl.blitFramebuffer(0, fh - h, w, fh, 0, th - h * k, w * k, th, gl.COLOR_BUFFER_BIT, gl.NEAREST);
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, null);
    gl.bindFramebuffer(gl.DRAW_FRAMEBUFFER, null);
    gl.deleteFramebuffer(read);
    gl.deleteFramebuffer(draw);
  }

  // Copies one target into another, scaling with linear filtering when the sizes differ.
  private copy(from: Target, fw: number, fh: number, to: Target, tw: number, th: number): void {
    const gl = this.gl;
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, from.fbo);
    gl.bindFramebuffer(gl.DRAW_FRAMEBUFFER, to.fbo);
    const scaled = fw !== tw || fh !== th;
    gl.blitFramebuffer(
      0,
      0,
      fw,
      fh,
      0,
      0,
      tw,
      th,
      gl.COLOR_BUFFER_BIT,
      scaled ? gl.LINEAR : gl.NEAREST,
    );
    gl.bindFramebuffer(gl.READ_FRAMEBUFFER, null);
    gl.bindFramebuffer(gl.DRAW_FRAMEBUFFER, null);
  }

  private feedbackPair(
    W: number,
    H: number,
    internal: number,
    type: number,
    verify = true,
  ): Target[] {
    const gl = this.gl;
    return [0, 1].map(() => {
      const tex = this.texture(W, H, internal, gl.RGBA, type, gl.LINEAR);
      const fbo = this.framebuffer([tex], verify);
      gl.clearBufferfv(gl.COLOR, 0, [0, 0, 0, 1]);
      return { tex, fbo };
    });
  }

  // A fresh limiter: its output targets arrive black and tracked black, and resize writes its
  // budgets spent.
  private newLimiterState(
    rows: WebGLTexture,
    out: WebGLTexture[],
    track: WebGLTexture[],
    fOut: WebGLFramebuffer[],
    window: LimiterWindow,
  ): Limiter {
    const gl = this.gl;
    const s = this.storage(64, 36, gl.RGBA32UI);
    const c = this.storage(16, 9, gl.RGBA32UI);
    const w = this.storage(16, 9, gl.RGBA16UI);
    const g = [0, 1].map(() => this.storage(1, 1, gl.RGBA16UI));
    const limiter: Limiter = {
      rows,
      s,
      c,
      w,
      g,
      track,
      out,
      fRows: this.framebuffer([rows]),
      fS: this.framebuffer([s]),
      fC: this.framebuffer([c]),
      fW: this.framebuffer([w]),
      fG: g.map((x) => this.framebuffer([x])),
      fOut,
      window,
      cur: 0,
    };
    for (const f of limiter.fG) this.clearUint(f, 1);
    return limiter;
  }

  // Frees the size's targets. With keepLimiter the limiter's textures stay for resize to carry
  // over (and free its size-bound ones after copying the last output).
  private freeSize(keepLimiter = false): void {
    const s = this.size;
    if (!s) return;
    const gl = this.gl;
    const L = s.limiter;
    for (const t of [...s.feedback, ...s.history]) {
      gl.deleteTexture(t.tex);
      gl.deleteFramebuffer(t.fbo);
    }
    gl.deleteTexture(s.frame);
    if (!keepLimiter) {
      for (const t of [L.rows, L.s, L.c, L.w, ...L.g, ...L.track, ...L.out]) {
        gl.deleteTexture(t);
      }
      for (const f of [L.fRows, L.fS, L.fC, L.fW, ...L.fG, ...L.fOut]) gl.deleteFramebuffer(f);
    }
    this.size = null;
  }

  private texture(
    w: number,
    h: number,
    internal: number,
    format: number,
    type: number,
    mode: number,
  ) {
    const gl = this.gl;
    const t = must(gl.createTexture());
    gl.bindTexture(gl.TEXTURE_2D, t);
    gl.texImage2D(gl.TEXTURE_2D, 0, internal, w, h, 0, format, type, null);
    filter(gl, mode);
    return t;
  }

  private storage(w: number, h: number, internal: number): WebGLTexture {
    const gl = this.gl;
    const t = must(gl.createTexture());
    gl.bindTexture(gl.TEXTURE_2D, t);
    gl.texStorage2D(gl.TEXTURE_2D, 1, internal, w, h);
    filter(gl, gl.NEAREST);
    return t;
  }

  // checkFramebufferStatus waits on the GPU process, including any shader still compiling in
  // parallel (over 500 ms seen on a switch), so the targets a switch adds skip it: they reuse
  // the feedback format resize already checked.
  private framebuffer(attachments: WebGLTexture[], verify = true): WebGLFramebuffer {
    const gl = this.gl;
    const f = must(gl.createFramebuffer());
    gl.bindFramebuffer(gl.FRAMEBUFFER, f);
    attachments.forEach((t, i) =>
      gl.framebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0 + i, gl.TEXTURE_2D, t, 0),
    );
    gl.drawBuffers(attachments.map((_, i) => gl.COLOR_ATTACHMENT0 + i));
    if (verify && gl.checkFramebufferStatus(gl.FRAMEBUFFER) !== gl.FRAMEBUFFER_COMPLETE) {
      throw new Error("visualizer: incomplete framebuffer");
    }
    return f;
  }

  private clearUint(fbo: WebGLFramebuffer, attachments: number): void {
    const gl = this.gl;
    gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
    for (let i = 0; i < attachments; i++) gl.clearBufferuiv(gl.COLOR, i, new Uint32Array(4));
  }

  // An output target: the frame black and the trackers tracked black (zero), so the limiter
  // counts the first frame's rises from black (Oscillux shaders/limiter/README.md).
  private clearOutput(fbo: WebGLFramebuffer): void {
    const gl = this.gl;
    gl.bindFramebuffer(gl.FRAMEBUFFER, fbo);
    gl.clearBufferfv(gl.COLOR, 0, new Float32Array(4));
    gl.clearBufferuiv(gl.COLOR, 1, new Uint32Array(4));
  }

  private link(vertex: string, fragment: string): WebGLProgram {
    const p = this.compile(vertex, fragment);
    this.check(p);
    return p;
  }

  // Starts compiling and linking a program; check() reads the result, which waits for the
  // driver unless it has finished.
  private compile(vertex: string, fragment: string): WebGLProgram {
    const gl = this.gl;
    const p = must(gl.createProgram());
    // Owned from creation, so a failure below leaves them to dispose.
    this.linked.push(p);
    for (const [type, source] of [
      [gl.VERTEX_SHADER, vertex],
      [gl.FRAGMENT_SHADER, fragment],
    ] as const) {
      const s = must(gl.createShader(type));
      this.shaders.push(s);
      gl.shaderSource(s, source);
      gl.compileShader(s);
      gl.attachShader(p, s);
    }
    gl.bindAttribLocation(p, 0, "aPos");
    gl.linkProgram(p);
    return p;
  }

  private check(p: WebGLProgram): void {
    const gl = this.gl;
    if (gl.getProgramParameter(p, gl.LINK_STATUS) || gl.isContextLost()) return;
    for (const s of gl.getAttachedShaders(p) ?? []) {
      if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) {
        throw new Error(`visualizer: shader: ${gl.getShaderInfoLog(s)}`);
      }
    }
    throw new Error(`visualizer: program: ${gl.getProgramInfoLog(p)}`);
  }
}

function bind(gl: WebGL2RenderingContext, unit: number, tex: WebGLTexture | null): void {
  gl.activeTexture(gl.TEXTURE0 + unit);
  gl.bindTexture(gl.TEXTURE_2D, tex);
}

function filter(gl: WebGL2RenderingContext, mode: number): void {
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, mode);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, mode);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
}

function must<T>(value: T | null): T {
  if (value === null) throw new Error("visualizer: WebGL object refused");
  return value;
}
